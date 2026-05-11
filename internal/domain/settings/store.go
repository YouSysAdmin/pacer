// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package settings

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	settingsmodel "github.com/yousysadmin/pacer/internal/models/settings"
)

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// Get returns the setting with the given key, or (nil, nil) if no row
// exists.
//
// updated_at is declared TEXT in the migration (the settings table
// doesn't share the jobs/instances "TIMESTAMP" column type), so
// modernc/sqlite's auto-conversion to time.Time doesn't kick in. We
// scan as string and parse with time.RFC3339Nano (the format Put
// writes through).
func (s *Store) Get(ctx context.Context, key string) (*settingsmodel.Setting, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT key, value, updated_at FROM settings WHERE key = ?`, key)
	var (
		out     settingsmodel.Setting
		updated string
	)
	if err := row.Scan(&out.Key, &out.Value, &updated); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	t, err := parseTimestamp(updated)
	if err != nil {
		return nil, fmt.Errorf("parse updated_at %q: %w", updated, err)
	}
	out.UpdatedAt = t
	return &out, nil
}

// parseTimestamp tolerates three layouts:
//   - RFC3339Nano (what Put writes today)
//   - RFC3339 second-precision (older Put paths)
//   - Go's default time.Time.String() format, which is what modernc/sqlite
//     used to serialize through the TEXT column before Put started
//     formatting explicitly. Existing rows on upgraded deployments are
//     in this layout, so we keep it for one-time read compatibility.
func parseTimestamp(s string) (time.Time, error) {
	for _, layout := range []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.999999999 -0700 MST",
		"2006-01-02 15:04:05 -0700 MST",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognized timestamp format")
}

// Put upserts the setting. updated_at is stamped server-side so the
// UI can show "last rotated" for the bootstrap token.
//
// We format the timestamp explicitly to RFC3339Nano rather than
// passing time.Time and letting the driver pick. The settings
// column is declared TEXT (not TIMESTAMP) -- with TEXT, modernc/sqlite
// serializes time.Time via Go's default String() shape
// (`2006-01-02 15:04:05.000000000 -0700 MST`), which is unparseable
// by stdlib's RFC3339 readers. Explicit RFC3339Nano keeps writes
// and reads symmetric.
func (s *Store) Put(ctx context.Context, key, value string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx, `
        INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
        ON CONFLICT(key) DO UPDATE SET
            value      = excluded.value,
            updated_at = excluded.updated_at
    `, key, value, now)
	return err
}
