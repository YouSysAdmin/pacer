// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package pool

import (
	"context"
	"database/sql"
	"testing"
	"time"

	poolmodel "github.com/yousysadmin/pacer/internal/models/pool"
	"github.com/yousysadmin/pacer/internal/testutil"
)

// TestStore_Delete_NullsHistoricalReferences verifies that deleting a pool
// with historical (terminated) jobs and instances pointing at it succeeds
// and leaves those rows intact with pool_id NULL. Without the tx-based
// NULL-out, FK ON DELETE RESTRICT on jobs/instances would reject the
// DELETE and the operator would have to drop into sqlite by hand.
func TestStore_Delete_NullsHistoricalReferences(t *testing.T) {
	db := testutil.OpenTestDB(t)
	store := NewStore(db)
	ctx := context.Background()

	const projectID = "proj-1"
	const poolID = "pool-doomed"
	insertProject(t, db, projectID, "proj")
	mustPutPool(t, store, &poolmodel.Pool{
		ID: poolID, ProjectID: projectID, Name: "doomed",
		AMIID: "ami-x", InstanceTypes: []string{"t3.large"},
		SubnetIDs: []string{"subnet-1"}, SecurityGroupIDs: []string{"sg-1"},
		MaxRuntimeMinutes: 60, MaxConcurrentRunners: 5,
	})
	insertJob(t, db, "job-done", projectID, poolID, "completed")
	insertInstance(t, db, "inst-done", "job-done", projectID, poolID, "terminated")

	if err := store.Delete(ctx, poolID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Pool row gone.
	got, err := store.Get(ctx, poolID)
	if err != nil {
		t.Fatalf("Get after delete: %v", err)
	}
	if got != nil {
		t.Fatalf("pool row should be gone, got %+v", got)
	}

	// Job row still there, pool_id NULL.
	var jobPoolID sql.NullString
	if err := db.QueryRowContext(ctx,
		`SELECT pool_id FROM jobs WHERE id = ?`, "job-done").Scan(&jobPoolID); err != nil {
		t.Fatalf("read job row: %v", err)
	}
	if jobPoolID.Valid {
		t.Fatalf("job.pool_id should be NULL, got %q", jobPoolID.String)
	}

	// Instance row still there, pool_id NULL.
	var instPoolID sql.NullString
	if err := db.QueryRowContext(ctx,
		`SELECT pool_id FROM instances WHERE id = ?`, "inst-done").Scan(&instPoolID); err != nil {
		t.Fatalf("read instance row: %v", err)
	}
	if instPoolID.Valid {
		t.Fatalf("instance.pool_id should be NULL, got %q", instPoolID.String)
	}
}

// Deleting a non-existent pool is a no-op success (UPDATE/DELETE match 0
// rows). Verifies the tx commits cleanly when nothing changes.
func TestStore_Delete_NoOpOnUnknownPool(t *testing.T) {
	db := testutil.OpenTestDB(t)
	store := NewStore(db)
	if err := store.Delete(context.Background(), "nonexistent"); err != nil {
		t.Fatalf("Delete unknown pool: %v", err)
	}
}

// --- helpers ---

func mustPutPool(t *testing.T, s *Store, p *poolmodel.Pool) {
	t.Helper()
	if err := s.Put(context.Background(), p); err != nil {
		t.Fatalf("Put pool %q: %v", p.ID, err)
	}
}

func insertProject(t *testing.T, db *sql.DB, id, name string) {
	t.Helper()
	_, err := db.ExecContext(context.Background(), `
		INSERT INTO projects (id, name, max_concurrent_runners, tags, scope, org_name, runner_group_id, disabled)
		VALUES (?, ?, 0, '{}', 'repo', '', 0, 0)`, id, name)
	if err != nil {
		t.Fatalf("insertProject: %v", err)
	}
}

func insertJob(t *testing.T, db *sql.DB, id, projectID, poolID, status string) {
	t.Helper()
	_, err := db.ExecContext(context.Background(), `
		INSERT INTO jobs (
			id, gh_job_id, gh_run_id, repo_full_name, project_id, pool_id,
			installation_id, status, queued_at, payload
		) VALUES (?, ?, 1, 'octocat/repo', ?, ?, 1, ?, ?, '{}')`,
		id, time.Now().UnixNano(), projectID, poolID, status, time.Now().UTC())
	if err != nil {
		t.Fatalf("insertJob: %v", err)
	}
}

func insertInstance(t *testing.T, db *sql.DB, id, jobID, projectID, poolID, state string) {
	t.Helper()
	_, err := db.ExecContext(context.Background(), `
		INSERT INTO instances (id, job_id, project_id, pool_id, state, spot, launched_at)
		VALUES (?, ?, ?, ?, ?, 0, ?)`,
		id, jobID, projectID, poolID, state, time.Now().UTC())
	if err != nil {
		t.Fatalf("insertInstance: %v", err)
	}
}
