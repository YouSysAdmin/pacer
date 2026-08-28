// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package sqlite

import (
	"path/filepath"
	"testing"
)

func TestOpen_AppliesMigrationsAndIndexes(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	err = db.DB().QueryRowContext(t.Context(),
		`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_jobs_instance_id'`).Scan(&n)
	if err != nil || n != 1 {
		t.Fatalf("idx_jobs_instance_id missing: n=%d err=%v", n, err)
	}
}

func TestOpen_BadPath(t *testing.T) {
	if _, err := Open(filepath.Join(t.TempDir(), "nope", "x", "m.db")); err == nil {
		t.Fatal("expected error for unwritable path")
	}
}
