// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package webhook

import (
	"context"
	"database/sql"
	"github.com/yousysadmin/pacer/internal/core/dbutil"
	"time"
)

// Store owns the webhook_deliveries table. The webhook handler writes
// rows directly via persistDelivery (see endpoint.go) because the hot
// path needs RowsAffected from the same statement. This store exists
// for the other side of the lifecycle -- pruning -- so the table
// doesn't grow forever on a busy install.
type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// DeleteOlderThan removes webhook_deliveries rows received before the
// cutoff. Returns the number of rows deleted. The dedup-window the
// receive path cares about is GitHub's redelivery window (~15 min).
// Keep the cutoff comfortably above that so a slow retry can't slip
// through after we've forgotten the delivery id.
func (s *Store) DeleteOlderThan(ctx context.Context, cutoff time.Time) (int, error) {
	cutoff = dbutil.UTC(cutoff)
	// The comparison is textual. The table holds two shapes, both
	// space-separated UTC: 'YYYY-MM-DD HH:MM:SS' from the column's
	// DEFAULT CURRENT_TIMESTAMP (rows written before persistDelivery
	// supplied the column) and the driver's
	// 'YYYY-MM-DD HH:MM:SS.fffffffff +0000 UTC'. Prefix ordering makes
	// text comparison chronologically correct across both -- but only
	// while the bound cutoff is UTC too, so normalize here rather than
	// trusting every caller.
	res, err := s.db.ExecContext(ctx, `DELETE FROM webhook_deliveries WHERE received_at < ?`, cutoff.UTC())
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}
