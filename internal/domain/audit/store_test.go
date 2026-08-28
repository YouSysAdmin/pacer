// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package audit

import (
	"fmt"
	"testing"
	"time"

	auditmodel "github.com/yousysadmin/pacer/internal/models/audit"
	"github.com/yousysadmin/pacer/internal/testutil"
)

func TestAudit_PutListCount_RoundTrip(t *testing.T) {
	s := NewStore(testutil.OpenTestDB(t))
	ctx := t.Context()
	now := time.Now().UTC().Truncate(time.Second)

	for i := range 5 {
		err := s.Put(ctx, &auditmodel.Entry{
			ID:         fmt.Sprintf("e-%d", i),
			Action:     auditmodel.ActionProjectCreated,
			ActorEmail: "op@example.com",
			OccurredAt: now.Add(time.Duration(i) * time.Minute),
		})
		if err != nil {
			t.Fatalf("Put: %v", err)
		}
	}

	entries, err := s.List(ctx, auditmodel.ListFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 5 {
		t.Fatalf("expected 5 entries, got %d", len(entries))
	}
	// Newest first.
	if entries[0].ID != "e-4" || entries[4].ID != "e-0" {
		t.Fatalf("ordering: got %q ... %q", entries[0].ID, entries[4].ID)
	}

	n, err := s.Count(ctx, auditmodel.ListFilter{})
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if n != 5 {
		t.Fatalf("Count: want 5, got %d", n)
	}
}

func TestAudit_FilterAndPaginate_Consistent(t *testing.T) {
	s := NewStore(testutil.OpenTestDB(t))
	ctx := t.Context()
	base := time.Now().UTC().Truncate(time.Second)

	// 10 entries: 6 project.created (ours), 4 pool.created (other).
	for i := range 6 {
		_ = s.Put(ctx, &auditmodel.Entry{
			ID: fmt.Sprintf("p-%d", i), Action: auditmodel.ActionProjectCreated,
			OccurredAt: base.Add(time.Duration(i) * time.Minute),
		})
	}
	for i := range 4 {
		_ = s.Put(ctx, &auditmodel.Entry{
			ID: fmt.Sprintf("o-%d", i), Action: auditmodel.ActionPoolCreated,
			OccurredAt: base.Add(time.Duration(i) * time.Minute),
		})
	}

	filter := auditmodel.ListFilter{Action: auditmodel.ActionProjectCreated}

	count, err := s.Count(ctx, filter)
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if count != 6 {
		t.Fatalf("Count: want 6, got %d", count)
	}

	// Paginate two pages of size 4. The two pages together must equal Count.
	first, err := s.List(ctx, auditmodel.ListFilter{Action: filter.Action, Limit: 4, Offset: 0})
	if err != nil {
		t.Fatalf("List page 1: %v", err)
	}
	second, err := s.List(ctx, auditmodel.ListFilter{Action: filter.Action, Limit: 4, Offset: 4})
	if err != nil {
		t.Fatalf("List page 2: %v", err)
	}
	if len(first)+len(second) != count {
		t.Fatalf("pagination drift: page1=%d page2=%d, count=%d", len(first), len(second), count)
	}

	// No overlap.
	seen := map[string]bool{}
	for _, e := range first {
		seen[e.ID] = true
	}
	for _, e := range second {
		if seen[e.ID] {
			t.Fatalf("duplicate across pages: %s", e.ID)
		}
	}

	// Ensure no `pool.*` rows leaked into the filtered list.
	for _, e := range append(first, second...) {
		if e.Action != auditmodel.ActionProjectCreated {
			t.Fatalf("filter leak: got action %q", e.Action)
		}
	}
}

func TestAudit_TimeWindow(t *testing.T) {
	s := NewStore(testutil.OpenTestDB(t))
	ctx := t.Context()
	base := time.Now().UTC().Truncate(time.Second)

	for i := range 5 {
		_ = s.Put(ctx, &auditmodel.Entry{
			ID: fmt.Sprintf("e-%d", i), Action: auditmodel.ActionProjectCreated,
			OccurredAt: base.Add(time.Duration(i) * time.Hour),
		})
	}

	// Hours [0..4] inserted. Window [1..3) should match e-1, e-2.
	since := base.Add(1 * time.Hour)
	until := base.Add(3 * time.Hour)
	got, err := s.List(ctx, auditmodel.ListFilter{Since: since, Until: until})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 in window, got %d (%v)", len(got), got)
	}

	n, err := s.Count(ctx, auditmodel.ListFilter{Since: since, Until: until})
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if n != 2 {
		t.Fatalf("Count: want 2, got %d", n)
	}
}

