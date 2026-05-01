// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// Package sqlite is the SQLite backend.
// Single-file embedded store,pure-Go driver (modernc.org/sqlite - no CGO), goose-driven schema.
//
// SQLite is single-writer; we cap MaxOpenConns at 1 to avoid
// "database is locked" under contention.
// WAL keeps concurrent reads fast.
// Postgres / MySQL backends will live in sibling packages.
package sqlite

import (
	"database/sql"
	"fmt"
	"log/slog"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"

	"github.com/yousysadmin/pacer/internal/database/sqlite/migrations"
)

// SQLite implements database.Database backed by a single .db file.
type SQLite struct {
	db   *sql.DB
	path string
}

// Open opens (or creates) the database at path, sets pragmas, runs
// pending goose migrations, and returns the handle.
// Migrations are embedded into the binary at compile time via the migrations package.
func Open(path string) (*SQLite, error) {
	dsn := fmt.Sprintf(
		"%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(on)&_pragma=synchronous(NORMAL)",
		path,
	)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %q: %w", path, err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite %q: %w", path, err)
	}
	db.SetMaxOpenConns(1)

	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("sqlite3"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("goose set dialect: %w", err)
	}
	if err := goose.Up(db, "."); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("goose up: %w", err)
	}

	slog.Info("sqlite open", "path", path)
	return &SQLite{db: db, path: path}, nil
}

func (s *SQLite) Close() error   { return s.db.Close() }
func (s *SQLite) Path() string   { return s.path }
func (s *SQLite) Engine() string { return "sqlite" }
func (s *SQLite) DB() *sql.DB    { return s.db }
