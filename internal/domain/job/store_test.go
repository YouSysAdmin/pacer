// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package job

import (
	"context"
	"database/sql"
	"testing"
	"time"

	jobmodel "github.com/yousysadmin/pacer/internal/models/job"
	"github.com/yousysadmin/pacer/internal/testutil"
)

// fixture is a project + pool inserted into the test DB; the helpers
// below stamp the FK columns onto every test job so Job.Claim's
// project/pool joins succeed.
type fixture struct {
	store     *Store
	db        *sql.DB
	projectID string
	poolID    string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	db := testutil.OpenTestDB(t)
	f := &fixture{
		store:     NewStore(db),
		db:        db,
		projectID: "p-" + t.Name(),
		poolID:    "po-" + t.Name(),
	}
	insertProject(t, db, f.projectID, "proj", 0, false)
	insertPool(t, db, f.poolID, f.projectID, "default", 5, false)
	return f
}

func TestJob_Claim_PicksOldestQueued(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()

	// Insert two queued jobs; older one wins.
	mustPut(t, f, "job-old", jobmodel.StatusQueued, now.Add(-1*time.Minute), nil, 0)
	mustPut(t, f, "job-new", jobmodel.StatusQueued, now, nil, 0)

	got, err := f.store.Claim(ctx, now.Add(time.Second))
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if got == nil {
		t.Fatal("expected to claim a job, got nil")
	}
	if got.ID != "job-old" {
		t.Fatalf("expected job-old, got %q", got.ID)
	}
	if got.Status != jobmodel.StatusClaimed {
		t.Fatalf("expected status claimed, got %q", got.Status)
	}
	if got.ClaimedAt == nil {
		t.Fatal("ClaimedAt should be set after Claim")
	}
}

func TestJob_Claim_NothingToClaim(t *testing.T) {
	f := newFixture(t)
	got, err := f.store.Claim(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil on empty queue, got %v", got)
	}
}

func TestJob_Claim_SkipsRowsBeyondPoolCap(t *testing.T) {
	f := newFixture(t)
	// Pool cap is 5 (set in newFixture). Fill 5 active slots, queue a sixth.
	now := time.Now().UTC()
	for i := 0; i < 5; i++ {
		mustPut(t, f, idN("running", i), jobmodel.StatusRunning, now.Add(-time.Hour), nil, 0)
	}
	mustPut(t, f, "queued-blocked", jobmodel.StatusQueued, now, nil, 0)

	got, err := f.store.Claim(context.Background(), now.Add(time.Second))
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil (pool at cap), got job %q", got.ID)
	}

	// Free a slot by completing one running job; now Claim should succeed.
	if _, err := f.db.ExecContext(context.Background(),
		`UPDATE jobs SET status='completed' WHERE id=?`, idN("running", 0)); err != nil {
		t.Fatalf("free a slot: %v", err)
	}
	got, err = f.store.Claim(context.Background(), now.Add(time.Second))
	if err != nil {
		t.Fatalf("Claim after free: %v", err)
	}
	if got == nil || got.ID != "queued-blocked" {
		t.Fatalf("expected queued-blocked, got %v", got)
	}
}

func TestJob_Claim_RespectsProjectCeiling(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	// Tighten the project ceiling to 2 (pool cap stays 5 -- project wins).
	if _, err := f.db.ExecContext(ctx,
		`UPDATE projects SET max_concurrent_runners=2 WHERE id=?`, f.projectID); err != nil {
		t.Fatalf("tighten project: %v", err)
	}

	now := time.Now().UTC()
	mustPut(t, f, "active-1", jobmodel.StatusRunning, now.Add(-time.Hour), nil, 0)
	mustPut(t, f, "active-2", jobmodel.StatusRunning, now.Add(-time.Hour), nil, 0)
	mustPut(t, f, "blocked", jobmodel.StatusQueued, now, nil, 0)

	got, err := f.store.Claim(ctx, now.Add(time.Second))
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if got != nil {
		t.Fatalf("project ceiling should block claim, got %q", got.ID)
	}

	// 0 means no project ceiling; pool cap 5 leaves room.
	if _, err := f.db.ExecContext(ctx,
		`UPDATE projects SET max_concurrent_runners=0 WHERE id=?`, f.projectID); err != nil {
		t.Fatalf("relax project: %v", err)
	}
	got, err = f.store.Claim(ctx, now.Add(time.Second))
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if got == nil || got.ID != "blocked" {
		t.Fatalf("expected blocked to be claimed after relax, got %v", got)
	}
}

