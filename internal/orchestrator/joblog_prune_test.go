// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package orchestrator_test

import (
	"testing"
	"time"

	"github.com/yousysadmin/pacer/internal/core/env"
	jobmodel "github.com/yousysadmin/pacer/internal/models/job"
	poolmodel "github.com/yousysadmin/pacer/internal/models/pool"
	projectmodel "github.com/yousysadmin/pacer/internal/models/project"
	statsmodel "github.com/yousysadmin/pacer/internal/models/stats"
	"github.com/yousysadmin/pacer/internal/orchestrator"
)

// seedFailedJob lands one failed job with a captured log, completed
// `age` ago.
func seedFailedJob(t *testing.T, rt *env.Runtime, id string, age time.Duration, log string) time.Time {
	t.Helper()
	completed := time.Now().UTC().Add(-age)
	cost := 0.25
	if err := rt.Store.Job.Put(t.Context(), &jobmodel.Job{
		ID: id, GHJobID: time.Now().UnixNano() + int64(len(id)), GHRunID: 1, InstallationID: 1,
		RepoFullName: "acme/api", ProjectID: "p-1", PoolID: "po-1",
		Status: jobmodel.StatusFailed, CompletedAt: &completed,
		FailureStage: "run", FailureMessage: "actions-runner exited 2", FailureLog: log,
		Payload: []byte("{}"),
	}); err != nil {
		t.Fatal(err)
	}
	// Put writes the job's identity and lifecycle, not its outcome:
	// failure_log and estimated_cost_usd belong to MarkFailedWithLog
	// and the cost finalizer. Stamp them directly so the row looks
	// like one that failed `age` ago, which MarkFailedWithLog cannot
	// produce (it always stamps now).
	if _, err := rt.DB.DB().ExecContext(t.Context(),
		`UPDATE jobs SET estimated_cost_usd = ?, failure_log = ? WHERE id = ?`,
		cost, log, id); err != nil {
		t.Fatal(err)
	}
	return completed
}

func seedProjectPool(t *testing.T, rt *env.Runtime) {
	t.Helper()
	if err := rt.Store.Project.Put(t.Context(), &projectmodel.Project{
		ID: "p-1", Name: "demo", Scope: projectmodel.ScopeRepo,
	}); err != nil {
		t.Fatal(err)
	}
	if err := rt.Store.Pool.Put(t.Context(), &poolmodel.Pool{
		ID: "po-1", ProjectID: "p-1", Name: "default", IsDefault: true, Priority: 100,
		AMIID: "ami-1", InstanceTypes: []string{"t3.large"}, SubnetIDs: []string{"subnet-1"},
		SecurityGroupIDs: []string{"sg-1"}, MaxConcurrentRunners: 5, MaxRuntimeMinutes: 60,
	}); err != nil {
		t.Fatal(err)
	}
}

// Reuses newPrunerRT from pruner_test.go, which wires the Webhook
// store runtimeutil deliberately omits (import cycle - see the note
// in runtimeutil/runtime.go). Tick touches every housekeeping table,
// so a partial Runtime panics rather than skipping.
func newPrunerRuntime(t *testing.T, jobLogDays int) *env.Runtime {
	t.Helper()
	return newPrunerRT(t, &env.Config{
		Retention: env.RetentionConfig{AuditDays: 90, WebhookDays: 7, JobLogDays: jobLogDays},
	})
}

// TestPruner_ClearsOldJobLogsKeepsRecent is the basic contract: past
// the window the log goes, inside it the log stays.
func TestPruner_ClearsOldJobLogsKeepsRecent(t *testing.T) {
	rt := newPrunerRuntime(t, 31)
	seedProjectPool(t, rt)
	seedFailedJob(t, rt, "j-old", 40*24*time.Hour, "ancient bootstrap output")
	seedFailedJob(t, rt, "j-new", 3*24*time.Hour, "recent bootstrap output")

	orchestrator.NewPruner(rt).Tick(t.Context())

	old, err := rt.Store.Job.Get(t.Context(), "j-old")
	if err != nil {
		t.Fatal(err)
	}
	if old.FailureLog != "" {
		t.Errorf("log past the window should be cleared, got %q", old.FailureLog)
	}
	// The reason the job failed is a sentence, not a log - it costs
	// nothing to keep and is what the jobs list renders.
	if old.FailureMessage == "" || old.FailureStage == "" {
		t.Errorf("stage/message must survive the sweep: %q / %q", old.FailureStage, old.FailureMessage)
	}

	recent, err := rt.Store.Job.Get(t.Context(), "j-new")
	if err != nil {
		t.Fatal(err)
	}
	if recent.FailureLog != "recent bootstrap output" {
		t.Errorf("log inside the window must survive, got %q", recent.FailureLog)
	}
}

