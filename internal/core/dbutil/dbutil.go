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

import (
	"encoding/json"
	"fmt"
	"time"
)

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
	return t.UTC()
}

// UTC normalizes a bind parameter before it reaches modernc/sqlite.
// The driver writes UTC values as RFC3339 with a trailing Z and
// anything else as time.Time.String(), so a single non-UTC write
// silently breaks every textual range comparison on that column.
func UTC(t time.Time) time.Time {
	return t.UTC()
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

// MustJSON marshals v as JSON and panics on error. Stores serialize
// Go-native maps and slices (string-keyed maps, []string, etc.) which
// json.Marshal cannot fail on - a panic here means a programmer bug
// (e.g. someone slipped a chan or func into a model field) and we'd
// rather crash loudly than write a corrupt empty-string column.
//
// MustJSON returns []byte so callers can pass it straight to driver
// args without an extra type conversion.
func MustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("dbutil.MustJSON: %v (input: %#v)", err, v))
	}
	return b
}

// MustUnmarshalJSON is the read-side complement to MustJSON: parse
// store-emitted JSON into v. An error here means the row in the DB
// was tampered with or written by a foreign tool, both of which are
// hard failures rather than recoverable conditions. Empty input
// behaves as a no-op so callers can safely round-trip through stores
// that default empty TEXT columns to "".
func MustUnmarshalJSON(raw string, v any) {
	if raw == "" {
		return
	}
	if err := json.Unmarshal([]byte(raw), v); err != nil {
		panic(fmt.Sprintf("dbutil.MustUnmarshalJSON: %v (raw: %q)", err, raw))
	}
}