func TestJob_Claim_SkipsDisabledProjectAndPool(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()

	t.Run("disabled project", func(t *testing.T) {
		f := newFixture(t)
		if _, err := f.db.ExecContext(ctx,
			`UPDATE projects SET disabled=1 WHERE id=?`, f.projectID); err != nil {
			t.Fatalf("disable project: %v", err)
		}
		mustPut(t, f, "j1", jobmodel.StatusQueued, now, nil, 0)

		got, err := f.store.Claim(ctx, now.Add(time.Second))
		if err != nil {
			t.Fatalf("Claim: %v", err)
		}
		if got != nil {
			t.Fatalf("disabled project should block claim, got %q", got.ID)
		}
	})

	t.Run("disabled pool", func(t *testing.T) {
		f := newFixture(t)
		if _, err := f.db.ExecContext(ctx,
			`UPDATE pools SET disabled=1 WHERE id=?`, f.poolID); err != nil {
			t.Fatalf("disable pool: %v", err)
		}
		mustPut(t, f, "j1", jobmodel.StatusQueued, now, nil, 0)

		got, err := f.store.Claim(ctx, now.Add(time.Second))
		if err != nil {
			t.Fatalf("Claim: %v", err)
		}
		if got != nil {
			t.Fatalf("disabled pool should block claim, got %q", got.ID)
		}
	})
}

func TestJob_Claim_NextRetryAtGatesClaim(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	future := now.Add(5 * time.Minute)

	mustPut(t, f, "rescheduled", jobmodel.StatusQueued, now.Add(-time.Hour), &future, 1)
	mustPut(t, f, "ready", jobmodel.StatusQueued, now, nil, 0)

	// Now is before NextRetryAt -- only `ready` is claimable, despite being newer.
	got, err := f.store.Claim(ctx, now)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if got == nil || got.ID != "ready" {
		t.Fatalf("expected ready, got %v", got)
	}

	// Advance past NextRetryAt; the rescheduled job is now visible.
	got, err = f.store.Claim(ctx, future.Add(time.Second))
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if got == nil || got.ID != "rescheduled" {
		t.Fatalf("expected rescheduled, got %v", got)
	}
}

