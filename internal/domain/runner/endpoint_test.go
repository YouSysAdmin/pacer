// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/yousysadmin/pacer/internal/core/callback"
	"github.com/yousysadmin/pacer/internal/core/env"
	auditmodel "github.com/yousysadmin/pacer/internal/models/audit"
	instancemodel "github.com/yousysadmin/pacer/internal/models/instance"
	jobmodel "github.com/yousysadmin/pacer/internal/models/job"
	poolmodel "github.com/yousysadmin/pacer/internal/models/pool"
	projectmodel "github.com/yousysadmin/pacer/internal/models/project"
	"github.com/yousysadmin/pacer/internal/testutil/runtimeutil"
)

var hmacKey = []byte("test-runner-callback-key-32-bytes-yes")

type harness struct {
	app *fiber.App
	rt  *env.Runtime
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	rt := runtimeutil.NewRuntime(t, &env.Config{})
	h := &Handler{Runtime: rt, HMACKey: hmacKey}
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Post("/api/runner/register", h.Register)
	app.Post("/api/runner/complete", h.Complete)
	app.Post("/api/runner/error", h.Error)
	return &harness{app: app, rt: rt}
}

func (h *harness) postJSON(t *testing.T, path string, body any) *http.Response {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.app.Test(req, -1)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	return resp
}

func seedProjectAndPool(t *testing.T, rt *env.Runtime) {
	t.Helper()
	if err := rt.Store.Project.Put(context.Background(), &projectmodel.Project{
		ID: "p-1", Name: "demo", Scope: projectmodel.ScopeRepo,
	}); err != nil {
		t.Fatalf("project: %v", err)
	}
	if err := rt.Store.Pool.Put(context.Background(), &poolmodel.Pool{
		ID: "po-1", ProjectID: "p-1", Name: "default", IsDefault: true, Priority: 100,
		AMIID: "ami-test", InstanceTypes: []string{"t3.large"},
		SubnetIDs: []string{"subnet-1"}, SecurityGroupIDs: []string{"sg-1"},
		MaxConcurrentRunners: 5, MaxRuntimeMinutes: 60, RootVolumeGB: 30, Spot: true,
	}); err != nil {
		t.Fatalf("pool: %v", err)
	}
}

// seedClaimedJob inserts a job in `claimed` status with a known callback
// token + linked instance row. Returns the token string.
func seedClaimedJob(t *testing.T, rt *env.Runtime, jobID, instanceID string) string {
	t.Helper()
	tok, hash := callback.Mint(jobID, hmacKey, time.Hour)
	now := time.Now().UTC()

	j := &jobmodel.Job{
		ID: jobID, GHJobID: time.Now().UnixNano() + int64(len(jobID)), GHRunID: 1, InstallationID: 1,
		RepoFullName: "octocat/hello-world", ProjectID: "p-1", PoolID: "po-1",
		Status: jobmodel.StatusClaimed, InstanceID: instanceID, CallbackTokenHash: hash,
		QueuedAt: now, ClaimedAt: &now, Payload: []byte("{}"),
	}
	if err := rt.Store.Job.Put(context.Background(), j); err != nil {
		t.Fatalf("job Put: %v", err)
	}
	if err := rt.Store.Instance.Put(context.Background(), &instancemodel.Instance{
		ID: instanceID, JobID: jobID, ProjectID: "p-1", PoolID: "po-1",
		State: instancemodel.StateStarting, LaunchedAt: now,
	}); err != nil {
		t.Fatalf("instance Put: %v", err)
	}
	return tok
}

func TestRunner_Register_RejectsBadToken(t *testing.T) {
	h := newHarness(t)
	seedProjectAndPool(t, h.rt)
	_ = seedClaimedJob(t, h.rt, "j-1", "i-1")

	resp := h.postJSON(t, "/api/runner/register", map[string]any{
		"job_id":         "j-1",
		"instance_id":    "i-1",
		"callback_token": "garbage.token.here",
	})
	if resp.StatusCode != 401 {
		t.Fatalf("status: want 401, got %d", resp.StatusCode)
	}
}

func TestRunner_Register_RejectsCrossJobToken(t *testing.T) {
	h := newHarness(t)
	seedProjectAndPool(t, h.rt)
	tokForJ1 := seedClaimedJob(t, h.rt, "j-1", "i-1")
	_ = seedClaimedJob(t, h.rt, "j-2", "i-2")

	// Token was minted for j-1; sending it as j-2's token must fail
	// (parsedJobID != in.JobID gate).
	resp := h.postJSON(t, "/api/runner/register", map[string]any{
		"job_id":         "j-2",
		"instance_id":    "i-2",
		"callback_token": tokForJ1,
	})
	if resp.StatusCode != 401 {
		t.Fatalf("status: want 401, got %d", resp.StatusCode)
	}
}