// TestPruner_JobLogSweepPreservesStats is why this clears logs
// instead of deleting rows: cost and runtime rollups read the jobs
// table, so removing rows would silently shorten every historical
// report to the retention window.
func TestPruner_JobLogSweepPreservesStats(t *testing.T) {
	rt := newPrunerRuntime(t, 31)
	seedProjectPool(t, rt)
	seedFailedJob(t, rt, "j-old", 40*24*time.Hour, "ancient output")

	from := time.Now().UTC().Add(-90 * 24 * time.Hour)
	to := time.Now().UTC().Add(time.Hour)

	before, _, err := rt.Store.Stats.Rollup(t.Context(), statsmodel.ByProject, from, to, "")
	if err != nil {
		t.Fatal(err)
	}
	orchestrator.NewPruner(rt).Tick(t.Context())
	after, _, err := rt.Store.Stats.Rollup(t.Context(), statsmodel.ByProject, from, to, "")
	if err != nil {
		t.Fatal(err)
	}

	if before.Jobs != 1 {
		t.Fatalf("precondition: expected the seeded job in the rollup, got %d", before.Jobs)
	}
	if after.Jobs != before.Jobs || after.EstCostUSD != before.EstCostUSD {
		t.Fatalf("sweep changed history: jobs %d -> %d, cost %v -> %v",
			before.Jobs, after.Jobs, before.EstCostUSD, after.EstCostUSD)
	}
}

// TestPruner_JobLogRespectsSettingsOverride: the Settings UI writes a
// row, and the pruner has to honor it on the next tick without a
// restart.
func TestPruner_JobLogRespectsSettingsOverride(t *testing.T) {
	rt := newPrunerRuntime(t, 31)
	seedProjectPool(t, rt)
	seedFailedJob(t, rt, "j-mid", 10*24*time.Hour, "ten days old")

	// Default 31d would keep it; an override of 7d must not.
	if err := rt.Store.Settings.Put(t.Context(), "job_log_retention_days", "7"); err != nil {
		t.Fatal(err)
	}
	orchestrator.NewPruner(rt).Tick(t.Context())

	j, err := rt.Store.Job.Get(t.Context(), "j-mid")
	if err != nil {
		t.Fatal(err)
	}
	if j.FailureLog != "" {
		t.Fatalf("override ignored: log still present (%q)", j.FailureLog)
	}
}

// TestPruner_LeavesUnfinishedJobsAlone: an in-flight job has no
// completed_at, and its log is still being written.
func TestPruner_LeavesUnfinishedJobsAlone(t *testing.T) {
	rt := newPrunerRuntime(t, 1)
	seedProjectPool(t, rt)
	if err := rt.Store.Job.Put(t.Context(), &jobmodel.Job{
		ID: "j-live", GHJobID: time.Now().UnixNano(), GHRunID: 1, InstallationID: 1,
		RepoFullName: "acme/api", ProjectID: "p-1", PoolID: "po-1",
		Status:   jobmodel.StatusRunning,
		QueuedAt: time.Now().UTC().Add(-90 * 24 * time.Hour), Payload: []byte("{}"),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.DB.DB().ExecContext(t.Context(),
		`UPDATE jobs SET failure_log = ? WHERE id = ?`, "partial output", "j-live"); err != nil {
		t.Fatal(err)
	}

	orchestrator.NewPruner(rt).Tick(t.Context())

	j, err := rt.Store.Job.Get(t.Context(), "j-live")
	if err != nil {
		t.Fatal(err)
	}
	if j.FailureLog != "partial output" {
		t.Fatalf("running job's log was cleared: %q", j.FailureLog)
	}
}

// TestClearLogsOlderThan_ReportsOnlyRowsItChanged: a second sweep over
// an already-clean window must report zero, so the log line does not
// claim work that did not happen.
func TestClearLogsOlderThan_ReportsOnlyRowsItChanged(t *testing.T) {
	rt := newPrunerRuntime(t, 31)
	seedProjectPool(t, rt)
	seedFailedJob(t, rt, "j-a", 40*24*time.Hour, "old output")
	seedFailedJob(t, rt, "j-b", 40*24*time.Hour, "") // already empty

	cutoff := time.Now().UTC().Add(-31 * 24 * time.Hour)
	n, err := rt.Store.Job.ClearLogsOlderThan(t.Context(), cutoff)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("first sweep: cleared %d, want 1 (the empty one is not work)", n)
	}
	n, err = rt.Store.Job.ClearLogsOlderThan(t.Context(), cutoff)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("second sweep: cleared %d, want 0", n)
	}
}