func TestAudit_LimitCap(t *testing.T) {
	s := NewStore(testutil.OpenTestDB(t))
	ctx := t.Context()

	for i := range 5 {
		_ = s.Put(ctx, &auditmodel.Entry{
			ID:         fmt.Sprintf("e-%d", i),
			Action:     auditmodel.ActionProjectCreated,
			OccurredAt: time.Now().UTC().Add(time.Duration(i) * time.Second),
		})
	}

	// Limit=0 -> default 100.
	got, err := s.List(ctx, auditmodel.ListFilter{Limit: 0})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("default limit: want 5, got %d", len(got))
	}

	// Excessive limit silently capped at 1000 (just verify it doesn't error).
	got, err = s.List(ctx, auditmodel.ListFilter{Limit: 100000})
	if err != nil {
		t.Fatalf("oversized limit: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("oversized limit returned %d, want 5", len(got))
	}
}

// TestAudit_Q_SearchAcrossColumns is the regression for the
// "search by instance id / IP / etc doesn't work" report. The Q
// filter must hit target_id, detail, client_ip, actor_email,
// request_id, and action all at once -- because that's where
// operators actually look up an event from a clue they have.
func TestAudit_Q_SearchAcrossColumns(t *testing.T) {
	s := NewStore(testutil.OpenTestDB(t))
	ctx := t.Context()
	base := time.Now().UTC().Truncate(time.Second)

	// Seed one row per searchable surface so each LIKE clause
	// is exercised independently.
	rows := []*auditmodel.Entry{
		{
			ID: "by-target-id", Action: auditmodel.ActionInstanceReaped,
			TargetType: "instance", TargetID: "i-0123abcd",
			OccurredAt: base,
		},
		{
			ID: "by-detail-json", Action: auditmodel.ActionJobFailed,
			TargetType: "job",
			Detail:     `{"stage":"ec2","instance_id":"i-9999dead","reason":"capacity"}`,
			OccurredAt: base.Add(1 * time.Minute),
		},
		{
			ID: "by-ip", Action: auditmodel.ActionLoginFailed,
			ClientIP: "203.0.113.42", OccurredAt: base.Add(2 * time.Minute),
		},
		{
			ID: "by-actor", Action: auditmodel.ActionProjectCreated,
			ActorEmail: "alice@example.com", OccurredAt: base.Add(3 * time.Minute),
		},
		{
			ID: "by-request", Action: auditmodel.ActionPoolCreated,
			RequestID: "req-abcdef12", OccurredAt: base.Add(4 * time.Minute),
		},
		{
			ID: "by-action", Action: "instance.launched",
			OccurredAt: base.Add(5 * time.Minute),
		},
		{
			ID: "noise", Action: auditmodel.ActionProjectCreated,
			OccurredAt: base.Add(6 * time.Minute),
		},
	}
	for _, r := range rows {
		if err := s.Put(ctx, r); err != nil {
			t.Fatalf("Put %s: %v", r.ID, err)
		}
	}

	cases := []struct {
		name  string
		q     string
		wants []string
	}{
		// The whole point of the bug fix: searching by a specific
		// instance ID must find both the audit row that targets it
		// directly AND any row whose detail JSON mentions it.
		{"target_id exact", "i-0123abcd", []string{"by-target-id"}},
		{"target_id prefix", "i-0123", []string{"by-target-id"}},
		{"detail json field", "i-9999dead", []string{"by-detail-json"}},
		{"detail json substring", "capacity", []string{"by-detail-json"}},
		{"client_ip", "203.0.113", []string{"by-ip"}},
		{"actor_email", "alice", []string{"by-actor"}},
		{"actor_email tld", "example.com", []string{"by-actor"}},
		{"request_id", "req-abcd", []string{"by-request"}},
		{"action substring", "launched", []string{"by-action"}},
		{"no match returns empty", "totally-unrelated", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := s.List(ctx, auditmodel.ListFilter{Q: c.q})
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			ids := make([]string, 0, len(got))
			for _, g := range got {
				ids = append(ids, g.ID)
			}
			if len(ids) != len(c.wants) {
				t.Fatalf("len: want %v, got %v", c.wants, ids)
			}
			want := map[string]bool{}
			for _, w := range c.wants {
				want[w] = true
			}
			for _, id := range ids {
				if !want[id] {
					t.Errorf("unexpected match: %s (wanted %v)", id, c.wants)
				}
			}

			// Count must agree with List on the same filter -- this
			// is the invariant the pagination contract relies on.
			n, err := s.Count(ctx, auditmodel.ListFilter{Q: c.q})
			if err != nil {
				t.Fatalf("Count: %v", err)
			}
			if n != len(c.wants) {
				t.Errorf("Count: want %d, got %d", len(c.wants), n)
			}
		})
	}
}

