// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// Package dbutil holds tiny helpers shared by every domain's
// SQL-backed store.go.
// Kept backend-agnostic - no sqlite-only assumptions live here
// so a future postgres / mysql impl can use them too where appropriate.
//
// At this time, Pacer only supports SQLite databases,
// I plan to add Postgres and MySQL (maybe Mongo too) in the future.
package dbutil

import "time"

// NullStr returns nil for empty strings so they land as SQL NULL,
// otherwise returns the string itself.
// Use as the arg for nullable TEXT columns.
func NullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// NullTime returns nil when t is nil, otherwise returns the
// dereferenced value.
// Use for nullable TIMESTAMP / DATETIME columns.
func NullTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return *t
}

// BoolInt encodes a Go bool as 0/1 - required for SQLite which has
// no native BOOLEAN.
// Postgres / MySQL impls can pass bools through directly.
func BoolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
