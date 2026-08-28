// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package instance

import (
	"database/sql"
	"testing"
	"time"

	instancemodel "github.com/yousysadmin/pacer/internal/models/instance"
	"github.com/yousysadmin/pacer/internal/testutil"
)

// newTestStore opens a fresh DB and seeds one project + one job per
// requested instance ID so the instances.job_id / project_id FKs are
// satisfied. Returns the store.
func newTestStore(t *testing.T) (*Store, *sql.DB) {
	t.Helper()
	db := testutil.OpenTestDB(t)
	// Single project shared by every seeded instance.
	if _, err := db.Exec(`INSERT INTO projects (id, name, scope) VALUES (?, ?, ?)`,
		"proj-1", "proj-1", "repo"); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	return NewStore(db), db
}

// seedInstance inserts the prerequisite job row, then a running
// instance row via the store. last_seen_at is left nil so callers
// can assert that Touch populates it.
func seedInstance(t *testing.T, s *Store, db *sql.DB, id string) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO jobs (id, project_id, status, payload, repo_full_name, gh_job_id, gh_run_id, installation_id, queued_at, sender_login)
		 VALUES (?, ?, 'claimed', '{}', 'owner/repo', ?, ?, 1, CURRENT_TIMESTAMP, 'tester')`,
		"job-"+id, "proj-1", int64(rowSeq()), int64(rowSeq())); err != nil {
		t.Fatalf("seed job: %v", err)
	}
	now := time.Now().UTC()
	err := s.Put(t.Context(), &instancemodel.Instance{
		ID:         id,
		JobID:      "job-" + id,
		ProjectID:  "proj-1",
		State:      instancemodel.StateRunning,
		LaunchedAt: now.Add(-5 * time.Minute),
	})
	if err != nil {
		t.Fatalf("seed Put: %v", err)
	}
}

// rowSeq is a monotonic counter so seeded gh_job_id / gh_run_id values
// don't collide on the UNIQUE indices.
var rowSeqCounter int64

func rowSeq() int64 {
	rowSeqCounter++
	return rowSeqCounter
}

// TestTouch_BumpsLastSeenAt is the heartbeat-update happy path. The
// reaper calls Touch on every alive instance every tick. The round
// trip back through Get must reflect the new timestamp so the UI
// has a value to render.
func TestTouch_BumpsLastSeenAt(t *testing.T) {
	s, db := newTestStore(t)
	_ = db
	ctx := t.Context()

	seedInstance(t, s, db, "i-aaa")

	before, err := s.Get(ctx, "i-aaa")
	if err != nil {
		t.Fatalf("Get before: %v", err)
	}
	if before.LastSeenAt != nil {
		t.Fatalf("seed should leave LastSeenAt nil, got %v", *before.LastSeenAt)
	}

	now := time.Now().UTC()
	if err := s.Touch(ctx, []string{"i-aaa"}, now); err != nil {
		t.Fatalf("Touch: %v", err)
	}

	after, err := s.Get(ctx, "i-aaa")
	if err != nil {
		t.Fatalf("Get after: %v", err)
	}
	if after.LastSeenAt == nil {
		t.Fatal("LastSeenAt nil after Touch")
	}
	if dur := time.Since(*after.LastSeenAt); dur < 0 || dur > time.Minute {
		t.Errorf("LastSeenAt drift: %v ago (round-trip should be < 1s)", dur)
	}
	// Critically: Touch must NOT change state or terminated_at -- it's
	// a heartbeat, not a state transition.
	if after.State != instancemodel.StateRunning {
		t.Errorf("Touch changed state: was %q, now %q", instancemodel.StateRunning, after.State)
	}
	if after.TerminatedAt != nil {
		t.Errorf("Touch set TerminatedAt: %v", *after.TerminatedAt)
	}
}

func TestTouch_Batch(t *testing.T) {
	s, db := newTestStore(t)
	_ = db
	ctx := t.Context()
	seedInstance(t, s, db, "i-1")
	seedInstance(t, s, db, "i-2")
	seedInstance(t, s, db, "i-3")

	now := time.Now().UTC()
	if err := s.Touch(ctx, []string{"i-1", "i-2", "i-3"}, now); err != nil {
		t.Fatalf("Touch batch: %v", err)
	}
	for _, id := range []string{"i-1", "i-2", "i-3"} {
		got, err := s.Get(ctx, id)
		if err != nil {
			t.Fatalf("Get %s: %v", id, err)
		}
		if got.LastSeenAt == nil {
			t.Errorf("%s: LastSeenAt still nil after batch Touch", id)
		}
	}
}

func TestTouch_EmptyIDs_NoOp(t *testing.T) {
	s, db := newTestStore(t)
	_ = db
	// Should not error and should not touch the DB. Reaper hits
	// this path on every idle sweep (no alive instances), so it's
	// the most common call -- it has to be cheap.
	if err := s.Touch(t.Context(), nil, time.Now()); err != nil {
		t.Fatalf("Touch with empty ids: %v", err)
	}
	if err := s.Touch(t.Context(), []string{}, time.Now()); err != nil {
		t.Fatalf("Touch with empty slice: %v", err)
	}
}

func TestTouch_UnknownID_NoError(t *testing.T) {
	// An ID we don't have a row for must not error -- UPDATE on no
	// matching row is silently a 0-rows-affected no-op. We rely on
	// this so the reaper can pass the AWS-returned ID set straight
	// through without filtering against ListAlive twice.
	s, db := newTestStore(t)
	_ = db
	if err := s.Touch(t.Context(), []string{"i-does-not-exist"}, time.Now()); err != nil {
		t.Fatalf("Touch unknown id: %v", err)
	}
}
