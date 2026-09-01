// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/yousysadmin/pacer/internal/core/env"
	"github.com/yousysadmin/pacer/internal/core/health"
	"github.com/yousysadmin/pacer/internal/models/instance"
	"github.com/yousysadmin/pacer/internal/models/job"
	"github.com/yousysadmin/pacer/internal/models/pool"
	projectmodel "github.com/yousysadmin/pacer/internal/models/project"
	"github.com/yousysadmin/pacer/internal/testutil/runtimeutil"
)

func TestIsCapacityErrorCode_ExactMatch(t *testing.T) {
	for _, code := range []string{"InsufficientInstanceCapacity", "Unsupported", "SpotMaxPriceTooLow", "InstanceLimitExceeded", "UnfulfillableCapacity"} {
		if !isCapacityErrorCode(code) {
			t.Errorf("%s should be capacity", code)
		}
	}
	// Permanent config errors must fail fast, not burn 12 retries.
	for _, code := range []string{"UnsupportedOperation", "InvalidAMIID.NotFound", "UnauthorizedOperation", ""} {
		if isCapacityErrorCode(code) {
			t.Errorf("%s must not be capacity", code)
		}
	}
}

func TestIsCapacityError_WrappedText(t *testing.T) {
	if !isCapacityError(errors.New("api error InsufficientInstanceCapacity: no capacity")) {
		t.Error("substring fallback should match embedded code")
	}
	if !isCapacityError(&fakeAPIError{code: "SpotMaxPriceTooLow", msg: "x"}) {
		t.Error("typed smithy code should match")
	}
	if isCapacityError(&fakeAPIError{code: "UnsupportedOperation", msg: "arm64 AMI on x86 type"}) {
		t.Error("UnsupportedOperation is permanent")
	}
	if isCapacityError(nil) {
		t.Error("nil is not an error")
	}
}

func TestSpawnRunInstances_EmptyPoolIsPermanent(t *testing.T) {
	o := &Orchestrator{Runtime: &env.Runtime{}}
	for _, p := range []*pool.Pool{
		{Name: "no-types", SubnetIDs: []string{"subnet-1"}},
		{Name: "no-subnets", InstanceTypes: []string{"t3.micro"}},
	} {
		sc := &spawnContext{job: &job.Job{ID: "j"}, pool: p, project: &projectInfo{Name: "p"}}
		res, exhausted, err := o.spawnRunInstances(t.Context(), sc)
		if res != nil || exhausted || err == nil {
			t.Errorf("%s: want permanent error, got res=%v exhausted=%v err=%v", p.Name, res, exhausted, err)
		}
	}
}

func TestOrchestratorSafeTick_RecoversPanic(t *testing.T) {
	rt := &env.Runtime{Health: health.New()}
	err := safeTick(rt, orchestratorHealthComponent, func() error { panic("boom") })
	if err == nil || !strings.Contains(err.Error(), "orchestrator panic") {
		t.Fatalf("want orchestrator panic error, got %v", err)
	}
	if msg, ok := rt.Health.Get(orchestratorHealthComponent); !ok || !strings.Contains(msg, "boom") {
		t.Fatalf("health not set: %q %v", msg, ok)
	}
}

func TestDetach_SurvivesParentCancel(t *testing.T) {
	parent, cancel := context.WithCancel(t.Context())
	cancel()
	ctx, done := detach(parent)
	defer done()
	if ctx.Err() != nil {
		t.Fatal("detached ctx must not inherit cancellation")
	}
	if _, ok := ctx.Deadline(); !ok {
		t.Fatal("detached ctx must carry a deadline")
	}
}

func TestFleetClientToken_VariesPerClaimAndAttempt(t *testing.T) {
	t1 := time.Unix(1000, 0)
	t2 := time.Unix(2000, 0)
	a := fleetClientToken(&job.Job{ID: "j", Attempts: 0, ClaimedAt: &t1})
	b := fleetClientToken(&job.Job{ID: "j", Attempts: 1, ClaimedAt: &t1})
	c := fleetClientToken(&job.Job{ID: "j", Attempts: 0, ClaimedAt: &t2})
	d := fleetClientToken(&job.Job{ID: "j", Attempts: 0, ClaimedAt: &t1})
	if a == b || a == c {
		t.Fatalf("token must change with attempt or claim: %s %s %s", a, b, c)
	}
	if a != d {
		t.Fatalf("token must be stable within a claim: %s %s", a, d)
	}
	if got := fleetClientToken(&job.Job{ID: "j"}); got != "j-0-0" {
		t.Fatalf("nil ClaimedAt: %s", got)
	}
}

