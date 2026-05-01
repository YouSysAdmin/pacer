// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package instance

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/yousysadmin/pacer/internal/core/dbutil"
	instancemodel "github.com/yousysadmin/pacer/internal/models/instance"
)

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

const instanceSelect = `
SELECT id, job_id, project_id, pool_id, instance_type, az, state, spot,
       price_per_hour, price_model,
       launched_at, registered_at, terminated_at, last_seen_at
FROM instances`

func (s *Store) Put(ctx context.Context, i *instancemodel.Instance) error {
	if i.LaunchedAt.IsZero() {
		i.LaunchedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
        INSERT INTO instances (
            id, job_id, project_id, pool_id, instance_type, az, state, spot,
            price_per_hour, price_model,
            launched_at, registered_at, terminated_at, last_seen_at
        ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
        ON CONFLICT(id) DO UPDATE SET
            job_id         = excluded.job_id,
            project_id     = excluded.project_id,
            pool_id        = excluded.pool_id,
            instance_type  = excluded.instance_type,
            az             = excluded.az,
            state          = excluded.state,
            spot           = excluded.spot,
            price_per_hour = excluded.price_per_hour,
            price_model    = excluded.price_model,
            registered_at  = excluded.registered_at,
            terminated_at  = excluded.terminated_at,
            last_seen_at   = excluded.last_seen_at
    `,
		i.ID, i.JobID, i.ProjectID, dbutil.NullStr(i.PoolID),
		dbutil.NullStr(i.InstanceType), dbutil.NullStr(i.AZ),
		string(i.State), dbutil.BoolInt(i.Spot),
		nullFloat(i.PricePerHour), dbutil.NullStr(i.PriceModel),
		i.LaunchedAt, dbutil.NullTime(i.RegisteredAt), dbutil.NullTime(i.TerminatedAt), dbutil.NullTime(i.LastSeenAt),
	)
	return err
}

// nullFloat maps *float64 to sql.NullFloat64 for INSERTs.  We can't
// hoist this into dbutil yet because dbutil currently has no
// nullable-float helper -- if a second caller needs it, lift then.
func nullFloat(p *float64) sql.NullFloat64 {
	if p == nil {
		return sql.NullFloat64{}
	}
	return sql.NullFloat64{Float64: *p, Valid: true}
}

func (s *Store) Get(ctx context.Context, id string) (*instancemodel.Instance, error) {
	row := s.db.QueryRowContext(ctx, instanceSelect+` WHERE id = ?`, id)
	i, err := scanInstance(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return i, err
}

func (s *Store) UpdateState(ctx context.Context, id string, state instancemodel.State, now time.Time) error {
	if state == instancemodel.StateTerminated || state == instancemodel.StateReaped {
		_, err := s.db.ExecContext(ctx,
			`UPDATE instances SET state = ?, terminated_at = ?, last_seen_at = ? WHERE id = ?`,
			string(state), now, now, id)
		return err
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE instances SET state = ?, last_seen_at = ? WHERE id = ?`,
		string(state), now, id)
	return err
}

func (s *Store) StampRegistration(ctx context.Context, id, instanceType, az string, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `
        UPDATE instances
        SET instance_type = ?, az = ?, state = 'running', registered_at = ?, last_seen_at = ?
        WHERE id = ?
    `, instanceType, az, now, now, id)
	return err
}

func (s *Store) ListAlive(ctx context.Context) ([]*instancemodel.Instance, error) {
	rows, err := s.db.QueryContext(ctx,
		instanceSelect+` WHERE state IN ('starting','running') ORDER BY launched_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*instancemodel.Instance
	for rows.Next() {
		i, err := scanInstance(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

func (s *Store) ListStuck(ctx context.Context, cutoff time.Time) ([]*instancemodel.Instance, error) {
	rows, err := s.db.QueryContext(ctx,
		instanceSelect+` WHERE state IN ('starting','running') AND launched_at < ? ORDER BY launched_at ASC`,
		cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*instancemodel.Instance
	for rows.Next() {
		i, err := scanInstance(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

func scanInstance(r interface{ Scan(...any) error }) (*instancemodel.Instance, error) {
	var i instancemodel.Instance
	var poolID, iType, az, priceModel sql.NullString
	var state string
	var spot int
	var pricePerHour sql.NullFloat64
	var registeredAt, terminatedAt, lastSeenAt sql.NullTime
	if err := r.Scan(&i.ID, &i.JobID, &i.ProjectID, &poolID,
		&iType, &az, &state, &spot,
		&pricePerHour, &priceModel,
		&i.LaunchedAt, &registeredAt, &terminatedAt, &lastSeenAt); err != nil {
		return nil, err
	}
	i.PoolID = poolID.String
	i.InstanceType = iType.String
	i.AZ = az.String
	i.State = instancemodel.State(state)
	i.Spot = spot != 0
	if pricePerHour.Valid {
		i.PricePerHour = new(pricePerHour.Float64)
	}
	i.PriceModel = priceModel.String
	if registeredAt.Valid {
		i.RegisteredAt = new(registeredAt.Time)
	}
	if terminatedAt.Valid {
		i.TerminatedAt = new(terminatedAt.Time)
	}
	if lastSeenAt.Valid {
		i.LastSeenAt = new(lastSeenAt.Time)
	}
	return &i, nil
}
