// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package runner

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/yousysadmin/pacer/internal/core/ghapp"
	auditmodel "github.com/yousysadmin/pacer/internal/models/audit"
	jobmodel "github.com/yousysadmin/pacer/internal/models/job"
)

// stubMinter answers both mint calls with one canned error, standing
// in for GitHub refusing a registration.
type stubMinter struct{ err error }

func (s stubMinter) JITConfig(context.Context, int64, string, string, string, []string, int) (string, int64, error) {
	return "", 0, s.err
}

func (s stubMinter) JITConfigOrg(context.Context, int64, string, string, []string, int) (string, int64, error) {
	return "", 0, s.err
}

// registerWith drives a full register call against a minter that
// fails, and returns the response plus the decoded error envelope.
func registerWith(t *testing.T, mintErr error) (*http.Response, string) {
	t.Helper()
	h := newHarness(t)
	seedProjectAndPool(t, h.rt)
	tok := seedClaimedJob(t, h.rt, "j-mint", "i-mint")

	// Re-register the route against a handler wired to the stub.
	handler := &Handler{Runtime: h.rt, HMACKey: hmacKey, GHApp: stubMinter{err: mintErr}}
	h.app.Post("/api/runner/register-stub", handler.Register)

	resp := h.postJSON(t, "/api/runner/register-stub", map[string]any{
		"job_id": "j-mint", "instance_id": "i-mint",
		"instance_type": "t3.large", "az": "eu-west-1a", "callback_token": tok,
	})
	var body struct {
		Error string `json:"error"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return resp, body.Error
}

// TestRegister_PermanentGitHubRefusalIsNotRetryable: an App that lost
// access to the repo. The runner must be told to stop rather than
// spend its retry budget, and the response must carry GitHub's own
// sentence.
func TestRegister_PermanentGitHubRefusalIsNotRetryable(t *testing.T) {
	resp, msg := registerWith(t, &ghapp.APIError{
		Op: "jitconfig", StatusCode: http.StatusForbidden, Status: "403 Forbidden",
		Body: `{"message":"Resource not accessible by integration"}`,
	})

	// 424, not 5xx: curl's --retry treats 5xx as transient and would
	// re-ask twelve times over ~72 billed seconds.
	if resp.StatusCode != http.StatusFailedDependency {
		t.Fatalf("status: got %d, want 424", resp.StatusCode)
	}
	if !strings.Contains(msg, "Resource not accessible by integration") {
		t.Fatalf("message must carry GitHub's reason, got %q", msg)
	}
	if !strings.Contains(msg, "403") {
		t.Fatalf("message must carry the upstream status, got %q", msg)
	}
}

// TestRegister_TemporaryGitHubFailureIsRetryable: GitHub having a bad
// minute is exactly what the bootstrap's retry budget is for, so it
// has to land in curl's transient set.
func TestRegister_TemporaryGitHubFailureIsRetryable(t *testing.T) {
	resp, msg := registerWith(t, &ghapp.APIError{
		Op: "jitconfig", StatusCode: http.StatusBadGateway, Status: "502 Bad Gateway",
		Body: "upstream unavailable",
	})
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status: got %d, want 503", resp.StatusCode)
	}
	if !strings.Contains(msg, "502") {
		t.Fatalf("message should name the upstream status, got %q", msg)
	}
}

// TestRegister_NonAPIErrorStillReported: a transport failure is not
// an APIError, and must still produce a reason rather than an empty
// body.
func TestRegister_NonAPIErrorStillReported(t *testing.T) {
	resp, msg := registerWith(t, errors.New("dial tcp: lookup api.github.com: no such host"))
	if resp.StatusCode != http.StatusFailedDependency {
		t.Fatalf("status: got %d, want 424", resp.StatusCode)
	}
	if !strings.Contains(msg, "no such host") {
		t.Fatalf("message: got %q", msg)
	}
}

// TestRegister_FailureIsAudited: the instance shuts down seconds
// later and its log goes with it, so the reason has to be in the
// database independently of whatever the runner manages to report.
func TestRegister_FailureIsAudited(t *testing.T) {
	h := newHarness(t)
	seedProjectAndPool(t, h.rt)
	tok := seedClaimedJob(t, h.rt, "j-audit", "i-audit")

	handler := &Handler{Runtime: h.rt, HMACKey: hmacKey, GHApp: stubMinter{err: &ghapp.APIError{
		Op: "jitconfig", StatusCode: http.StatusUnauthorized, Status: "401 Unauthorized",
		Body: `{"message":"Bad credentials"}`,
	}}}
	h.app.Post("/api/runner/register-audit", handler.Register)
	h.postJSON(t, "/api/runner/register-audit", map[string]any{
		"job_id": "j-audit", "instance_id": "i-audit",
		"instance_type": "t3.large", "az": "eu-west-1a", "callback_token": tok,
	})

	entries, err := h.rt.Store.Audit.List(t.Context(), auditmodel.ListFilter{
		Action: auditmodel.ActionRunnerRegisterFailed, Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("audit rows: got %d, want 1", len(entries))
	}
	if !strings.Contains(entries[0].Detail, "Bad credentials") {
		t.Fatalf("audit detail must carry the reason, got %q", entries[0].Detail)
	}
	if !strings.Contains(entries[0].Detail, `"temporary":false`) {
		t.Fatalf("audit detail must record retryability, got %q", entries[0].Detail)
	}
	if entries[0].TargetID != "j-audit" {
		t.Fatalf("audit target: got %q", entries[0].TargetID)
	}
}

// TestError_RunStageAttachesLog covers the hole that swallowed the
// most common failure of all: ./run.sh exiting non-zero never tripped
// the ERR trap, so a runner GitHub rejected at connect time (a
// version it no longer accepts, a consumed JIT config) left no trace
// anywhere.
//
// The log is kept; the VERDICT is not ours to write while GitHub is
// still tracking the job - see
// TestError_RunReportDoesNotOverrideWebhookOutcome.
func TestError_RunStageAttachesLog(t *testing.T) {
	h := newHarness(t)
	seedProjectAndPool(t, h.rt)
	tok := seedClaimedJob(t, h.rt, "j-run", "i-run")
	if err := h.rt.Store.Job.MarkRunning(t.Context(), "j-run", "i-run", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	resp := h.postJSON(t, "/api/runner/error", map[string]any{
		"job_id": "j-run", "callback_token": tok, "stage": "run",
		"exit_code": 1, "line": 0,
		"log": "runner listener exited with error: The runner version is no longer supported",
	})
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d", resp.StatusCode)
	}

	j, err := h.rt.Store.Job.Get(t.Context(), "j-run")
	if err != nil {
		t.Fatal(err)
	}
	if j.FailureStage != "run" {
		t.Fatalf("stage: got %q, want run", j.FailureStage)
	}
	if !strings.Contains(j.FailureLog, "no longer supported") {
		t.Fatalf("log not stored: %q", j.FailureLog)
	}
	// The old message said "bootstrap exit=1 line=0" for this, which
	// named the wrong phase and a line number that means nothing.
	if strings.Contains(j.FailureMessage, "line") {
		t.Fatalf("run failures carry no line number, got %q", j.FailureMessage)
	}
	if !strings.Contains(j.FailureMessage, "actions-runner exited 1") {
		t.Fatalf("message: got %q", j.FailureMessage)
	}
}

func TestFailureMessage(t *testing.T) {
	cases := []struct {
		stage string
		exit  int
		line  int
		want  string
	}{
		{"run", 1, 0, "actions-runner exited 1"},
		{"run", 2, 42, "actions-runner exited 2"},
		{"register", 22, 142, "script exit=22 at line 142"},
		{"bootstrap", 13, 0, "script exit=13"},
	}
	for _, c := range cases {
		if got := failureMessage(c.stage, c.exit, c.line); got != c.want {
			t.Errorf("failureMessage(%q, %d, %d) = %q, want %q", c.stage, c.exit, c.line, got, c.want)
		}
	}
}

// TestError_RunReportDoesNotOverrideWebhookOutcome: run.sh exiting
// non-zero does not mean the workflow failed, and recording 'failed'
// would be permanent - MarkCompleted refuses to leave a terminal
// state, so the webhook carrying the real conclusion would be
// dropped and a green job would read as failed forever.
func TestError_RunReportDoesNotOverrideWebhookOutcome(t *testing.T) {
	h := newHarness(t)
	seedProjectAndPool(t, h.rt)
	tok := seedClaimedJob(t, h.rt, "j-race", "i-race")
	if err := h.rt.Store.Job.MarkRunning(t.Context(), "j-race", "i-race", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	resp := h.postJSON(t, "/api/runner/error", map[string]any{
		"job_id": "j-race", "callback_token": tok, "stage": "run",
		"exit_code": 1, "log": "runner listener exited with error code 1",
	})
	if resp.StatusCode >= 400 {
		t.Fatalf("error report rejected: %d", resp.StatusCode)
	}

	// The diagnosis is kept even though the verdict is not ours.
	j, err := h.rt.Store.Job.Get(t.Context(), "j-race")
	if err != nil {
		t.Fatal(err)
	}
	if j.Status != jobmodel.StatusRunning {
		t.Fatalf("status must stay with the webhook, got %q", j.Status)
	}
	if !strings.Contains(j.FailureLog, "error code 1") {
		t.Fatalf("log must still be attached, got %q", j.FailureLog)
	}
	if j.FailureStage != "run" {
		t.Fatalf("stage: got %q", j.FailureStage)
	}

	// GitHub then reports the truth, and it must land.
	if err := h.rt.Store.Job.MarkCompleted(t.Context(), "j-race", time.Now().UTC()); err != nil {
		t.Fatalf("webhook could not finalize: %v", err)
	}
	j, _ = h.rt.Store.Job.Get(t.Context(), "j-race")
	if j.Status != jobmodel.StatusCompleted {
		t.Fatalf("final status: got %q, want completed", j.Status)
	}
	// And the log survives the transition, which is the whole point
	// of attaching it separately.
	if !strings.Contains(j.FailureLog, "error code 1") {
		t.Fatalf("log lost on completion: %q", j.FailureLog)
	}
}

// A job that never reached `running` has no webhook coming, so this
// report IS the outcome.
func TestError_PreRunningStillMarksFailed(t *testing.T) {
	h := newHarness(t)
	seedProjectAndPool(t, h.rt)
	tok := seedClaimedJob(t, h.rt, "j-early", "i-early")

	h.postJSON(t, "/api/runner/error", map[string]any{
		"job_id": "j-early", "callback_token": tok, "stage": "register",
		"exit_code": 1, "line": 218, "log": "HTTP 424",
	})

	j, err := h.rt.Store.Job.Get(t.Context(), "j-early")
	if err != nil {
		t.Fatal(err)
	}
	if j.Status != jobmodel.StatusFailed {
		t.Fatalf("status: got %q, want failed", j.Status)
	}
}
