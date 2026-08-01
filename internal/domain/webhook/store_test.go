// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package webhook

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/yousysadmin/pacer/internal/core/env"
	"github.com/yousysadmin/pacer/internal/testutil/runtimeutil"
)

// The table historically holds two timestamp shapes, both UTC:
// 'YYYY-MM-DD HH:MM:SS' from the column's DEFAULT CURRENT_TIMESTAMP
// (rows written before persistDelivery supplied the column) and the
// driver's 'YYYY-MM-DD HH:MM:SS.fffffffff +0000 UTC'. The pruner's
// textual comparison must stay chronologically correct across both,
// and must not depend on the caller passing a UTC cutoff.
func TestDeleteOlderThan_MixedTimestampFormats(t *testing.T) {
	rt := runtimeutil.NewRuntime(t, &env.Config{})
	db := rt.DB.DB()
	store := NewStore(db)
	ctx := context.Background()
	now := time.Now().UTC()

	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatalf("exec: %v", err)
		}
	}

	const legacyFmt = "2006-01-02 15:04:05" // CURRENT_TIMESTAMP shape
	// Fresh rows (1h old) in both formats: must survive a 24h cutoff.
	exec(`INSERT INTO webhook_deliveries (id, event, payload, received_at) VALUES (?, ?, ?, ?)`,
		"fresh-driver", "ping", "{}", now.Add(-1*time.Hour))
	exec(`INSERT INTO webhook_deliveries (id, event, payload, received_at) VALUES (?, ?, ?, ?)`,
		"fresh-legacy", "ping", "{}", now.Add(-1*time.Hour).Format(legacyFmt))
	// Stale rows (48h old) in both formats: must be pruned.
	exec(`INSERT INTO webhook_deliveries (id, event, payload, received_at) VALUES (?, ?, ?, ?)`,
		"stale-driver", "ping", "{}", now.Add(-48*time.Hour))
	exec(`INSERT INTO webhook_deliveries (id, event, payload, received_at) VALUES (?, ?, ?, ?)`,
		"stale-legacy", "ping", "{}", now.Add(-48*time.Hour).Format(legacyFmt))

	// Cutoff deliberately in a non-UTC zone: DeleteOlderThan must
	// normalize before binding, or the textual comparison shifts by
	// the zone offset.
	east := time.FixedZone("UTC+5", 5*3600)
	n, err := store.DeleteOlderThan(ctx, now.Add(-24*time.Hour).In(east))
	if err != nil {
		t.Fatalf("DeleteOlderThan: %v", err)
	}
	if n != 2 {
		t.Fatalf("deleted rows: want 2, got %d", n)
	}
	for _, tc := range []struct {
		id   string
		want bool
	}{
		{"fresh-driver", true},
		{"fresh-legacy", true},
		{"stale-driver", false},
		{"stale-legacy", false},
	} {
		var count int
		if err := db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM webhook_deliveries WHERE id = ?`, tc.id).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", tc.id, err)
		}
		if got := count == 1; got != tc.want {
			t.Fatalf("row %s: survived=%v, want %v", tc.id, got, tc.want)
		}
	}
}

// persistDelivery must stamp received_at itself, in UTC, rather than
// relying on DEFAULT CURRENT_TIMESTAMP -- one write path, one format.
func TestPersistDelivery_StampsUTCReceivedAt(t *testing.T) {
	rt := runtimeutil.NewRuntime(t, &env.Config{})
	ctx := context.Background()

	before := time.Now().UTC().Add(-2 * time.Second)
	inserted, err := persistDelivery(ctx, rt, "del-fmt", "ping", []byte("{}"))
	if err != nil {
		t.Fatalf("persistDelivery: %v", err)
	}
	if !inserted {
		t.Fatal("first insert should report inserted=true")
	}

	// `received_at || ''` yields a computed TEXT column with no
	// decltype, so the driver hands back the stored text verbatim
	// instead of parsing it into time.Time first.
	var raw string
	var ts time.Time
	if err := rt.DB.DB().QueryRowContext(ctx,
		`SELECT received_at || '', received_at FROM webhook_deliveries WHERE id = ?`, "del-fmt").Scan(&raw, &ts); err != nil {
		t.Fatalf("select received_at: %v", err)
	}
	if !strings.HasSuffix(raw, "+0000 UTC") {
		t.Fatalf("received_at %q is not the driver's UTC text format", raw)
	}
	if ts.Before(before) || ts.After(time.Now().UTC().Add(2*time.Second)) {
		t.Fatalf("received_at %v not within the insert window", ts)
	}
}