func TestRunner_Register_RejectsBadStatus(t *testing.T) {
	h := newHarness(t)
	seedProjectAndPool(t, h.rt)
	tok := seedClaimedJob(t, h.rt, "j-1", "i-1")
	if _, err := h.rt.DB.DB().ExecContext(context.Background(),
		`UPDATE jobs SET status='running' WHERE id=?`, "j-1"); err != nil {
		t.Fatalf("flip status: %v", err)
	}

	resp := h.postJSON(t, "/api/runner/register", map[string]any{
		"job_id":         "j-1",
		"instance_id":    "i-1",
		"callback_token": tok,
	})
	if resp.StatusCode != 400 {
		t.Fatalf("status: want 400, got %d", resp.StatusCode)
	}
}

func TestRunner_Register_MissingFields(t *testing.T) {
	h := newHarness(t)
	resp := h.postJSON(t, "/api/runner/register", map[string]any{})
	if resp.StatusCode != 400 {
		t.Fatalf("status: want 400, got %d", resp.StatusCode)
	}
}

func TestRunner_Complete_HappyPath(t *testing.T) {
	h := newHarness(t)
	seedProjectAndPool(t, h.rt)
	tok := seedClaimedJob(t, h.rt, "j-1", "i-1")

	resp := h.postJSON(t, "/api/runner/complete", map[string]any{
		"job_id":         "j-1",
		"callback_token": tok,
		"exit_code":      0,
	})
	if resp.StatusCode != 204 {
		t.Fatalf("status: want 204, got %d", resp.StatusCode)
	}

	inst, _ := h.rt.Store.Instance.Get(context.Background(), "i-1")
	if inst == nil || inst.State != instancemodel.StateTerminated {
		t.Fatalf("instance state: want terminated, got %v", inst)
	}

	entries, _ := h.rt.Store.Audit.List(context.Background(),
		auditmodel.ListFilter{Action: auditmodel.ActionInstanceTerminated})
	if len(entries) != 1 {
		t.Fatalf("want 1 instance.terminated audit entry, got %d", len(entries))
	}
}

func TestRunner_Complete_RejectsBadToken(t *testing.T) {
	h := newHarness(t)
	seedProjectAndPool(t, h.rt)
	_ = seedClaimedJob(t, h.rt, "j-1", "i-1")

	resp := h.postJSON(t, "/api/runner/complete", map[string]any{
		"job_id":         "j-1",
		"callback_token": "bogus.token.x",
	})
	if resp.StatusCode != 401 {
		t.Fatalf("status: want 401, got %d", resp.StatusCode)
	}
}

func TestRunner_Error_MarksFailedAndStoresLog(t *testing.T) {
	h := newHarness(t)
	seedProjectAndPool(t, h.rt)
	tok := seedClaimedJob(t, h.rt, "j-1", "i-1")

	resp := h.postJSON(t, "/api/runner/error", map[string]any{
		"job_id":         "j-1",
		"callback_token": tok,
		"stage":          "runner-download",
		"exit_code":      127,
		"line":           42,
		"log":            "curl: command not found\n",
	})
	if resp.StatusCode != 204 {
		t.Fatalf("status: want 204, got %d", resp.StatusCode)
	}

	got, _ := h.rt.Store.Job.Get(context.Background(), "j-1")
	if got == nil || got.Status != jobmodel.StatusFailed {
		t.Fatalf("job status: want failed, got %v", got)
	}
	if got.FailureStage != "runner-download" {
		t.Fatalf("failure_stage: %q", got.FailureStage)
	}
	if got.FailureLog == "" {
		t.Fatal("failure_log should be captured")
	}
}

func TestRunner_Error_TruncatesOversizedLog(t *testing.T) {
	h := newHarness(t)
	seedProjectAndPool(t, h.rt)
	tok := seedClaimedJob(t, h.rt, "j-1", "i-1")

	big := strings.Repeat("x", 80*1024)

	resp := h.postJSON(t, "/api/runner/error", map[string]any{
		"job_id":         "j-1",
		"callback_token": tok,
		"stage":          "bootstrap",
		"log":            big,
	})
	if resp.StatusCode != 204 {
		t.Fatalf("status: want 204, got %d", resp.StatusCode)
	}

	got, _ := h.rt.Store.Job.Get(context.Background(), "j-1")
	if got == nil {
		t.Fatal("job missing")
	}
	if !strings.Contains(got.FailureLog, "...[truncated]...") {
		t.Fatalf("expected truncation marker, got log of len %d", len(got.FailureLog))
	}
	// Cap is 64 KiB plus the short prefix; allow generous slack.
	if len(got.FailureLog) > 64*1024+64 {
		t.Fatalf("log not truncated: %d bytes", len(got.FailureLog))
	}
}

func TestSplitRepoFullName(t *testing.T) {
	owner, name, err := splitRepoFullName("octocat/hello-world")
	if err != nil || owner != "octocat" || name != "hello-world" {
		t.Fatalf("good: %q %q %v", owner, name, err)
	}
	// "a/b/c" is accepted today (SplitN with N=2 stops at the first slash);
	// it's not a real GH full_name but the helper isn't responsible for that.
	for _, bad := range []string{"", "no-slash", "/leading", "trailing/", "/"} {
		if _, _, err := splitRepoFullName(bad); err == nil {
			t.Errorf("expected error for %q", bad)
		}
	}
}
