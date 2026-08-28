// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package repo

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/yousysadmin/pacer/internal/core/dbutil"
	repomodel "github.com/yousysadmin/pacer/internal/models/repo"
)

// Store is the SQLite-backed persistence for repo bindings.
type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

const repoSelect = `
SELECT full_name, project_id, max_concurrent_runners, tags, created_at
FROM repos`

func (s *Store) Get(ctx context.Context, fullName string) (*repomodel.Repo, error) {
	row := s.db.QueryRowContext(ctx, repoSelect+` WHERE full_name = ?`, fullName)
	r, err := scanRepo(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return r, err
}

func (s *Store) Put(ctx context.Context, r *repomodel.Repo) error {
	if r.CreatedAt.IsZero() {
		r.CreatedAt = time.Now()
	}
	r.CreatedAt = r.CreatedAt.UTC()
	var maxConc any
	if r.MaxConcurrentRunners != nil {
		maxConc = *r.MaxConcurrentRunners
	}
	tags := dbutil.MustJSON(r.Tags)
	_, err := s.db.ExecContext(ctx, `
        INSERT INTO repos (full_name, project_id, max_concurrent_runners, tags, created_at)
        VALUES (?, ?, ?, ?, ?)
        ON CONFLICT(full_name) DO UPDATE SET
            project_id = excluded.project_id,
            max_concurrent_runners = excluded.max_concurrent_runners,
            tags = excluded.tags
    `, r.FullName, r.ProjectID, maxConc, string(tags), r.CreatedAt)
	return err
}

func (s *Store) Delete(ctx context.Context, fullName string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM repos WHERE full_name = ?`, fullName)
	return err
}

func (s *Store) List(ctx context.Context) ([]*repomodel.Repo, error) {
	return s.queryRepos(ctx, "")
}

func (s *Store) ListByProject(ctx context.Context, projectID string) ([]*repomodel.Repo, error) {
	return s.queryRepos(ctx, "WHERE project_id = ?", projectID)
}

func (s *Store) queryRepos(ctx context.Context, where string, args ...any) ([]*repomodel.Repo, error) {
	q := repoSelect
	if where != "" {
		q += " " + where
	}
	q += ` ORDER BY full_name`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*repomodel.Repo
	for rows.Next() {
		r, err := scanRepo(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) ConcurrentRunnerCount(ctx context.Context, fullName string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM jobs WHERE repo_full_name = ? AND status IN ('claimed','starting','running')`,
		fullName).Scan(&n)
	return n, err
}

func scanRepo(r interface{ Scan(...any) error }) (*repomodel.Repo, error) {
	var rp repomodel.Repo
	var maxConc sql.NullInt64
	var tagsJSON string
	if err := r.Scan(&rp.FullName, &rp.ProjectID, &maxConc, &tagsJSON, &rp.CreatedAt); err != nil {
		return nil, err
	}
	if maxConc.Valid {
		rp.MaxConcurrentRunners = new(int(maxConc.Int64))
	}
	dbutil.MustUnmarshalJSON(tagsJSON, &rp.Tags)
	return &rp, nil
}
