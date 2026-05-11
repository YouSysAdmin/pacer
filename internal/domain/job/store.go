// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package job

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/yousysadmin/pacer/internal/core/dbutil"
	jobmodel "github.com/yousysadmin/pacer/internal/models/job"
)

// Store is the SQLite-backed persistence for the job queue + lifecycle.
type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

var errNotImpl = errors.New("job.Store: not implemented yet")

// ErrJobMissing is returned by lifecycle writes (StampSpawn, ...) when
// the targeted job row no longer exists. Callers treat this as a
// roll-back signal: the orchestrator unwinds an in-flight spawn.
var ErrJobMissing = errors.New("job: row missing")

func (s *Store) Put(ctx context.Context, j *jobmodel.Job) error {
	if j.QueuedAt.IsZero() {
		j.QueuedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
        INSERT INTO jobs (
            id, gh_job_id, gh_run_id, installation_id, repo_full_name,
            project_id, pool_id, status, instance_id, callback_token_hash,
            queued_at, claimed_at, started_at, completed_at,
            failure_stage, failure_message, attempts, next_retry_at,
            sender_login, payload
        ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
    `,
		j.ID, j.GHJobID, j.GHRunID, j.InstallationID, j.RepoFullName,
		j.ProjectID, dbutil.NullStr(j.PoolID), string(j.Status),
		dbutil.NullStr(j.InstanceID), dbutil.NullStr(j.CallbackTokenHash),
		j.QueuedAt, dbutil.NullTime(j.ClaimedAt), dbutil.NullTime(j.StartedAt), dbutil.NullTime(j.CompletedAt),
		dbutil.NullStr(j.FailureStage), dbutil.NullStr(j.FailureMessage),
		j.Attempts, dbutil.NullTime(j.NextRetryAt), j.SenderLogin, string(j.Payload),
	)
	return err
}

func (s *Store) Get(ctx context.Context, id string) (*jobmodel.Job, error) {
	return s.scanOne(ctx, "WHERE id = ?", id)
}

func (s *Store) GetByGHJobID(ctx context.Context, ghJobID int64) (*jobmodel.Job, error) {
	return s.scanOne(ctx, "WHERE gh_job_id = ?", ghJobID)
}

// Claim atomically pops the oldest queued job whose POOL still has
// capacity (active jobs < pool.max_concurrent_runners) AND whose
// PROJECT-wide ceiling (when set non-zero) is not yet reached, then
// flips it to claimed.
// MaxOpenConns(1) on the connection pool serializes this with all other writes;
// explicit transaction kept for clarity and ease of porting to postgres.
func (s *Store) Claim(ctx context.Context, now time.Time) (*jobmodel.Job, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	var id string
	row := tx.QueryRowContext(ctx, `
        SELECT j.id
        FROM jobs j
        JOIN pools po ON po.id = j.pool_id AND po.disabled = 0
        JOIN projects pr ON pr.id = j.project_id AND pr.disabled = 0
        WHERE j.status = 'queued'
          AND (j.next_retry_at IS NULL OR j.next_retry_at <= ?)
          AND (
              SELECT COUNT(*) FROM jobs jc
              WHERE jc.pool_id = j.pool_id
                AND jc.status IN ('claimed','starting','running')
          ) < po.max_concurrent_runners
          AND (
              pr.max_concurrent_runners = 0
              OR (
                  SELECT COUNT(*) FROM jobs jc
                  WHERE jc.project_id = j.project_id
                    AND jc.status IN ('claimed','starting','running')
              ) < pr.max_concurrent_runners
          )
        ORDER BY j.queued_at ASC
        LIMIT 1
    `, now)
	if err := row.Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE jobs SET status = 'claimed', claimed_at = ? WHERE id = ?`,
		now, id); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	// The deferred Rollback returns sql.ErrTxDone after a successful
	// Commit; that's expected and the standard library treats it as a
	// no-op, so the blank-identifier discard is intentional.
	return s.Get(ctx, id)
}

func (s *Store) StampSpawn(ctx context.Context, id, instanceID, callbackTokenHash string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE jobs SET instance_id = ?, callback_token_hash = ? WHERE id = ?`,
		instanceID, callbackTokenHash, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrJobMissing
	}
	return nil
}

func (s *Store) MarkRunning(ctx context.Context, id, instanceID string, now time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE jobs SET status = 'running', instance_id = COALESCE(?, instance_id), started_at = ? WHERE id = ?`,
		dbutil.NullStr(instanceID), now, id)
	return err
}

