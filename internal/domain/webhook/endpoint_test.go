// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package webhook

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/yousysadmin/pacer/internal/core/env"
	auditmodel "github.com/yousysadmin/pacer/internal/models/audit"
	instancemodel "github.com/yousysadmin/pacer/internal/models/instance"
	jobmodel "github.com/yousysadmin/pacer/internal/models/job"
	poolmodel "github.com/yousysadmin/pacer/internal/models/pool"
	projectmodel "github.com/yousysadmin/pacer/internal/models/project"
	repomodel "github.com/yousysadmin/pacer/internal/models/repo"
	"github.com/yousysadmin/pacer/internal/testutil/runtimeutil"
)

const testSecret = "test-webhook-secret"

type harness struct {
	app     *fiber.App
	rt      *env.Runtime
	handler *Handler
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	rt := runtimeutil.NewRuntime(t, &env.Config{
		GitHub: env.GitHubConfig{WebhookSecret: testSecret},
	})
	h := &Handler{Runtime: rt}
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Post("/api/webhook", h.Receive)
	return &harness{app: app, rt: rt, handler: h}
}

func (h *harness) post(t *testing.T, body []byte, headers map[string]string) *http.Response {
	t.Helper()
	req := httptest.NewRequest("POST", "/api/webhook", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Hub-Signature-256", sign(body, testSecret))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := h.app.Test(req, -1)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	return resp
}

func sign(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func bodyOf(t *testing.T, r *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}

func TestWebhook_RejectsBadSignature(t *testing.T) {
	h := newHarness(t)
	body := []byte(`{"action":"queued"}`)
	req := httptest.NewRequest("POST", "/api/webhook", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", "sha256=deadbeef")
	req.Header.Set("X-GitHub-Event", "ping")
	resp, err := h.app.Test(req, -1)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	if resp.StatusCode != 401 {
		t.Fatalf("status: want 401, got %d (%s)", resp.StatusCode, bodyOf(t, resp))
	}
}

func TestWebhook_PingAcked(t *testing.T) {
	h := newHarness(t)
	body := []byte(`{"zen":"hi"}`)
	resp := h.post(t, body, map[string]string{
		"X-GitHub-Event":    "ping",
		"X-GitHub-Delivery": "abc-123",
	})
	if resp.StatusCode != 200 {
		t.Fatalf("status: want 200, got %d", resp.StatusCode)
	}
	if !strings.Contains(bodyOf(t, resp), `"pong":true`) {
		t.Fatal("response should contain pong:true")
	}
}

func TestWebhook_EnqueuesQueuedJob(t *testing.T) {
	h := newHarness(t)
	seedRepoBoundProject(t, h.rt, "p-1", "demo", "octocat/hello-world", "po-1", "default", true, []string{})

	body := workflowJobQueued("octocat/hello-world", 12345, []string{"self-hosted", "demo"})
	resp := h.post(t, body, map[string]string{
		"X-GitHub-Event":    "workflow_job",
		"X-GitHub-Delivery": "del-1",
	})
	if resp.StatusCode != 200 {
		t.Fatalf("status: want 200, got %d (%s)", resp.StatusCode, bodyOf(t, resp))
	}

	jobs, err := h.rt.Store.Job.List(t.Context(), jobmodel.ListFilter{ProjectID: "p-1"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("want 1 job enqueued, got %d", len(jobs))
	}
	if jobs[0].Status != jobmodel.StatusQueued {
		t.Fatalf("status: want queued, got %q", jobs[0].Status)
	}
	if jobs[0].PoolID != "po-1" {
		t.Fatalf("pool_id: want po-1, got %q", jobs[0].PoolID)
	}

	entries, _ := h.rt.Store.Audit.List(t.Context(), auditmodel.ListFilter{Action: auditmodel.ActionJobEnqueued})
	if len(entries) != 1 {
		t.Fatalf("want 1 enqueue audit entry, got %d", len(entries))
	}
}

func TestWebhook_NoPoolMatch_AuditedAndDropped(t *testing.T) {
	h := newHarness(t)
	seedRepoBoundProject(t, h.rt, "p-1", "demo", "octocat/hello-world", "po-1", "default", true, nil)

	// `gpu` not advertised by any pool
	body := workflowJobQueued("octocat/hello-world", 99, []string{"self-hosted", "demo", "gpu"})
	resp := h.post(t, body, map[string]string{
		"X-GitHub-Event":    "workflow_job",
		"X-GitHub-Delivery": "del-no-match",
	})
	if resp.StatusCode != 200 {
		t.Fatalf("status: want 200, got %d", resp.StatusCode)
	}
	if !strings.Contains(bodyOf(t, resp), "no pool matches") {
		t.Fatal("response should explain no-pool-match drop")
	}

	jobs, _ := h.rt.Store.Job.List(t.Context(), jobmodel.ListFilter{})
	if len(jobs) != 0 {
		t.Fatalf("expected no jobs enqueued, got %d", len(jobs))
	}

	entries, _ := h.rt.Store.Audit.List(t.Context(), auditmodel.ListFilter{Action: auditmodel.ActionJobNoPoolMatch})
	if len(entries) != 1 {
		t.Fatalf("want 1 no-pool-match audit, got %d", len(entries))
	}
}

func TestWebhook_UnboundRepo_NoOrgFallback_Dropped(t *testing.T) {
	h := newHarness(t)
	body := workflowJobQueued("stranger/repo", 1, []string{"self-hosted"})
	resp := h.post(t, body, map[string]string{
		"X-GitHub-Event":    "workflow_job",
		"X-GitHub-Delivery": "del-stranger",
	})
	if resp.StatusCode != 200 {
		t.Fatalf("status: want 200, got %d", resp.StatusCode)
	}
	jobs, _ := h.rt.Store.Job.List(t.Context(), jobmodel.ListFilter{})
	if len(jobs) != 0 {
		t.Fatalf("unbound repo should not enqueue, got %d jobs", len(jobs))
	}
}

func TestWebhook_OrgScopeFallback(t *testing.T) {
	h := newHarness(t)
	// No repo binding. Project is org-scoped on owner.login.
	seedOrgScopedProject(t, h.rt, "p-org", "octo-runners", "octocat", "po-1", "default")

	body := workflowJobQueued("octocat/any-repo", 1, []string{"self-hosted", "octo-runners"})
	resp := h.post(t, body, map[string]string{
		"X-GitHub-Event":    "workflow_job",
		"X-GitHub-Delivery": "del-org",
	})
	if resp.StatusCode != 200 {
		t.Fatalf("status: want 200, got %d (%s)", resp.StatusCode, bodyOf(t, resp))
	}
	jobs, _ := h.rt.Store.Job.List(t.Context(), jobmodel.ListFilter{})
	if len(jobs) != 1 {
		t.Fatalf("want 1 enqueued via org fallback, got %d", len(jobs))
	}
	if jobs[0].ProjectID != "p-org" {
		t.Fatalf("project_id: want p-org, got %q", jobs[0].ProjectID)
	}
}

func TestWebhook_DuplicateDeliveryDeduped(t *testing.T) {
	h := newHarness(t)
	seedRepoBoundProject(t, h.rt, "p-1", "demo", "octocat/hello-world", "po-1", "default", true, nil)

	body := workflowJobQueued("octocat/hello-world", 7777, []string{"self-hosted", "demo"})
	headers := map[string]string{
		"X-GitHub-Event":    "workflow_job",
		"X-GitHub-Delivery": "del-dup",
	}
	if resp := h.post(t, body, headers); resp.StatusCode != 200 {
		t.Fatalf("first post: %d", resp.StatusCode)
	}
	resp := h.post(t, body, headers)
	if resp.StatusCode != 200 {
		t.Fatalf("second post: want 200, got %d", resp.StatusCode)
	}
	if !strings.Contains(bodyOf(t, resp), "duplicate") {
		t.Fatal("second delivery should report duplicate")
	}
	jobs, _ := h.rt.Store.Job.List(t.Context(), jobmodel.ListFilter{})
	if len(jobs) != 1 {
		t.Fatalf("dedup failed: %d jobs (want 1)", len(jobs))
	}
}

func TestWebhook_ProjectDisabled_Dropped(t *testing.T) {
	h := newHarness(t)
	seedRepoBoundProject(t, h.rt, "p-1", "demo", "octocat/hello-world", "po-1", "default", true, nil)
	// Disable the project.
	if _, err := h.rt.DB.DB().ExecContext(t.Context(),
		`UPDATE projects SET disabled=1 WHERE id=?`, "p-1"); err != nil {
		t.Fatalf("disable: %v", err)
	}

	body := workflowJobQueued("octocat/hello-world", 1, []string{"self-hosted", "demo"})
	resp := h.post(t, body, map[string]string{
		"X-GitHub-Event":    "workflow_job",
		"X-GitHub-Delivery": "del-disabled",
	})
	if resp.StatusCode != 200 {
		t.Fatalf("status: want 200, got %d", resp.StatusCode)
	}
	if !strings.Contains(bodyOf(t, resp), "project disabled") {
		t.Fatal("response should mention project disabled")
	}
	jobs, _ := h.rt.Store.Job.List(t.Context(), jobmodel.ListFilter{})
	if len(jobs) != 0 {
		t.Fatalf("disabled project should not enqueue, got %d", len(jobs))
	}
}

func TestWebhook_InProgress_MarksQueuedJobRunning(t *testing.T) {
	h := newHarness(t)
	seedRepoBoundProject(t, h.rt, "p-1", "demo", "octocat/hello-world", "po-1", "default", true, nil)

	queued := workflowJobQueued("octocat/hello-world", 4242, []string{"self-hosted", "demo"})
	if resp := h.post(t, queued, map[string]string{
		"X-GitHub-Event":    "workflow_job",
		"X-GitHub-Delivery": "del-q",
	}); resp.StatusCode != 200 {
		t.Fatalf("enqueue: %d", resp.StatusCode)
	}

	inProgress := workflowJobAction("in_progress", "octocat/hello-world", 4242, "")
	resp := h.post(t, inProgress, map[string]string{
		"X-GitHub-Event":    "workflow_job",
		"X-GitHub-Delivery": "del-ip",
	})
	if resp.StatusCode != 204 {
		t.Fatalf("in_progress: want 204, got %d", resp.StatusCode)
	}
	j, err := h.rt.Store.Job.GetByGHJobID(t.Context(), 4242)
	if err != nil || j == nil {
		t.Fatalf("GetByGHJobID: %v (job=%v)", err, j)
	}
	if j.Status != jobmodel.StatusRunning {
		t.Fatalf("status: want running, got %q", j.Status)
	}
}

func TestWebhook_LateInProgress_DoesNotResurrectTerminalJob(t *testing.T) {
	h := newHarness(t)
	seedRepoBoundProject(t, h.rt, "p-1", "demo", "octocat/hello-world", "po-1", "default", true, nil)
	ctx := t.Context()

	for _, tc := range []struct {
		name string
		mark func(id string) error
		want jobmodel.Status
	}{
		{"failed", func(id string) error {
			return h.rt.Store.Job.MarkFailed(ctx, id, "spawn", "capacity exhausted", time.Now().UTC())
		}, jobmodel.StatusFailed},
		{"cancelled", func(id string) error {
			return h.rt.Store.Job.MarkCancelled(ctx, id, "github", "conclusion=cancelled", time.Now().UTC())
		}, jobmodel.StatusCancelled},
		{"reaped", func(id string) error {
			return h.rt.Store.Job.MarkReaped(ctx, id, time.Now().UTC())
		}, jobmodel.StatusReaped},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ghID := int64(9000)
			switch tc.name {
			case "cancelled":
				ghID = 9001
			case "reaped":
				ghID = 9002
			}
			queued := workflowJobQueued("octocat/hello-world", ghID, []string{"self-hosted", "demo"})
			if resp := h.post(t, queued, map[string]string{
				"X-GitHub-Event":    "workflow_job",
				"X-GitHub-Delivery": "del-q-" + tc.name,
			}); resp.StatusCode != 200 {
				t.Fatalf("enqueue: %d", resp.StatusCode)
			}
			j, _ := h.rt.Store.Job.GetByGHJobID(ctx, ghID)
			if j == nil {
				t.Fatal("job not enqueued")
			}
			if err := tc.mark(j.ID); err != nil {
				t.Fatalf("mark %s: %v", tc.name, err)
			}

			late := workflowJobAction("in_progress", "octocat/hello-world", ghID, "")
			resp := h.post(t, late, map[string]string{
				"X-GitHub-Event":    "workflow_job",
				"X-GitHub-Delivery": "del-late-" + tc.name,
			})
			if resp.StatusCode != 204 {
				t.Fatalf("late in_progress: want 204, got %d", resp.StatusCode)
			}
			got, _ := h.rt.Store.Job.GetByGHJobID(ctx, ghID)
			if got.Status != tc.want {
				t.Fatalf("late in_progress resurrected job: want %q, got %q", tc.want, got.Status)
			}
		})
	}
}

func TestWebhook_ManualRedeliver_FreshGUID_DroppedAsDuplicate(t *testing.T) {
	h := newHarness(t)
	seedRepoBoundProject(t, h.rt, "p-1", "demo", "octocat/hello-world", "po-1", "default", true, nil)

	body := workflowJobQueued("octocat/hello-world", 8888, []string{"self-hosted", "demo"})
	if resp := h.post(t, body, map[string]string{
		"X-GitHub-Event":    "workflow_job",
		"X-GitHub-Delivery": "del-original",
	}); resp.StatusCode != 200 {
		t.Fatalf("first post: %d", resp.StatusCode)
	}
	// Manual "Redeliver" from the GitHub App UI mints a fresh delivery
	// GUID, so delivery-id dedup does not catch it.
	resp := h.post(t, body, map[string]string{
		"X-GitHub-Event":    "workflow_job",
		"X-GitHub-Delivery": "del-redelivered",
	})
	if resp.StatusCode != 200 {
		t.Fatalf("redeliver: want 200 logical drop, got %d (%s)", resp.StatusCode, bodyOf(t, resp))
	}
	if !strings.Contains(bodyOf(t, resp), "duplicate") {
		t.Fatal("redeliver should report duplicate")
	}
	jobs, _ := h.rt.Store.Job.List(t.Context(), jobmodel.ListFilter{})
	if len(jobs) != 1 {
		t.Fatalf("redeliver double-enqueued: %d jobs (want 1)", len(jobs))
	}
}

// --- helpers ---

// workflowJobAction builds a workflow_job payload for non-queued
// actions (in_progress, completed).
func workflowJobAction(action, repoFullName string, ghJobID int64, conclusion string) []byte {
	owner, _, _ := strings.Cut(repoFullName, "/")
	payload := map[string]any{
		"action": action,
		"workflow_job": map[string]any{
			"id":         ghJobID,
			"run_id":     ghJobID + 1,
			"labels":     []string{"self-hosted", "demo"},
			"name":       "build",
			"status":     action,
			"conclusion": conclusion,
		},
		"repository": map[string]any{
			"full_name": repoFullName,
			"owner": map[string]any{
				"login": owner,
				"type":  "User",
			},
		},
		"installation": map[string]any{"id": 12345},
		"sender":       map[string]any{"login": "octocat"},
	}
	b, _ := json.Marshal(payload)
	return b
}

func workflowJobQueued(repoFullName string, ghJobID int64, labels []string) []byte {
	owner, _, _ := strings.Cut(repoFullName, "/")
	payload := map[string]any{
		"action": "queued",
		"workflow_job": map[string]any{
			"id":     ghJobID,
			"run_id": ghJobID + 1,
			"labels": labels,
			"name":   "build",
			"status": "queued",
		},
		"repository": map[string]any{
			"full_name": repoFullName,
			"owner": map[string]any{
				"login": owner,
				"type":  "User",
			},
		},
		"installation": map[string]any{"id": 12345},
		"sender":       map[string]any{"login": "octocat"},
	}
	b, _ := json.Marshal(payload)
	return b
}

func seedRepoBoundProject(t *testing.T, rt *env.Runtime, projID, projName, repoFullName, poolID, poolName string, isDefault bool, extraLabels []string) {
	t.Helper()
	ctx := t.Context()
	if err := rt.Store.Project.Put(ctx, &projectmodel.Project{
		ID: projID, Name: projName, Scope: projectmodel.ScopeRepo,
	}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	if err := rt.Store.Pool.Put(ctx, &poolmodel.Pool{
		ID: poolID, ProjectID: projID, Name: poolName, IsDefault: isDefault, Priority: 100,
		AMIID: "ami-test", InstanceTypes: []string{"t3.large"},
		SubnetIDs: []string{"subnet-1"}, SecurityGroupIDs: []string{"sg-1"},
		MaxConcurrentRunners: 5, MaxRuntimeMinutes: 60, RootVolumeGB: 30, Spot: true,
		ExtraLabels: extraLabels,
	}); err != nil {
		t.Fatalf("seed pool: %v", err)
	}
	if err := rt.Store.Repo.Put(ctx, &repomodel.Repo{
		FullName: repoFullName, ProjectID: projID,
	}); err != nil {
		t.Fatalf("seed repo: %v", err)
	}
}

func seedOrgScopedProject(t *testing.T, rt *env.Runtime, projID, projName, orgLogin, poolID, poolName string) {
	t.Helper()
	ctx := t.Context()
	if err := rt.Store.Project.Put(ctx, &projectmodel.Project{
		ID: projID, Name: projName, Scope: projectmodel.ScopeOrg, OrgName: orgLogin,
	}); err != nil {
		t.Fatalf("seed org project: %v", err)
	}
	if err := rt.Store.Pool.Put(ctx, &poolmodel.Pool{
		ID: poolID, ProjectID: projID, Name: poolName, IsDefault: true, Priority: 100,
		AMIID: "ami-test", InstanceTypes: []string{"t3.large"},
		SubnetIDs: []string{"subnet-1"}, SecurityGroupIDs: []string{"sg-1"},
		MaxConcurrentRunners: 5, MaxRuntimeMinutes: 60, RootVolumeGB: 30, Spot: true,
	}); err != nil {
		t.Fatalf("seed pool: %v", err)
	}
}

func TestWebhook_LateCompleted_DoesNotOverwriteReapedJob(t *testing.T) {
	h := newHarness(t)
	seedRepoBoundProject(t, h.rt, "p-1", "demo", "octocat/hello-world", "po-1", "default", true, nil)
	ctx := t.Context()
	ghID := int64(9100)

	queued := workflowJobQueued("octocat/hello-world", ghID, []string{"self-hosted", "demo"})
	if resp := h.post(t, queued, map[string]string{
		"X-GitHub-Event": "workflow_job", "X-GitHub-Delivery": "del-q-late-completed",
	}); resp.StatusCode != 200 {
		t.Fatalf("enqueue: %d", resp.StatusCode)
	}
	j, _ := h.rt.Store.Job.GetByGHJobID(ctx, ghID)
	if err := h.rt.Store.Job.MarkReaped(ctx, j.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	for _, conclusion := range []string{"success", "failure", "cancelled"} {
		late := workflowJobAction("completed", "octocat/hello-world", ghID, conclusion)
		resp := h.post(t, late, map[string]string{
			"X-GitHub-Event": "workflow_job", "X-GitHub-Delivery": "del-late-completed-" + conclusion,
		})
		if resp.StatusCode != 204 {
			t.Fatalf("late completed(%s): want 204, got %d", conclusion, resp.StatusCode)
		}
		got, _ := h.rt.Store.Job.GetByGHJobID(ctx, ghID)
		if got.Status != jobmodel.StatusReaped {
			t.Fatalf("late completed(%s) overwrote reaped: %q", conclusion, got.Status)
		}
	}
}

// workflowJobOnRunner is workflowJobAction with the runner identity
// GitHub populates from in_progress onwards.
func workflowJobOnRunner(action, repoFullName string, ghJobID int64, conclusion, runnerName string) []byte {
	var payload map[string]any
	_ = json.Unmarshal(workflowJobAction(action, repoFullName, ghJobID, conclusion), &payload)
	wj := payload["workflow_job"].(map[string]any)
	wj["runner_id"] = 77
	wj["runner_name"] = runnerName
	b, _ := json.Marshal(payload)
	return b
}

// seedRunningJob puts a job on an instance the way the orchestrator
// would: both sides pointing at the machine spawned for it.
func seedRunningJob(t *testing.T, h *harness, jobID, instID string, ghJobID int64) {
	t.Helper()
	ctx := t.Context()
	if err := h.rt.Store.Job.Put(ctx, &jobmodel.Job{
		ID: jobID, GHJobID: ghJobID, GHRunID: ghJobID + 1, InstallationID: 12345,
		RepoFullName: "octocat/hello-world", ProjectID: "p-1", PoolID: "po-1",
		Status: jobmodel.StatusQueued, QueuedAt: time.Now().UTC(), Payload: []byte("{}"),
	}); err != nil {
		t.Fatalf("Job.Put %s: %v", jobID, err)
	}
	if err := h.rt.Store.Job.StampSpawn(ctx, jobID, instID, "hash-"+jobID, "tok-"+jobID); err != nil {
		t.Fatalf("StampSpawn %s: %v", jobID, err)
	}
	if err := h.rt.Store.Instance.Put(ctx, &instancemodel.Instance{
		ID: instID, JobID: jobID, ProjectID: "p-1", PoolID: "po-1",
		State: instancemodel.StateRunning, LaunchedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("Instance.Put %s: %v", instID, err)
	}
}

// Two runners in one pool carry identical labels, so GitHub is free to
// hand either job to either machine. When it crosses them, the job has
// to follow the runner that actually took it - the reaper decides what
// to fail and terminate from that binding.
func TestWebhook_InProgress_RebindsJobToTheRunnerThatTookIt(t *testing.T) {
	h := newHarness(t)
	seedRepoBoundProject(t, h.rt, "p-1", "demo", "octocat/hello-world", "po-1", "default", true, nil)
	ctx := t.Context()

	seedRunningJob(t, h, "job-A", "i-aaa", 1001)
	seedRunningJob(t, h, "job-B", "i-bbb", 1002)

	// GitHub gives job-A to the runner on i-bbb.
	resp := h.post(t, workflowJobOnRunner("in_progress", "octocat/hello-world", 1001, "", "ghr-i-bbb"),
		map[string]string{"X-GitHub-Event": "workflow_job", "X-GitHub-Delivery": "del-1"})
	if resp.StatusCode != 204 {
		t.Fatalf("in_progress: want 204, got %d", resp.StatusCode)
	}

	a, err := h.rt.Store.Job.Get(ctx, "job-A")
	if err != nil || a == nil {
		t.Fatalf("Get job-A: %v", err)
	}
	if a.RunnerInstanceID != "i-bbb" {
		t.Fatalf("job-A must record the runner that took it: want i-bbb, got %q", a.RunnerInstanceID)
	}
	// The launch pairing is untouched: it is how the machine's own
	// callbacks find their row, and what the job's cost is billed on.
	if a.InstanceID != "i-aaa" {
		t.Fatalf("job-A launch pairing must stay i-aaa, got %q", a.InstanceID)
	}
	if a.Status != jobmodel.StatusRunning {
		t.Fatalf("job-A status: want running, got %q", a.Status)
	}

	inst, err := h.rt.Store.Instance.Get(ctx, "i-bbb")
	if err != nil || inst == nil {
		t.Fatalf("Get i-bbb: %v", err)
	}
	if inst.JobID != "job-B" {
		t.Fatalf("instances.job_id must stay the launch pairing: want job-B, got %q", inst.JobID)
	}

	// And the query the reaper runs now names the right job.
	onB, err := h.rt.Store.Job.GetByInstanceID(ctx, "i-bbb")
	if err != nil || onB == nil {
		t.Fatalf("GetByInstanceID i-bbb: %v (job=%v)", err, onB)
	}
	if onB.ID != "job-A" {
		t.Fatalf("job on i-bbb: want job-A, got %q", onB.ID)
	}
	onA, err := h.rt.Store.Job.GetByInstanceID(ctx, "i-aaa")
	if err != nil {
		t.Fatalf("GetByInstanceID i-aaa: %v", err)
	}
	if onA != nil {
		t.Fatalf("nothing runs on i-aaa yet, got %q", onA.ID)
	}
}

func TestWebhook_RebindIsAuditedAndSkippedWhenAlreadyCorrect(t *testing.T) {
	h := newHarness(t)
	seedRepoBoundProject(t, h.rt, "p-1", "demo", "octocat/hello-world", "po-1", "default", true, nil)
	ctx := t.Context()
	seedRunningJob(t, h, "job-A", "i-aaa", 1001)

	// Runner matches the spawn pairing: nothing to correct, nothing to
	// audit.
	h.post(t, workflowJobOnRunner("in_progress", "octocat/hello-world", 1001, "", "ghr-i-aaa"),
		map[string]string{"X-GitHub-Event": "workflow_job", "X-GitHub-Delivery": "del-same"})
	if n := countAudit(t, h, auditmodel.ActionJobRunnerRebound); n != 0 {
		t.Fatalf("matching runner must not audit a rebind, got %d", n)
	}

	seedRunningJob(t, h, "job-B", "i-bbb", 1002)
	h.post(t, workflowJobOnRunner("in_progress", "octocat/hello-world", 1002, "", "ghr-i-aaa"),
		map[string]string{"X-GitHub-Event": "workflow_job", "X-GitHub-Delivery": "del-cross"})
	if n := countAudit(t, h, auditmodel.ActionJobRunnerRebound); n != 1 {
		t.Fatalf("crossed runner must audit a rebind, got %d", n)
	}
	b, _ := h.rt.Store.Job.Get(ctx, "job-B")
	if b.RunnerInstanceID != "i-aaa" {
		t.Fatalf("job-B ran on: want i-aaa, got %q", b.RunnerInstanceID)
	}
}

// A runner pacer did not spawn cannot be resolved to an instance. The
// binding is left alone rather than pointed at nothing.
func TestWebhook_ForeignRunnerLeavesBindingAlone(t *testing.T) {
	h := newHarness(t)
	seedRepoBoundProject(t, h.rt, "p-1", "demo", "octocat/hello-world", "po-1", "default", true, nil)
	seedRunningJob(t, h, "job-A", "i-aaa", 1001)

	for _, name := range []string{"", "some-corp-runner-7"} {
		h.post(t, workflowJobOnRunner("in_progress", "octocat/hello-world", 1001, "", name),
			map[string]string{"X-GitHub-Event": "workflow_job", "X-GitHub-Delivery": "del-" + name})
		j, _ := h.rt.Store.Job.Get(t.Context(), "job-A")
		if j.RunnerInstanceID != "" {
			t.Fatalf("runner %q: nothing should be recorded, got %q", name, j.RunnerInstanceID)
		}
	}
	if n := countAudit(t, h, auditmodel.ActionJobRunnerRebound); n != 0 {
		t.Fatalf("foreign runner must not audit a rebind, got %d", n)
	}
}

func countAudit(t *testing.T, h *harness, action string) int {
	t.Helper()
	n, err := h.rt.Store.Audit.Count(t.Context(), auditmodel.ListFilter{Action: action})
	if err != nil {
		t.Fatalf("Audit.Count: %v", err)
	}
	return n
}
