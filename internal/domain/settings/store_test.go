// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package settings

import (
	"testing"
	"time"

	settingsmodel "github.com/yousysadmin/pacer/internal/models/settings"
	"github.com/yousysadmin/pacer/internal/testutil"
)

// TestPutGet_RoundTrip is the canonical "did the SQL serializer eat
// our timestamp" test. We almost shipped a startup-crash regression
// because the TEXT column type silently changed how modernc/sqlite
// serializes time.Time. A round-trip would have caught that locally.
func TestPutGet_RoundTrip(t *testing.T) {
	s := NewStore(testutil.OpenTestDB(t))
	ctx := t.Context()

	if err := s.Put(ctx, settingsmodel.KeyBootstrapAPIToken, "deadbeef"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := s.Get(ctx, settingsmodel.KeyBootstrapAPIToken)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatalf("Get returned nil for key just inserted")
	}
	if got.Key != settingsmodel.KeyBootstrapAPIToken {
		t.Errorf("Key: got %q, want %q", got.Key, settingsmodel.KeyBootstrapAPIToken)
	}
	if got.Value != "deadbeef" {
		t.Errorf("Value: got %q, want %q", got.Value, "deadbeef")
	}
	if got.UpdatedAt.IsZero() {
		t.Error("UpdatedAt zero: TEXT/TIMESTAMP scan failed silently")
	}
	if dur := time.Since(got.UpdatedAt); dur < 0 || dur > time.Minute {
		t.Errorf("UpdatedAt drift: %v ago (round-trip should be < 1s)", dur)
	}
}

func TestGet_Missing(t *testing.T) {
	s := NewStore(testutil.OpenTestDB(t))
	got, err := s.Get(t.Context(), "nonexistent_key")
	if err != nil {
		t.Fatalf("Get on missing row: want (nil, nil), got err=%v", err)
	}
	if got != nil {
		t.Fatalf("Get on missing row: want nil, got %+v", got)
	}
}

func TestPut_Upsert(t *testing.T) {
	s := NewStore(testutil.OpenTestDB(t))
	ctx := t.Context()

	if err := s.Put(ctx, settingsmodel.KeyBootstrapAPIToken, "first"); err != nil {
		t.Fatalf("first Put: %v", err)
	}
	first, err := s.Get(ctx, settingsmodel.KeyBootstrapAPIToken)
	if err != nil {
		t.Fatalf("first Get: %v", err)
	}
	// Sleep a touch so UpdatedAt actually advances at second-precision
	// even on fast machines.
	time.Sleep(10 * time.Millisecond)

	if err := s.Put(ctx, settingsmodel.KeyBootstrapAPIToken, "second"); err != nil {
		t.Fatalf("second Put: %v", err)
	}
	second, err := s.Get(ctx, settingsmodel.KeyBootstrapAPIToken)
	if err != nil {
		t.Fatalf("second Get: %v", err)
	}
	if second.Value != "second" {
		t.Errorf("upsert lost: got %q, want %q", second.Value, "second")
	}
	if !second.UpdatedAt.After(first.UpdatedAt) {
		t.Errorf("UpdatedAt didn't advance: first=%v second=%v", first.UpdatedAt, second.UpdatedAt)
	}
}

// TestParseTimestamp_LegacyFormats locks in the read-side tolerance
// for rows written by older code paths. Regression: an earlier version
// passed time.Time to modernc/sqlite's TEXT column, which serialized
// via Go's default String() format (`2006-01-02 15:04:05 -0700 MST`).
// Fresh installs use RFC3339Nano, but upgraded deployments still have
// String-formatted rows - those must keep parsing.
func TestParseTimestamp_LegacyFormats(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"RFC3339Nano", "2026-05-11T20:31:19.977779457Z"},
		{"RFC3339 second precision", "2026-05-11T20:31:19Z"},
		{"Go String() nano + UTC tz name", "2026-05-11 20:31:19.977779457 +0000 UTC"},
		{"Go String() second precision", "2026-05-11 20:31:19 +0000 UTC"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseTimestamp(tc.in)
			if err != nil {
				t.Fatalf("parseTimestamp(%q): %v", tc.in, err)
			}
			if got.IsZero() {
				t.Fatalf("parseTimestamp(%q) returned zero time", tc.in)
			}
		})
	}
}

func TestParseTimestamp_Unrecognized(t *testing.T) {
	if _, err := parseTimestamp("not a timestamp"); err == nil {
		t.Fatal("parseTimestamp on garbage: want error, got nil")
	}
}
