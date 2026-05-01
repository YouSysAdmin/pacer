// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package project

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/yousysadmin/pacer/internal/core/dbutil"
	projectmodel "github.com/yousysadmin/pacer/internal/models/project"
)

// Store is the SQLite-backed persistence for projects.
// Project no longer owns EC2 launch settings (those moved to pools); it keeps
// the logical name + project-wide concurrency ceiling + cascading
// tags + disabled flag.
type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

const projectSelect = `
SELECT id, name, max_concurrent_runners, tags, scope, org_name, runner_group_id,
       disabled, created_at, updated_at
FROM projects`

func (s *Store) Get(ctx context.Context, id string) (*projectmodel.Project, error) {
	return s.scanOne(ctx, "WHERE id = ?", id)
}

func (s *Store) GetByName(ctx context.Context, name string) (*projectmodel.Project, error) {
	return s.scanOne(ctx, "WHERE name = ?", name)
}

// GetByOrgName returns the org-scoped project bound to the given GitHub
// org login (case-insensitive). Returns (nil, nil) when no such project
// exists -- callers fall back to repo-binding lookup.
func (s *Store) GetByOrgName(ctx context.Context, orgName string) (*projectmodel.Project, error) {
	return s.scanOne(ctx,
		"WHERE scope = 'org' AND lower(org_name) = lower(?)", orgName)
}

func (s *Store) Put(ctx context.Context, p *projectmodel.Project) error {
	if p.CreatedAt.IsZero() {
		p.CreatedAt = time.Now().UTC()
	}
	p.UpdatedAt = time.Now().UTC()
	tags, _ := json.Marshal(p.Tags)

	scope := p.Scope
	if scope == "" {
		scope = projectmodel.ScopeRepo
	}

	_, err := s.db.ExecContext(ctx, `
        INSERT INTO projects (id, name, max_concurrent_runners, tags, scope, org_name, runner_group_id, disabled, created_at, updated_at)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
        ON CONFLICT(id) DO UPDATE SET
            name = excluded.name,
            max_concurrent_runners = excluded.max_concurrent_runners,
            tags = excluded.tags,
            scope = excluded.scope,
            org_name = excluded.org_name,
            runner_group_id = excluded.runner_group_id,
            disabled = excluded.disabled,
            updated_at = excluded.updated_at
    `, p.ID, p.Name, p.MaxConcurrentRunners, string(tags),
		scope, p.OrgName, p.RunnerGroupID,
		dbutil.BoolInt(p.Disabled), p.CreatedAt, p.UpdatedAt)
	return err
}

func (s *Store) Delete(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM projects WHERE id = ?`, id)
	return err
}

func (s *Store) List(ctx context.Context) ([]*projectmodel.Project, error) {
	rows, err := s.db.QueryContext(ctx, projectSelect+` ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*projectmodel.Project
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) ConcurrentRunnerCount(ctx context.Context, projectID string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM jobs WHERE project_id = ? AND status IN ('claimed','starting','running')`,
		projectID).Scan(&n)
	return n, err
}

func (s *Store) scanOne(ctx context.Context, where string, args ...any) (*projectmodel.Project, error) {
	row := s.db.QueryRowContext(ctx, projectSelect+" "+where, args...)
	p, err := scanProject(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return p, err
}

func scanProject(r interface{ Scan(...any) error }) (*projectmodel.Project, error) {
	var p projectmodel.Project
	var tagsJSON, scope, orgName string
	var runnerGroupID, disabled int
	if err := r.Scan(&p.ID, &p.Name, &p.MaxConcurrentRunners, &tagsJSON,
		&scope, &orgName, &runnerGroupID,
		&disabled, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(tagsJSON), &p.Tags)
	p.Scope = scope
	p.OrgName = orgName
	p.RunnerGroupID = runnerGroupID
	p.Disabled = disabled != 0
	return &p, nil
}
