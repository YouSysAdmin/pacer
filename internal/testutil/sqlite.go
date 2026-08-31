// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// Package testutil holds shared helpers for unit tests.
//
// OpenTestDB returns a freshly-migrated SQLite handle backed by the
// same goose migrations the production binary uses, so store tests
// run against the real schema.
// The returned *sql.DB lives on a single connection (MaxOpenConns=1) so an in-memory database stays
// coherent across queries - matching the production posture.
package testutil

import (
	"database/sql"
	"path/filepath"
	"sync"
	"testing"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"

	"github.com/yousysadmin/pacer/internal/database/sqlite/migrations"
)

var gooseOnce sync.Once

// OpenTestDB returns a migrated SQLite *sql.DB and registers cleanup
// to close it at test end. Each call gets its own database file in
// the test's TempDir, so tests are cheap to create and isolated.
func OpenTestDB(t *testing.T) *sql.DB {
	t.Helper()
	return OpenTestDBAt(t, filepath.Join(t.TempDir(), "test.db"))
}

// OpenTestDBAt is OpenTestDB with a caller-chosen file path, for
// callers that need to report the real path (runtimeutil.Path).
func OpenTestDBAt(t *testing.T, path string) *sql.DB {
	t.Helper()
	dsn := path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(on)&_pragma=synchronous(NORMAL)"

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		t.Fatalf("ping sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)

	// goose setters mutate package globals, so run them once per
	// process to stay race-free under t.Parallel.
	var gooseErr error
	gooseOnce.Do(func() {
		goose.SetBaseFS(migrations.FS)
		gooseErr = goose.SetDialect("sqlite3")
		goose.SetLogger(goose.NopLogger())
	})
	if gooseErr != nil {
		_ = db.Close()
		t.Fatalf("goose set dialect: %v", gooseErr)
	}
	if err := goose.Up(db, "."); err != nil {
		_ = db.Close()
		t.Fatalf("goose up: %v", err)
	}

	t.Cleanup(func() { _ = db.Close() })
	return db
}
