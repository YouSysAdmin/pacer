// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package ghrunner

import (
	"context"
	"testing"
)

func TestNew_FailedFetchStillReturnsResolver(t *testing.T) {
	// A cancelled ctx makes the initial fetch fail immediately.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := New(ctx)
	if r == nil {
		t.Fatal("New must never return nil")
	}
	if r.Latest() != "" {
		t.Fatalf("expected empty cache, got %q", r.Latest())
	}
	if got := r.Resolve("2.300.0"); got != "2.300.0" {
		t.Fatalf("pool pin must win: %q", got)
	}
}

func TestResolve_NilReceiverUsesPin(t *testing.T) {
	var r *Resolver
	if got := r.Resolve("2.1.0"); got != "2.1.0" {
		t.Fatalf("got %q", got)
	}
}
