// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package stats

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/yousysadmin/pacer/internal/core/dbutil"
	"time"

	statsmodel "github.com/yousysadmin/pacer/internal/models/stats"
)

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// Rollup runs a single GROUP BY query over the terminal-state jobs
// in [from, to], returning one row per project / pool / repo plus
// the totals for the same window.
// julianday() arithmetic gives runner-minutes from launched_at to completed_at.
// Rows with no instance row (spawn-failed jobs) contribute zero minutes.
//
// projectID narrows the whole query to one project (the console's
// scope selector); empty means every project. It applies to the
// TOTALS as well as the buckets, which is the point - a scoped page
// showing a global total would read as that project's spend.
//
// Both timestamps are sliced to chars 1..19 (YYYY-MM-DDTHH:MM:SS)
// before julianday() because modernc/sqlite writes time.Time with
// 9-digit nanosecond precision. Some sqlite builds return NULL
// from julianday() on the wider format. Sub-second truncation is
// negligible for cost / runtime rollups.
func (s *Store) Rollup(ctx context.Context, by statsmodel.GroupBy, from, to time.Time, projectID string) (statsmodel.Totals, []statsmodel.Bucket, error) {
	from = dbutil.UTC(from)
	to = dbutil.UTC(to)
	keyCol, nameJoin, err := groupExpr(by)
	if err != nil {
		return statsmodel.Totals{}, nil, err
	}

	scope, args := projectScope(projectID, from, to)
	q := fmt.Sprintf(`
        SELECT
            %s AS k,
            %s AS name,
            COUNT(*) AS jobs,
            COALESCE(SUM(
                CASE
                    WHEN i.launched_at IS NOT NULL AND j.completed_at IS NOT NULL
                    THEN (julianday(substr(j.completed_at, 1, 19)) - julianday(substr(i.launched_at, 1, 19))) * 1440.0
                    ELSE 0
                END
            ), 0) AS runner_minutes,
            COALESCE(SUM(j.estimated_cost_usd), 0) AS est_cost,
            SUM(CASE WHEN j.estimated_cost_usd IS NULL THEN 1 ELSE 0 END) AS no_cost
        FROM jobs j
        LEFT JOIN instances i ON i.id = j.instance_id
        %s
        WHERE j.status IN ('completed','failed','cancelled','reaped')
          AND j.completed_at IS NOT NULL
          AND j.completed_at >= ?
          AND j.completed_at <  ?%s
        GROUP BY k
        ORDER BY est_cost DESC, jobs DESC
    `, keyCol, nameCol(by), nameJoin, scope)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return statsmodel.Totals{}, nil, err
	}
	defer rows.Close()

	var (
		totals  statsmodel.Totals
		buckets []statsmodel.Bucket
	)
	for rows.Next() {
		var b statsmodel.Bucket
		if err := rows.Scan(&b.Key, &b.Name, &b.Jobs, &b.RunnerMinutes, &b.EstCostUSD, &b.JobsWithoutCost); err != nil {
			return statsmodel.Totals{}, nil, err
		}
		buckets = append(buckets, b)
		totals.Jobs += b.Jobs
		totals.RunnerMinutes += b.RunnerMinutes
		totals.EstCostUSD += b.EstCostUSD
		totals.JobsWithoutCost += b.JobsWithoutCost
	}
	return totals, buckets, rows.Err()
}

// groupExpr returns (key column expression, join clause that exposes
// the human name).
// We need a LEFT JOIN against projects / pools so
// deleted rows still show up under their stamped key with a fallback
// name = the key itself.
func groupExpr(by statsmodel.GroupBy) (keyCol, nameJoin string, err error) {
	switch by {
	case statsmodel.ByProject:
		return "j.project_id", "LEFT JOIN projects p ON p.id = j.project_id", nil
	case statsmodel.ByPool:
		// Pool names aren't unique across projects - qualify with the
		// owning project so identically named pools don't collapse into
		// one bucket in the UI.
		return "j.pool_id",
			"LEFT JOIN pools po ON po.id = j.pool_id LEFT JOIN projects p ON p.id = po.project_id",
			nil
	case statsmodel.ByRepo:
		return "j.repo_full_name", "", nil
	default:
		return "", "", fmt.Errorf("unknown group_by %q", by)
	}
}

// TopUsers ranks senders by terminal-state job count in [from, to),
// limit-clamped at the call site. projectID narrows to one project;
// empty means every project. Rows where sender_login = "" are
// excluded (jobs predating the column, or webhook payloads with no
// sender block). They would all collapse into a single misleading
// "anonymous" bucket otherwise.
func (s *Store) TopUsers(ctx context.Context, from, to time.Time, limit int, projectID string) ([]statsmodel.UserBucket, error) {
	from = dbutil.UTC(from)
	to = dbutil.UTC(to)
	scope, args := projectScope(projectID, from, to)
	// LIMIT binds last, after the window and the optional project.
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, `
        SELECT
            j.sender_login AS login,
            COUNT(*) AS jobs,
            COALESCE(SUM(
                CASE
                    WHEN i.launched_at IS NOT NULL AND j.completed_at IS NOT NULL
                    THEN (julianday(substr(j.completed_at, 1, 19)) - julianday(substr(i.launched_at, 1, 19))) * 1440.0
                    ELSE 0
                END
            ), 0) AS runner_minutes,
            COALESCE(SUM(j.estimated_cost_usd), 0) AS est_cost
        FROM jobs j
        LEFT JOIN instances i ON i.id = j.instance_id
        WHERE j.sender_login != ''
          AND j.status IN ('completed','failed','cancelled','reaped')
          AND j.completed_at IS NOT NULL
          AND j.completed_at >= ?
          AND j.completed_at <  ?`+scope+`
        GROUP BY j.sender_login
        ORDER BY jobs DESC, est_cost DESC
        LIMIT ?
    `, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []statsmodel.UserBucket
	for rows.Next() {
		var b statsmodel.UserBucket
		if err := rows.Scan(&b.Login, &b.Jobs, &b.RunnerMinutes, &b.EstCostUSD); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// projectScope appends the console's scope selector to a window-bound
// query and returns the bind args in the order the SQL expects them
// (from, to, then the project when one was given).
//
// Returning both halves together is what keeps them in step: the two
// queries here differ in what follows the WHERE block, and a clause
// added in one place with its arg appended in another is how a query
// ends up binding the project id to the LIMIT.
func projectScope(projectID string, from, to time.Time) (string, []any) {
	args := []any{from, to}
	if projectID == "" {
		return "", args
	}
	return "\n          AND j.project_id = ?", append(args, projectID)
}

func nameCol(by statsmodel.GroupBy) string {
	switch by {
	case statsmodel.ByProject:
		return "COALESCE(p.name, j.project_id)"
	case statsmodel.ByPool:
		// Display as <project>/<pool>. Fall back to the raw IDs when
		// either parent row was deleted after the job ran.
		return "COALESCE(p.name, j.project_id) || '/' || COALESCE(po.name, j.pool_id)"
	case statsmodel.ByRepo:
		return "j.repo_full_name"
	}
	return "j.id" // unreachable, groupExpr would have rejected
}
