// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package instance

import (
	"context"
	"database/sql"
	"errors"
	"strings"
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
       launched_at, registered_at, terminated_at, last_seen_at,
       gh_runner_id
FROM instances`

func (s *Store) Put(ctx context.Context, i *instancemodel.Instance) error {
	if i.LaunchedAt.IsZero() {
		i.LaunchedAt = time.Now()
	}
	i.LaunchedAt = i.LaunchedAt.UTC()
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
// nullable-float helper - if a second caller needs it, lift then.
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

// Touch bumps last_seen_at on the given instance rows. The reaper
// calls this on every alive instance AWS confirmed in its DescribeInstances
// response so the UI can distinguish "row is current" from "row hasn't
// been reconciled in N minutes - something is wrong with the reaper."
//
// Empty ids list is a no-op (saves a needless transaction on idle
// sweeps). State is intentionally not touched - this is purely a
// heartbeat. Rows whose state would actually change ride through
// UpdateState or markLost, both of which already stamp last_seen_at.
func (s *Store) Touch(ctx context.Context, ids []string, now time.Time) error {
	now = dbutil.UTC(now)
	if len(ids) == 0 {
		return nil
	}
	// Batch UPDATE via a single statement with an IN clause. sqlite
	// caps placeholders at 999. The reaper only sweeps alive
	// instances, which are bounded by total concurrent runners
	// across all pools - effectively under a few hundred for any
	// realistic install. If that ever grows, chunk here.
	q := `UPDATE instances SET last_seen_at = ? WHERE id IN (?` +
		strings.Repeat(",?", len(ids)-1) + `)`
	args := make([]any, 0, len(ids)+1)
	args = append(args, now)
	for _, id := range ids {
		args = append(args, id)
	}
	_, err := s.db.ExecContext(ctx, q, args...)
	return err
}

func (s *Store) UpdateState(ctx context.Context, id string, state instancemodel.State, now time.Time) error {
	now = dbutil.UTC(now)
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

func (s *Store) StampRegistration(ctx context.Context, id, instanceType, az string, ghRunnerID int64, now time.Time) error {
	now = dbutil.UTC(now)
	_, err := s.db.ExecContext(ctx, `
        UPDATE instances
        SET instance_type = ?, az = ?, state = 'running',
            registered_at = ?, last_seen_at = ?, gh_runner_id = ?
        WHERE id = ?
    `, instanceType, az, now, now, nullInt64Zero(ghRunnerID), id)
	return err
}

// nullInt64Zero maps a 0 sentinel to SQL NULL so the gh_runner_id
// column stays NULL when the JIT-config response didn't include a
// runner id (older GHES, mocked dev runs). Avoids a magic 0 row that
// would later be passed to GitHub's DeleteRunner endpoint.
func nullInt64Zero(v int64) sql.NullInt64 {
	if v == 0 {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: v, Valid: true}
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
	cutoff = dbutil.UTC(cutoff)
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
	var ghRunnerID sql.NullInt64
	if err := r.Scan(&i.ID, &i.JobID, &i.ProjectID, &poolID,
		&iType, &az, &state, &spot,
		&pricePerHour, &priceModel,
		&i.LaunchedAt, &registeredAt, &terminatedAt, &lastSeenAt,
		&ghRunnerID); err != nil {
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
	if ghRunnerID.Valid {
		i.GHRunnerID = ghRunnerID.Int64
	}
	return &i, nil
}
