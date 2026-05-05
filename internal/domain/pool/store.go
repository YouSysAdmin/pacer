// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package pool

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/yousysadmin/pacer/internal/core/dbutil"
	poolmodel "github.com/yousysadmin/pacer/internal/models/pool"
)

// Store is the SQLite-backed persistence for pools.
type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

const poolSelect = `
SELECT id, project_id, name, is_default, priority,
       ami_id, instance_types, subnet_ids, security_group_ids,
       iam_instance_profile, root_volume_gb, max_runtime_minutes,
       max_concurrent_runners, spot, spawn_method, allocation_strategy,
       extra_labels, tags, runner_version, runner_user, user_data_extra,
       launch_template_id, launch_template_version, disabled,
       created_at, updated_at
FROM pools`

func (s *Store) Get(ctx context.Context, id string) (*poolmodel.Pool, error) {
	return s.scanOne(ctx, "WHERE id = ?", id)
}

func (s *Store) GetDefault(ctx context.Context, projectID string) (*poolmodel.Pool, error) {
	return s.scanOne(ctx, "WHERE project_id = ? AND is_default = 1", projectID)
}

func (s *Store) Put(ctx context.Context, p *poolmodel.Pool) error {
	if p.CreatedAt.IsZero() {
		p.CreatedAt = time.Now().UTC()
	}
	p.UpdatedAt = time.Now().UTC()

	instTypes := dbutil.MustJSON(p.InstanceTypes)
	subnets := dbutil.MustJSON(p.SubnetIDs)
	sgs := dbutil.MustJSON(p.SecurityGroupIDs)
	tags := dbutil.MustJSON(p.Tags)
	var extraLabels []byte
	if p.ExtraLabels == nil {
		extraLabels = []byte("[]")
	} else {
		extraLabels = dbutil.MustJSON(p.ExtraLabels)
	}

	var ltVer any
	if p.LaunchTemplateVersion != 0 {
		ltVer = p.LaunchTemplateVersion
	}

	spawnMethod := p.SpawnMethod
	if spawnMethod == "" {
		spawnMethod = "fleet"
	}
	allocStrategy := p.AllocationStrategy
	if allocStrategy == "" {
		allocStrategy = "cost"
	}

	_, err := s.db.ExecContext(ctx, `
        INSERT INTO pools (
            id, project_id, name, is_default, priority,
            ami_id, instance_types, subnet_ids, security_group_ids,
            iam_instance_profile, root_volume_gb, max_runtime_minutes,
            max_concurrent_runners, spot, spawn_method, allocation_strategy,
            extra_labels, tags, runner_version, runner_user, user_data_extra,
            launch_template_id, launch_template_version, disabled,
            created_at, updated_at
        ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
        ON CONFLICT(id) DO UPDATE SET
            project_id = excluded.project_id,
            name = excluded.name,
            is_default = excluded.is_default,
            priority = excluded.priority,
            ami_id = excluded.ami_id,
            instance_types = excluded.instance_types,
            subnet_ids = excluded.subnet_ids,
            security_group_ids = excluded.security_group_ids,
            iam_instance_profile = excluded.iam_instance_profile,
            root_volume_gb = excluded.root_volume_gb,
            max_runtime_minutes = excluded.max_runtime_minutes,
            max_concurrent_runners = excluded.max_concurrent_runners,
            spot = excluded.spot,
            spawn_method = excluded.spawn_method,
            allocation_strategy = excluded.allocation_strategy,
            extra_labels = excluded.extra_labels,
            tags = excluded.tags,
            runner_version = excluded.runner_version,
            runner_user = excluded.runner_user,
            user_data_extra = excluded.user_data_extra,
            launch_template_id = excluded.launch_template_id,
            launch_template_version = excluded.launch_template_version,
            disabled = excluded.disabled,
            updated_at = excluded.updated_at
    `,
		p.ID, p.ProjectID, p.Name, dbutil.BoolInt(p.IsDefault), p.Priority,
		p.AMIID, string(instTypes), string(subnets), string(sgs),
		p.IAMInstanceProfile, p.RootVolumeGB, p.MaxRuntimeMinutes,
		p.MaxConcurrentRunners, dbutil.BoolInt(p.Spot), spawnMethod, allocStrategy,
		string(extraLabels), string(tags), p.RunnerVersion, p.RunnerUser, dbutil.NullStr(p.UserDataExtra),
		dbutil.NullStr(p.LaunchTemplateID), ltVer, dbutil.BoolInt(p.Disabled),
		p.CreatedAt, p.UpdatedAt,
	)
	return err
}

