// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// Package database declares the abstraction every persistence backend
// implements.
// The interface stays narrow - concrete backends layer
// richer typed methods on top.
// Per-domain stores read the *sql.DB through this interface
// so they don't bind to a specific dialect.
package database

import "database/sql"

// Database is the minimum surface every backend exposes.
type Database interface {
	// Close releases backend resources.
	// Idempotent.
	Close() error

	// Path returns the on-disk file path for SQLite, or the DSN for
	// network-backed engines (Postgres / MySQL).
	Path() string

	// Engine returns the dialect identifier ("sqlite", "postgres",
	// "mysql").
	// Used by goose at runtime and by code paths that need
	// dialect-specific SQL.
	Engine() string

	// DB returns the underlying *sql.DB so domain stores can issue
	// queries directly.
	// Connection pool tuning is the backend's responsibility -
	// callers should NOT mutate pool settings.
	DB() *sql.DB
}
