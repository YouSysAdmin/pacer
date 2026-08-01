// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// Package store aggregates the per-domain persistence interfaces
// behind a single struct.
//
// Every method takes a context.Context first arg (rather than a
// fiber-bound RequestContext) so the interface stays usable from
// orchestrator goroutines and CLI commands as well as HTTP handlers.
package store

import (
	"context"
	"time"

	"github.com/yousysadmin/pacer/internal/models/audit"
	"github.com/yousysadmin/pacer/internal/models/instance"
	"github.com/yousysadmin/pacer/internal/models/job"
	"github.com/yousysadmin/pacer/internal/models/pool"
	"github.com/yousysadmin/pacer/internal/models/project"
	"github.com/yousysadmin/pacer/internal/models/repo"
	settingsmodel "github.com/yousysadmin/pacer/internal/models/settings"
	"github.com/yousysadmin/pacer/internal/models/stats"
	"github.com/yousysadmin/pacer/internal/models/user"
)

// Store bundles every per-domain store.
// Handlers depend on this struct, never on a concrete backend.
type Store struct {
	User     UserStore
	Project  ProjectStore
	Pool     PoolStore
	Repo     RepoStore
	Job      JobStore
	Instance InstanceStore
	Audit    AuditStore
	Stats    StatsStore
	Webhook  WebhookStore
	Settings SettingsStore
}

type UserStore interface {
	Get(ctx context.Context, email string) (*user.User, error)
	GetByID(ctx context.Context, id string) (*user.User, error)
	GetByOIDCSubject(ctx context.Context, sub string) (*user.User, error)
	Put(ctx context.Context, u *user.User) error
	Delete(ctx context.Context, email string) error
	List(ctx context.Context) ([]*user.User, error)
	Count(ctx context.Context) (int, error)
	TouchLastLogin(ctx context.Context, email string) error
}

type ProjectStore interface {
	Get(ctx context.Context, id string) (*project.Project, error)
	GetByName(ctx context.Context, name string) (*project.Project, error)
	// GetByOrgName resolves an org-scoped project by GitHub org login.
	// Used by webhook routing when a repo isn't bound to a project.
	GetByOrgName(ctx context.Context, orgName string) (*project.Project, error)
	Put(ctx context.Context, p *project.Project) error
	Delete(ctx context.Context, id string) error
	List(ctx context.Context) ([]*project.Project, error)
	// ConcurrentRunnerCount totals active jobs across ALL pools in
	// the project.
	// Used to enforce the project-wide ceiling (Project.MaxConcurrentRunners).
	// Zero ceiling means skip the check at the orchestrator's claim-time gate.
	ConcurrentRunnerCount(ctx context.Context, projectID string) (int, error)
}

// PoolStore is the persistence contract for pools - each pool owns
// one EC2 launch template and one set of spawn parameters.
type PoolStore interface {
	Get(ctx context.Context, id string) (*pool.Pool, error)
	Put(ctx context.Context, p *pool.Pool) error
	Delete(ctx context.Context, id string) error
	List(ctx context.Context) ([]*pool.Pool, error)
	ListByProject(ctx context.Context, projectID string) ([]*pool.Pool, error)
	GetDefault(ctx context.Context, projectID string) (*pool.Pool, error)
	// ConcurrentRunnerCount is the per-pool active-job counter the
	// orchestrator's claim-time gate consults against
	// Pool.MaxConcurrentRunners.
	ConcurrentRunnerCount(ctx context.Context, poolID string) (int, error)
	// ActiveJobCount additionally includes queued jobs. Used by the
	// delete gate: a queued job whose pool_id gets NULLed can never be
	// claimed (Claim joins pools), so deletion must be blocked while
	// any non-terminal job references the pool.
	ActiveJobCount(ctx context.Context, poolID string) (int, error)
}

type RepoStore interface {
	Get(ctx context.Context, fullName string) (*repo.Repo, error)
	Put(ctx context.Context, r *repo.Repo) error
	Delete(ctx context.Context, fullName string) error
	List(ctx context.Context) ([]*repo.Repo, error)
	ListByProject(ctx context.Context, projectID string) ([]*repo.Repo, error)
	ConcurrentRunnerCount(ctx context.Context, fullName string) (int, error)
}

