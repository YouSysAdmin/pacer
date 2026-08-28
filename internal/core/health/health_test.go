// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package health

import (
	"sync"
	"testing"
	"time"
)

func TestSetGetClearSnapshot(t *testing.T) {
	h := New()
	if got := h.Snapshot(); len(got) != 0 {
		t.Fatalf("empty snapshot: want 0, got %d", len(got))
	}

	h.Set("reaper", "describe failed")
	h.Set("preflight", "missing perm")

	if msg, ok := h.Get("reaper"); !ok || msg != "describe failed" {
		t.Fatalf("Get reaper: ok=%v msg=%q", ok, msg)
	}
	snap := h.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("snapshot len: want 2, got %d", len(snap))
	}
	// Snapshot must be sorted by component for deterministic UI order.
	if snap[0].Component != "preflight" || snap[1].Component != "reaper" {
		t.Fatalf("snapshot order: %v", snap)
	}
	for _, i := range snap {
		if i.Since.IsZero() {
			t.Fatalf("Since not stamped on %s", i.Component)
		}
	}

	h.Clear("reaper")
	if _, ok := h.Get("reaper"); ok {
		t.Fatalf("reaper not cleared")
	}
	if got := len(h.Snapshot()); got != 1 {
		t.Fatalf("post-clear snapshot len: want 1, got %d", got)
	}

	// Clearing a missing component is a no-op.
	h.Clear("does-not-exist")
}

func TestSet_PreservesSinceOnRepeat(t *testing.T) {
	h := New()
	h.Set("reaper", "describe failed")
	orig, _ := h.Get("reaper")
	if orig != "describe failed" {
		t.Fatalf("initial msg: %q", orig)
	}
	since := h.Snapshot()[0].Since

	time.Sleep(2 * time.Millisecond)

	// Same message: Since must not move.
	h.Set("reaper", "describe failed")
	if got := h.Snapshot()[0].Since; !got.Equal(since) {
		t.Fatalf("Since moved on identical Set: was %v, now %v", since, got)
	}

	// Different message: Since must move.
	h.Set("reaper", "panic recovered")
	if got := h.Snapshot()[0].Since; !got.After(since) {
		t.Fatalf("Since did not advance on changed message: was %v, now %v", since, got)
	}
}

func TestSet_EmptyComponent_NoOp(t *testing.T) {
	h := New()
	h.Set("", "msg")
	if got := len(h.Snapshot()); got != 0 {
		t.Fatalf("empty component should not insert: got %d", got)
	}
}

// TestRace exercises Set/Clear/Snapshot/Get from many goroutines so
// `go test -race` proves the locking is sound. Without the mutex the
// underlying map racing kills the test.
func TestRace(t *testing.T) {
	h := New()
	var wg sync.WaitGroup
	for i := range 10 {
		wg.Add(4)
		go func(n int) {
			defer wg.Done()
			for range 200 {
				h.Set("reaper", "msg")
				_ = n
			}
		}(i)
		go func() {
			defer wg.Done()
			for range 200 {
				h.Clear("reaper")
			}
		}()
		go func() {
			defer wg.Done()
			for range 200 {
				_ = h.Snapshot()
			}
		}()
		go func() {
			defer wg.Done()
			for range 200 {
				_, _ = h.Get("reaper")
			}
		}()
	}
	wg.Wait()
}