func TestJob_Reschedule_FlipsBackToQueued(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()

	mustPut(t, f, "j1", jobmodel.StatusQueued, now, nil, 0)
	claimed, err := f.store.Claim(ctx, now)
	if err != nil || claimed == nil {
		t.Fatalf("setup Claim: %v %v", claimed, err)
	}

	retryAt := now.Add(30 * time.Second)
	if err := f.store.Reschedule(ctx, claimed.ID, 1, retryAt); err != nil {
		t.Fatalf("Reschedule: %v", err)
	}

	got, err := f.store.Get(ctx, claimed.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != jobmodel.StatusQueued {
		t.Fatalf("status: want queued, got %q", got.Status)
	}
	if got.Attempts != 1 {
		t.Fatalf("Attempts: want 1, got %d", got.Attempts)
	}
	if got.NextRetryAt == nil || !got.NextRetryAt.Equal(retryAt) {
		t.Fatalf("NextRetryAt: want %v, got %v", retryAt, got.NextRetryAt)
	}
	if got.ClaimedAt != nil {
		t.Fatalf("ClaimedAt should clear on reschedule, got %v", got.ClaimedAt)
	}
}

func TestJob_UpdatePayloadIfRunning_OnlyUpdatesRunningRows(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()

	mustPut(t, f, "j-run", jobmodel.StatusRunning, now, nil, 0)
	mustPut(t, f, "j-done", jobmodel.StatusCompleted, now, nil, 0)

	fresh := []byte(`{"action":"in_progress","workflow_job":{"steps":[1,2,3]}}`)

	if err := f.store.UpdatePayloadIfRunning(ctx, "j-run", fresh); err != nil {
		t.Fatalf("UpdatePayloadIfRunning(running): %v", err)
	}
	if err := f.store.UpdatePayloadIfRunning(ctx, "j-done", fresh); err != nil {
		// Zero rows affected isn't an error -- the WHERE just skipped.
		t.Fatalf("UpdatePayloadIfRunning(completed): %v", err)
	}

	gotRun, err := f.store.Get(ctx, "j-run")
	if err != nil {
		t.Fatalf("Get(j-run): %v", err)
	}
	if string(gotRun.Payload) != string(fresh) {
		t.Fatalf("running row should have new payload, got %q", string(gotRun.Payload))
	}

	gotDone, err := f.store.Get(ctx, "j-done")
	if err != nil {
		t.Fatalf("Get(j-done): %v", err)
	}
	if string(gotDone.Payload) == string(fresh) {
		t.Fatalf("completed row payload should NOT have been overwritten, got %q", string(gotDone.Payload))
	}
}

// TestJob_ListCountPaginate_Consistent locks in the pagination
// contract the jobs-page pager relies on: List+Offset returns
// non-overlapping subsets, Count agrees with List ignoring pagination,
// and the filter is applied symmetrically to both.
func TestJob_ListCountPaginate_Consistent(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	base := time.Now().UTC().Truncate(time.Second)

	// 10 jobs: 6 completed, 4 failed. Older queued_at first so the
	// DESC order on queued_at produces a deterministic sequence.
	for i := 0; i < 6; i++ {
		mustPut(t, f, "c-"+idN("", i), jobmodel.StatusCompleted, base.Add(time.Duration(i)*time.Second), nil, 0)
	}
	for i := 0; i < 4; i++ {
		mustPut(t, f, "f-"+idN("", i), jobmodel.StatusFailed, base.Add(time.Duration(i+6)*time.Second), nil, 0)
	}

	// Filter: completed only. Count = 6.
	filt := jobmodel.ListFilter{Status: jobmodel.StatusCompleted}
	n, err := f.store.Count(ctx, filt)
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if n != 6 {
		t.Fatalf("Count: want 6, got %d", n)
	}

	// Two pages of size 4 -- pages must concat to Count, no overlap.
	page1, err := f.store.List(ctx, jobmodel.ListFilter{Status: filt.Status, Limit: 4, Offset: 0})
	if err != nil {
		t.Fatalf("List page 1: %v", err)
	}
	page2, err := f.store.List(ctx, jobmodel.ListFilter{Status: filt.Status, Limit: 4, Offset: 4})
	if err != nil {
		t.Fatalf("List page 2: %v", err)
	}
	if len(page1)+len(page2) != n {
		t.Fatalf("pagination drift: page1=%d + page2=%d != count=%d",
			len(page1), len(page2), n)
	}
	seen := map[string]bool{}
	for _, j := range page1 {
		seen[j.ID] = true
		if j.Status != jobmodel.StatusCompleted {
			t.Errorf("filter leak on page1: %s status=%s", j.ID, j.Status)
		}
	}
	for _, j := range page2 {
		if seen[j.ID] {
			t.Errorf("duplicate across pages: %s", j.ID)
		}
		if j.Status != jobmodel.StatusCompleted {
			t.Errorf("filter leak on page2: %s status=%s", j.ID, j.Status)
		}
	}

	// Offset past the end returns empty, not error.
	tail, err := f.store.List(ctx, jobmodel.ListFilter{Status: filt.Status, Limit: 4, Offset: 100})
	if err != nil {
		t.Fatalf("List past end: %v", err)
	}
	if len(tail) != 0 {
		t.Fatalf("past-end list: want 0, got %d", len(tail))
	}

	// No-filter Count covers every row.
	all, err := f.store.Count(ctx, jobmodel.ListFilter{})
	if err != nil {
		t.Fatalf("Count(all): %v", err)
	}
	if all != 10 {
		t.Fatalf("Count(all): want 10, got %d", all)
	}
}

// --- helpers ---

func idN(prefix string, i int) string {
	return prefix + "-" + string(rune('0'+i))
}

func mustPut(t *testing.T, f *fixture, id string, st jobmodel.Status, queuedAt time.Time, nextRetry *time.Time, attempts int) {
	t.Helper()
	j := &jobmodel.Job{
		ID:             id,
		GHJobID:        time.Now().UnixNano() + int64(len(id)), // unique
		GHRunID:        1,
		InstallationID: 1,
		RepoFullName:   "octocat/hello-world",
		ProjectID:      f.projectID,
		PoolID:         f.poolID,
		Status:         st,
		QueuedAt:       queuedAt,
		NextRetryAt:    nextRetry,
		Attempts:       attempts,
		Payload:        []byte("{}"),
	}
	if err := f.store.Put(context.Background(), j); err != nil {
		t.Fatalf("Put %q: %v", id, err)
	}
}

func insertProject(t *testing.T, db *sql.DB, id, name string, ceiling int, disabled bool) {
	t.Helper()
	d := 0
	if disabled {
		d = 1
	}
	_, err := db.ExecContext(context.Background(), `
		INSERT INTO projects (id, name, max_concurrent_runners, tags, scope, org_name, runner_group_id, disabled)
		VALUES (?, ?, ?, '{}', 'repo', '', 0, ?)`,
		id, name, ceiling, d)
	if err != nil {
		t.Fatalf("insertProject: %v", err)
	}
}

func insertPool(t *testing.T, db *sql.DB, id, projectID, name string, cap int, disabled bool) {
	t.Helper()
	d := 0
	if disabled {
		d = 1
	}
	_, err := db.ExecContext(context.Background(), `
		INSERT INTO pools (
			id, project_id, name, is_default, priority,
			ami_id, instance_types, subnet_ids, security_group_ids,
			iam_instance_profile, root_volume_gb, max_runtime_minutes,
			max_concurrent_runners, spot, spawn_method, allocation_strategy,
			extra_labels, tags, runner_version, runner_user, disabled
		)
		VALUES (?, ?, ?, 1, 100,
			'ami-test', '["t3.large"]', '["subnet-1"]', '["sg-1"]',
			'', 30, 60, ?, 1, 'fleet', 'cost',
			'[]', '{}', '', '', ?)`,
		id, projectID, name, cap, d)
	if err != nil {
		t.Fatalf("insertPool: %v", err)
	}
}