// JobStore is the persistence contract for the job queue + lifecycle.
// Claim is the hot atomic-dequeue path the orchestrator hits each
// tick; the lifecycle Mark* methods are write-once transitions.
type JobStore interface {
	Put(ctx context.Context, j *job.Job) error
	Get(ctx context.Context, id string) (*job.Job, error)
	GetByGHJobID(ctx context.Context, ghJobID int64) (*job.Job, error)
	// Claim atomically pops the oldest queued job whose pool is not
	// at capacity AND (if set) whose project-wide ceiling is not
	// reached, stamps claim metadata, and returns the updated row.
	// Returns (nil, nil) when no eligible job is available - caller
	// backs off + polls.
	Claim(ctx context.Context, now time.Time) (*job.Job, error)
	// StampSpawn records the spawned EC2 instance ID, the sha256
	// hash of the runner self-registration callback token, and the
	// raw token cached for the bootstrap endpoint to return.
	// ConsumeBootstrap clears the raw column on first read so the
	// secret has at most single-use exposure.
	StampSpawn(ctx context.Context, id, instanceID, callbackTokenHash, bootstrapToken string) error
	// ConsumeBootstrap atomically reads-and-clears the cached
	// bootstrap token for the job stamped with this instance_id.
	// Returns ErrBootstrapUnavailable when no valid row matches
	// (already consumed, stale, missing, or not in claimed state).
	ConsumeBootstrap(ctx context.Context, instanceID string, ttl time.Duration, now time.Time) (token, jobID string, err error)
	// UpdatePayload overwrites jobs.payload with a newer webhook body.
	// Used so the in_progress / completed workflow_job payloads -- which
	// carry the populated steps[] array -- replace the queued-action
	// snapshot stamped at enqueue time. Best-effort; callers log and
	// continue on error rather than fail the lifecycle transition.
	UpdatePayload(ctx context.Context, id string, payload []byte) error
	// UpdatePayloadIfRunning is the race-safe variant used by the
	// detail-endpoint inline-refresh path. The conditional WHERE
	// prevents the on-demand GitHub fetch from regressing a final
	// `completed` webhook payload that landed between the handler's
	// status check and the UPDATE.
	UpdatePayloadIfRunning(ctx context.Context, id string, payload []byte) error
	MarkRunning(ctx context.Context, id, instanceID string, now time.Time) error
	MarkCompleted(ctx context.Context, id string, now time.Time) error
	MarkFailed(ctx context.Context, id, stage, message string, now time.Time) error
	// MarkFailedWithLog is the bootstrap-error variant -- attaches
	// the captured user-data log so operators can see what blew up
	// before the runner ever registered.
	MarkFailedWithLog(ctx context.Context, id, stage, message, log string, now time.Time) error
	// MarkCancelled is the user-initiated-cancellation variant of
	// MarkFailed. GitHub reports conclusion=cancelled when the user
	// aborts a run; the distinct status lets the UI separate "broke"
	// from "user cancelled" without text parsing.
	MarkCancelled(ctx context.Context, id, stage, message string, now time.Time) error
	MarkReaped(ctx context.Context, id string, now time.Time) error
	// Reschedule sends a job back to 'queued' with a backoff and bumped
	// attempts counter. Used when capacity-class spawn failures should
	// retry rather than fail the job.
	Reschedule(ctx context.Context, id string, attempts int, nextRetryAt time.Time) error
	ReclaimStale(ctx context.Context, cutoff time.Time) (int, error)
	List(ctx context.Context, f job.ListFilter) ([]*job.Job, error)
	// Count returns the number of rows matching f, ignoring Limit
	// and Offset. Powers the "X of Y" pager on the jobs page.
	Count(ctx context.Context, f job.ListFilter) (int, error)
	// StatusTimeseries returns one bucket per UTC calendar day with
	// terminal-status counts (completed / failed / reaped). Powers
	// the Overview page's success-vs-failed chart.
	StatusTimeseries(ctx context.Context, from, to time.Time) ([]job.DayBucket, error)
	// FinalizeCost recomputes a job's estimated_cost_usd after its
	// instance has been marked terminated/reaped. The Mark{Completed,
	// Failed,Reaped} stamps an early estimate using the webhook /
	// reaper firing time; this call replaces it with the
	// actual billable window (i.terminated_at - i.launched_at) so
	// the runner-shutdown tail isn't excluded. No-op when the
	// instance row is missing or its terminated_at / price is NULL.
	FinalizeCost(ctx context.Context, instanceID string) error
}

type InstanceStore interface {
	Put(ctx context.Context, i *instance.Instance) error
	Get(ctx context.Context, id string) (*instance.Instance, error)
	UpdateState(ctx context.Context, id string, state instance.State, now time.Time) error
	StampRegistration(ctx context.Context, id, instanceType, az string, ghRunnerID int64, now time.Time) error
	// Touch is the reaper heartbeat: bumps last_seen_at on every
	// instance the reaper just confirmed via DescribeInstances.
	// Lets the UI distinguish "row is current" from "row hasn't been
	// reconciled in N minutes -- something is wrong."
	Touch(ctx context.Context, ids []string, now time.Time) error
	ListAlive(ctx context.Context) ([]*instance.Instance, error)
	ListStuck(ctx context.Context, cutoff time.Time) ([]*instance.Instance, error)
}

// AuditStore is the persistence contract for the audit log.
// Entries are immutable - Put is the only writer; pruning goes through
// DeleteOlderThan.
type AuditStore interface {
	Put(ctx context.Context, e *audit.Entry) error
	List(ctx context.Context, f audit.ListFilter) ([]*audit.Entry, error)
	// Count returns the total number of rows matching f (ignoring
	// Limit + Offset) so paginated UIs can show "page X of N".
	Count(ctx context.Context, f audit.ListFilter) (int, error)
	DeleteOlderThan(ctx context.Context, cutoff time.Time) (int, error)
}

// StatsStore is the read-only cost + activity rollup over completed jobs.
// Best-effort: cost is launch-time price * elapsed time; jobs
// whose pricing fetch failed at spawn contribute to JobsWithoutCost.
type StatsStore interface {
	Rollup(ctx context.Context, by stats.GroupBy, from, to time.Time) (stats.Totals, []stats.Bucket, error)
	// TopUsers returns the top-N GitHub senders by job count over
	// terminal-state jobs in [from, to). Used by the stats page's
	// "who runs the most CI" panel.
	TopUsers(ctx context.Context, from, to time.Time, limit int) ([]stats.UserBucket, error)
}

// WebhookStore is the lifecycle side of the webhook_deliveries table
// (the receive path writes inline from the handler for atomicity with
// the dedup check). Pruning runs from a serve-bound goroutine so the
// table doesn't grow without bound.
type WebhookStore interface {
	DeleteOlderThan(ctx context.Context, cutoff time.Time) (int, error)
}

// SettingsStore is the generic key-value store for pacer-managed
// config that needs to live outside YAML (auto-generated secrets,
// UI-rotatable values). Today: just the bootstrap API token. The
// store is intentionally minimal -- callers interpret the value.
type SettingsStore interface {
	Get(ctx context.Context, key string) (*settingsmodel.Setting, error)
	Put(ctx context.Context, key, value string) error
}