func (s *Store) Delete(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM pools WHERE id = ?`, id)
	return err
}

func (s *Store) List(ctx context.Context) ([]*poolmodel.Pool, error) {
	return s.queryMany(ctx, ``, `ORDER BY project_id, priority, name`)
}

func (s *Store) ListByProject(ctx context.Context, projectID string) ([]*poolmodel.Pool, error) {
	return s.queryMany(ctx, `WHERE project_id = ?`, `ORDER BY priority, name`, projectID)
}

// ConcurrentRunnerCount drives the per-pool cap.  Counts jobs in
// active states: claimed, starting, running.
func (s *Store) ConcurrentRunnerCount(ctx context.Context, poolID string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM jobs WHERE pool_id = ? AND status IN ('claimed','starting','running')`,
		poolID).Scan(&n)
	return n, err
}

func (s *Store) scanOne(ctx context.Context, where string, args ...any) (*poolmodel.Pool, error) {
	row := s.db.QueryRowContext(ctx, poolSelect+" "+where, args...)
	p, err := scanPool(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return p, err
}

func (s *Store) queryMany(ctx context.Context, where, order string, args ...any) ([]*poolmodel.Pool, error) {
	q := poolSelect
	if where != "" {
		q += " " + where
	}
	if order != "" {
		q += " " + order
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*poolmodel.Pool
	for rows.Next() {
		p, err := scanPool(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func scanPool(r interface{ Scan(...any) error }) (*poolmodel.Pool, error) {
	var p poolmodel.Pool
	var instTypes, subnets, sgs, tags, spawnMethod, allocStrategy, extraLabels, runnerVersion, runnerUser string
	var userDataExtra, ltID sql.NullString
	var ltVer sql.NullInt64
	var isDefault, spot, disabled int
	if err := r.Scan(&p.ID, &p.ProjectID, &p.Name, &isDefault, &p.Priority,
		&p.AMIID, &instTypes, &subnets, &sgs,
		&p.IAMInstanceProfile, &p.RootVolumeGB, &p.MaxRuntimeMinutes,
		&p.MaxConcurrentRunners, &spot, &spawnMethod, &allocStrategy,
		&extraLabels, &tags, &runnerVersion, &runnerUser, &userDataExtra,
		&ltID, &ltVer, &disabled, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return nil, err
	}
	p.SpawnMethod = spawnMethod
	p.AllocationStrategy = allocStrategy
	p.RunnerVersion = runnerVersion
	p.RunnerUser = runnerUser
	dbutil.MustUnmarshalJSON(instTypes, &p.InstanceTypes)
	dbutil.MustUnmarshalJSON(subnets, &p.SubnetIDs)
	dbutil.MustUnmarshalJSON(sgs, &p.SecurityGroupIDs)
	dbutil.MustUnmarshalJSON(extraLabels, &p.ExtraLabels)
	dbutil.MustUnmarshalJSON(tags, &p.Tags)
	p.UserDataExtra = userDataExtra.String
	p.LaunchTemplateID = ltID.String
	p.LaunchTemplateVersion = int(ltVer.Int64)
	p.IsDefault = isDefault != 0
	p.Spot = spot != 0
	p.Disabled = disabled != 0
	return &p, nil
}