// UpdatePayload overwrites jobs.payload with the latest workflow_job
// webhook body. The in_progress and completed actions carry a richer
// blob than the queued action (steps[] is populated, started_at /
// completed_at are stamped, sender drift is reflected) and the modal
// is more useful when that data is on hand. Job-row state columns
// (status, claimed_at, etc.) are authoritative; the payload is purely
// for display.
func (s *Store) UpdatePayload(ctx context.Context, id string, payload []byte) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE jobs SET payload = ? WHERE id = ?`,
		string(payload), id)
	return err
}

// UpdatePayloadIfRunning is the conditional variant used by the
// detail-endpoint inline refresh path. The WHERE clause closes the
// race where a `completed` webhook lands between the handler's status
// check and our UPDATE -- without it, an in-flight refresh could
// regress an authoritative final payload back to a partial running
// snapshot. Zero rows affected is silent success: it just means the
// job moved out of running state and our stale view should be
// discarded.
func (s *Store) UpdatePayloadIfRunning(ctx context.Context, id string, payload []byte) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE jobs SET payload = ? WHERE id = ? AND status = 'running'`,
		string(payload), id)
	return err
}

// costSubquery is the SQL fragment that derives a job's estimated
// USD cost at terminal-state time -- price_per_hour * elapsed-hours
// since the instance launched, looked up via the job's instance_id.
// julianday() returns days; * 24 converts to hours.
// NULL-safe: a missing instance row, or a NULL price, leaves the result NULL.
//
// Both timestamps are sliced to YYYY-MM-DDTHH:MM:SS (chars 1..19)
// before being fed to julianday(). modernc/sqlite stores time.Time
// as RFC3339Nano (9-digit fractional seconds plus trailing Z), and
// some sqlite builds return NULL from julianday() when the
// fractional precision is wider than the .SSS shape they accept.
// Truncating loses up to one second of precision -- negligible for
// hour-billed compute.
const costSubquery = `(
    SELECT i.price_per_hour * (julianday(substr(?, 1, 19)) - julianday(substr(i.launched_at, 1, 19))) * 24
    FROM instances i
    WHERE i.id = jobs.instance_id
)`

func (s *Store) MarkCompleted(ctx context.Context, id string, now time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE jobs SET status = 'completed', completed_at = ?,
		    estimated_cost_usd = `+costSubquery+`
		 WHERE id = ?`,
		now, now, id)
	return err
}

func (s *Store) MarkFailed(ctx context.Context, id, stage, message string, now time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE jobs SET status = 'failed', failure_stage = ?, failure_message = ?,
		    completed_at = ?,
		    estimated_cost_usd = `+costSubquery+`
		 WHERE id = ?`,
		stage, message, now, now, id)
	return err
}

// MarkCancelled is the user-initiated-cancellation variant: GitHub
// reports workflow_job.completed with conclusion=cancelled when the
// user aborts a run from the UI / API. Lifecycle-wise identical to
// MarkFailed (terminal state, cost rollup runs) -- the separate
// status lets the UI distinguish "the job blew up" from "the user
// cancelled it" without parsing failure_message.
func (s *Store) MarkCancelled(ctx context.Context, id, stage, message string, now time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE jobs SET status = 'cancelled', failure_stage = ?, failure_message = ?,
		    completed_at = ?,
		    estimated_cost_usd = `+costSubquery+`
		 WHERE id = ?`,
		stage, message, now, now, id)
	return err
}

// MarkFailedWithLog is the runner-bootstrap-error variant: the
// spawned instance posts the captured stdout/stderr to
// /api/runner/error and the handler routes here so the operator can
// see what blew up before the runner ever connected.
// Same lifecycle as MarkFailed otherwise.
func (s *Store) MarkFailedWithLog(ctx context.Context, id, stage, message, log string, now time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE jobs SET status = 'failed', failure_stage = ?, failure_message = ?,
		    failure_log = ?, completed_at = ?,
		    estimated_cost_usd = `+costSubquery+`
		 WHERE id = ?`,
		stage, message, log, now, now, id)
	return err
}