func TestTick_CancelledDuringSpawn_RequeuesWithoutAttempt(t *testing.T) {
	rt := runtimeutil.NewRuntime(t, &env.Config{})
	ctx := t.Context()
	if err := rt.Store.Project.Put(ctx, &projectmodel.Project{ID: "p", Name: "p", Scope: "repo", Tags: map[string]string{}}); err != nil {
		t.Fatal(err)
	}
	if err := rt.Store.Pool.Put(ctx, &pool.Pool{ID: "po", ProjectID: "p", Name: "default", IsDefault: true,
		AMIID: "ami-1", InstanceTypes: []string{"t3.small"}, SubnetIDs: []string{"s-1"}, MaxRuntimeMinutes: 60, MaxConcurrentRunners: 5}); err != nil {
		t.Fatal(err)
	}
	if err := rt.Store.Job.Put(ctx, &job.Job{ID: "j", GHJobID: 1, GHRunID: 1, InstallationID: 1, RepoFullName: "o/r",
		ProjectID: "p", PoolID: "po", Status: job.StatusQueued, Payload: []byte("{}")}); err != nil {
		t.Fatal(err)
	}

	// Claim succeeds on a live ctx, then the ctx is cancelled before
	// spawn runs its first store call. Simulate by claiming here and
	// invoking the same failure path tick takes.
	o := &Orchestrator{Runtime: rt}
	cctx, cancel := context.WithCancel(ctx)
	j, err := rt.Store.Job.Claim(cctx, time.Now().UTC())
	if err != nil || j == nil {
		t.Fatalf("claim: %v %v", j, err)
	}
	cancel()
	spawnErr, exhausted := o.spawn(cctx, j)
	if exhausted || spawnErr == nil || !errors.Is(spawnErr, context.Canceled) {
		t.Fatalf("expected cancellation error from spawn, got %v %v", spawnErr, exhausted)
	}
	o.handleSpawnOutcome(cctx, j, spawnErr, exhausted)

	got, err := rt.Store.Job.Get(ctx, "j")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != job.StatusQueued || got.Attempts != 0 {
		t.Fatalf("want queued with 0 attempts, got %q/%d", got.Status, got.Attempts)
	}
}

func TestMaybeReap_MissingPoolUnderCapIsNoop(t *testing.T) {
	rt := runtimeutil.NewRuntime(t, &env.Config{})
	r := &Reaper{Runtime: rt}
	inst := &instance.Instance{ID: "i-1", JobID: "j", PoolID: "po-gone", LaunchedAt: time.Now()}
	if err := r.maybeReap(t.Context(), inst); err != nil {
		t.Fatalf("missing pool under cap must be a no-op, got %v", err)
	}
}

// Post-launch tagging is best-effort precisely because nothing the
// runner needs travels in it: the callback token comes from
// /api/runner/bootstrap and gha:managed-by (which the terminate
// policy is gated on) comes from the launch template. If a secret or
// a boot-critical tag is ever added here, that reasoning breaks and
// spawnFleet must stop shrugging off a CreateTags failure.
func TestPostLaunchTags_CarryNothingBootCritical(t *testing.T) {
	sc := &spawnContext{
		job:           &job.Job{ID: "j-1", RepoFullName: "octocat/hello-world"},
		pool:          &pool.Pool{Name: "linux"},
		project:       &projectInfo{Name: "alpha"},
		repoTags:      map[string]string{"cost-center": "eng"},
		callbackToken: "job.9999.deadbeef",
		tokenHash:     "sha256hash",
	}

	keys := map[string]string{}
	for _, tag := range postLaunchTags(sc) {
		keys[*tag.Key] = *tag.Value
	}

	for _, want := range []string{"gha:job_id", "gha:repo", "cost-center"} {
		if _, ok := keys[want]; !ok {
			t.Errorf("missing expected tag %q", want)
		}
	}
	for k, v := range keys {
		if strings.Contains(v, sc.callbackToken) || strings.Contains(v, sc.tokenHash) {
			t.Errorf("tag %q leaks a per-job credential", k)
		}
	}
	if _, ok := keys["gha:managed-by"]; ok {
		t.Error("gha:managed-by must come from the launch template, not a post-launch call the spawn tolerates failing")
	}
}
