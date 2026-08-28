// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/yousysadmin/pacer/internal/core/env"
	"github.com/yousysadmin/pacer/internal/core/health"
	"github.com/yousysadmin/pacer/internal/models/job"
	"github.com/yousysadmin/pacer/internal/models/pool"
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