func (s *Store) MarkReaped(ctx context.Context, id string, now time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE jobs SET status = 'reaped', completed_at = ?,
		    estimated_cost_usd = `+costSubquery+`
		 WHERE id = ?`,
		now, now, id)
	return err
}

func (s *Store) ReclaimStale(ctx context.Context, cutoff time.Time) (int, error) {
	return 0, errNotImpl
}

// Reschedule flips a claimed job back to 'queued', bumps its
// attempts counter, and gates it from being re-claimed until
// nextRetryAt. Used by the orchestrator when a capacity-class
// failure exhausts every (instance_type, subnet) combo -- the job
// stays in flight rather than failing, and the next tick after
// nextRetryAt picks it up.
func (s *Store) Reschedule(ctx context.Context, id string, attempts int, nextRetryAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `
        UPDATE jobs
        SET status = 'queued',
            instance_id = NULL,
            callback_token_hash = NULL,
            claimed_at = NULL,
            attempts = ?,
            next_retry_at = ?
        WHERE id = ?
    `, attempts, nextRetryAt, id)
	return err
}

// List returns jobs filtered by Status / ProjectID / PoolID / Repo,
// newest first, capped at f.Limit (default 100, max 500).
func (s *Store) List(ctx context.Context, f jobmodel.ListFilter) ([]*jobmodel.Job, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}

	q := jobSelect
	args := []any{}
	conds := []string{}
	if f.Status != "" {
		conds = append(conds, "status = ?")
		args = append(args, string(f.Status))
	}
	if f.ProjectID != "" {
		conds = append(conds, "project_id = ?")
		args = append(args, f.ProjectID)
	}
	if f.PoolID != "" {
		conds = append(conds, "pool_id = ?")
		args = append(args, f.PoolID)
	}
	if f.Repo != "" {
		conds = append(conds, "repo_full_name = ?")
		args = append(args, f.Repo)
	}
	if len(conds) > 0 {
		q += " WHERE " + strings.Join(conds, " AND ")
	}
	q += " ORDER BY queued_at DESC LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*jobmodel.Job
	for rows.Next() {
		j, err := scanJobRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

const jobSelect = `
SELECT id, gh_job_id, gh_run_id, installation_id, repo_full_name,
       project_id, pool_id, status, instance_id, callback_token_hash,
       queued_at, claimed_at, started_at, completed_at,
       failure_stage, failure_message, failure_log, estimated_cost_usd,
       attempts, next_retry_at, sender_login, payload
FROM jobs`

func (s *Store) scanOne(ctx context.Context, where string, args ...any) (*jobmodel.Job, error) {
	row := s.db.QueryRowContext(ctx, jobSelect+" "+where, args...)
	j, err := scanJobRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return j, err
}

func scanJobRow(r interface{ Scan(...any) error }) (*jobmodel.Job, error) {
	var j jobmodel.Job
	var poolID, instID, callbackHash, failStage, failMsg, failLog sql.NullString
	var claimedAt, startedAt, completedAt, nextRetryAt sql.NullTime
	var costUSD sql.NullFloat64
	var status, senderLogin, payload string
	if err := r.Scan(&j.ID, &j.GHJobID, &j.GHRunID, &j.InstallationID,
		&j.RepoFullName, &j.ProjectID, &poolID,
		&status, &instID, &callbackHash,
		&j.QueuedAt, &claimedAt, &startedAt, &completedAt,
		&failStage, &failMsg, &failLog, &costUSD,
		&j.Attempts, &nextRetryAt, &senderLogin, &payload); err != nil {
		return nil, err
	}
	j.SenderLogin = senderLogin
	j.PoolID = poolID.String
	j.Status = jobmodel.Status(status)
	j.InstanceID = instID.String
	j.CallbackTokenHash = callbackHash.String
	j.FailureStage = failStage.String
	j.FailureMessage = failMsg.String
	j.FailureLog = failLog.String
	if claimedAt.Valid {
		j.ClaimedAt = new(claimedAt.Time)
	}
	if startedAt.Valid {
		j.StartedAt = new(startedAt.Time)
	}
	if completedAt.Valid {
		j.CompletedAt = new(completedAt.Time)
	}
	if costUSD.Valid {
		j.EstimatedCostUSD = new(costUSD.Float64)
	}
	if nextRetryAt.Valid {
		j.NextRetryAt = new(nextRetryAt.Time)
	}
	j.Payload = []byte(payload)
	return &j, nil
}

// FinalizeCost recomputes a job's estimated_cost_usd from its
// instance's full billable window (terminated_at - launched_at), so
// the runner-shutdown tail that runs AFTER the workflow_job webhook
// fires is included. Called from every UpdateState(terminated|reaped)
// path; the earlier estimate stamped at MarkCompleted/MarkFailed/
// MarkReaped is overwritten in place.
//
// Both timestamps are sliced to char 1..19 for the same reason as
// costSubquery / StatusTimeseries: modernc/sqlite writes 9-digit
// nanos that some sqlite builds reject from julianday().
//
// No-op when the instance row is missing, its terminated_at is NULL
// (instance not yet marked terminated), or its price is NULL (the
// pricing fetch failed at spawn time -- nothing to multiply by).
func (s *Store) FinalizeCost(ctx context.Context, instanceID string) error {
	_, err := s.db.ExecContext(ctx, `
        UPDATE jobs
        SET estimated_cost_usd = (
            SELECT i.price_per_hour
                 * (julianday(substr(i.terminated_at, 1, 19))
                  - julianday(substr(i.launched_at,  1, 19))) * 24
            FROM instances i
            WHERE i.id = jobs.instance_id
              AND i.terminated_at  IS NOT NULL
              AND i.price_per_hour IS NOT NULL
        )
        WHERE jobs.instance_id = ?
    `, instanceID)
	return err
}

// StatusTimeseries groups terminal-state jobs by UTC calendar day.
// Rows outside [from, to) by completed_at are filtered out.
//
// We slice the date prefix with substr() rather than calling
// sqlite's date(): modernc/sqlite stores time.Time as RFC3339Nano
// (9-digit fractional seconds), and some sqlite builds return NULL
// from date() / strftime() when the fractional component is wider
// than the .SSS shape they accept. substr(x, 1, 10) is format-
// agnostic and works regardless of fractional precision because
// every RFC3339 string starts with YYYY-MM-DD.
//
// Empty response when no jobs completed in the window -- the chart
// renderer pads zero-bars for the missing days if it wants a
// continuous axis.
func (s *Store) StatusTimeseries(ctx context.Context, from, to time.Time) ([]jobmodel.DayBucket, error) {
	rows, err := s.db.QueryContext(ctx, `
        SELECT substr(j.completed_at, 1, 10)                           AS day,
               SUM(CASE WHEN j.status = 'completed' THEN 1 ELSE 0 END) AS completed,
               SUM(CASE WHEN j.status = 'failed'    THEN 1 ELSE 0 END) AS failed,
               SUM(CASE WHEN j.status = 'cancelled' THEN 1 ELSE 0 END) AS cancelled,
               SUM(CASE WHEN j.status = 'reaped'    THEN 1 ELSE 0 END) AS reaped
        FROM jobs j
        WHERE j.status IN ('completed','failed','cancelled','reaped')
          AND j.completed_at IS NOT NULL
          AND j.completed_at >= ?
          AND j.completed_at <  ?
        GROUP BY day
        ORDER BY day ASC
    `, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []jobmodel.DayBucket
	for rows.Next() {
		var b jobmodel.DayBucket
		if err := rows.Scan(&b.Day, &b.Completed, &b.Failed, &b.Cancelled, &b.Reaped); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}