func TestAudit_Q_CombinesWithOtherFilters(t *testing.T) {
	// Q is AND-combined with action / time window. An operator who
	// scoped to "the last day of job.failed events" still expects
	// their text search to narrow within that subset, not blow it
	// open to every action that happens to contain the needle.
	s := NewStore(testutil.OpenTestDB(t))
	ctx := t.Context()
	base := time.Now().UTC().Truncate(time.Second)

	_ = s.Put(ctx, &auditmodel.Entry{
		ID: "match", Action: auditmodel.ActionJobFailed,
		TargetID: "i-deadbeef01", OccurredAt: base,
	})
	_ = s.Put(ctx, &auditmodel.Entry{
		ID: "wrong-action", Action: auditmodel.ActionJobCompleted,
		TargetID: "i-deadbeef02", OccurredAt: base.Add(1 * time.Minute),
	})
	_ = s.Put(ctx, &auditmodel.Entry{
		ID: "wrong-needle", Action: auditmodel.ActionJobFailed,
		TargetID: "i-fffeeeee", OccurredAt: base.Add(2 * time.Minute),
	})

	got, err := s.List(ctx, auditmodel.ListFilter{
		Action: auditmodel.ActionJobFailed,
		Q:      "i-deadbeef",
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].ID != "match" {
		t.Fatalf("want exactly [match], got %v", got)
	}
}

func TestAudit_Q_EscapesLikeMetacharacters(t *testing.T) {
	// A search for "100%" must not match "1001%foo" via LIKE %
	// wildcard behavior. Same for underscore. This is the
	// difference between a literal substring search and a SQL LIKE
	// pattern that happens to use the user's input as a pattern.
	s := NewStore(testutil.OpenTestDB(t))
	ctx := t.Context()
	now := time.Now().UTC()

	_ = s.Put(ctx, &auditmodel.Entry{
		ID: "literal-pct", Action: "demo",
		TargetID: "ratio=100%done", OccurredAt: now,
	})
	_ = s.Put(ctx, &auditmodel.Entry{
		ID: "underscore", Action: "demo",
		TargetID: "tag_x_y", OccurredAt: now.Add(1 * time.Minute),
	})
	_ = s.Put(ctx, &auditmodel.Entry{
		ID: "no-pct", Action: "demo",
		TargetID: "ratio=100done", OccurredAt: now.Add(2 * time.Minute),
	})

	// Searching for "100%" must hit ONLY the literal "100%done"
	// row, not "100done".
	got, err := s.List(ctx, auditmodel.ListFilter{Q: "100%"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].ID != "literal-pct" {
		ids := []string{}
		for _, g := range got {
			ids = append(ids, g.ID)
		}
		t.Fatalf("100%% search: want [literal-pct], got %v", ids)
	}

	// Searching for "tag_" must hit the literal row, not match
	// arbitrary single-char positions via LIKE underscore.
	got, err = s.List(ctx, auditmodel.ListFilter{Q: "tag_"})
	if err != nil {
		t.Fatalf("List tag_: %v", err)
	}
	if len(got) != 1 || got[0].ID != "underscore" {
		t.Fatalf("tag_ search: want [underscore], got %v", got)
	}
}

func TestAudit_DeleteOlderThan(t *testing.T) {
	s := NewStore(testutil.OpenTestDB(t))
	ctx := t.Context()
	base := time.Now().UTC().Truncate(time.Second)

	for i := range 5 {
		_ = s.Put(ctx, &auditmodel.Entry{
			ID: fmt.Sprintf("e-%d", i), Action: auditmodel.ActionProjectCreated,
			OccurredAt: base.Add(time.Duration(i) * time.Hour),
		})
	}

	cutoff := base.Add(2 * time.Hour)
	deleted, err := s.DeleteOlderThan(ctx, cutoff)
	if err != nil {
		t.Fatalf("DeleteOlderThan: %v", err)
	}
	if deleted != 2 {
		t.Fatalf("deleted: want 2 (e-0, e-1), got %d", deleted)
	}

	n, _ := s.Count(ctx, auditmodel.ListFilter{})
	if n != 3 {
		t.Fatalf("after delete: want 3 remaining, got %d", n)
	}
}
