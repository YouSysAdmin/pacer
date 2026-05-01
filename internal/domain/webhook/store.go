// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package webhook

import (
	"context"
	"database/sql"
	"time"
)

// Store owns the webhook_deliveries table. The webhook handler writes
// rows directly via persistDelivery (see endpoint.go) because the hot
// path needs RowsAffected from the same statement; this store exists
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
// receive path cares about is GitHub's redelivery window (~15 min);
// keep the cutoff comfortably above that so a slow retry can't slip
// through after we've forgotten the delivery id.
func (s *Store) DeleteOlderThan(ctx context.Context, cutoff time.Time) (int, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM webhook_deliveries WHERE received_at < ?`, cutoff)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}
