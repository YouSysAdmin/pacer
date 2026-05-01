// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package audit

import (
	"context"
	"fmt"
	"testing"
	"time"

	auditmodel "github.com/yousysadmin/pacer/internal/models/audit"
	"github.com/yousysadmin/pacer/internal/testutil"
)

func TestAudit_PutListCount_RoundTrip(t *testing.T) {
	s := NewStore(testutil.OpenTestDB(t))
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	for i := 0; i < 5; i++ {
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
	ctx := context.Background()
	base := time.Now().UTC().Truncate(time.Second)

	// 10 entries: 6 project.created (ours), 4 pool.created (other).
	for i := 0; i < 6; i++ {
		_ = s.Put(ctx, &auditmodel.Entry{
			ID: fmt.Sprintf("p-%d", i), Action: auditmodel.ActionProjectCreated,
			OccurredAt: base.Add(time.Duration(i) * time.Minute),
		})
	}
	for i := 0; i < 4; i++ {
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
	ctx := context.Background()
	base := time.Now().UTC().Truncate(time.Second)

	for i := 0; i < 5; i++ {
		_ = s.Put(ctx, &auditmodel.Entry{
			ID: fmt.Sprintf("e-%d", i), Action: auditmodel.ActionProjectCreated,
			OccurredAt: base.Add(time.Duration(i) * time.Hour),
		})
	}

	// Hours [0..4] inserted; window [1..3) should match e-1, e-2.
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
	ctx := context.Background()

	for i := 0; i < 5; i++ {
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

func TestAudit_DeleteOlderThan(t *testing.T) {
	s := NewStore(testutil.OpenTestDB(t))
	ctx := context.Background()
	base := time.Now().UTC().Truncate(time.Second)

	for i := 0; i < 5; i++ {
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
