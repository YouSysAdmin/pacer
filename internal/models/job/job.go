// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// Package job models the lifecycle of a single GitHub workflow_job
// from "queued" webhook through completion or reap.
package job

import "time"

type Status string

const (
	StatusQueued    Status = "queued"
	StatusClaimed   Status = "claimed"   // RunInstances issued, instance not yet self-registered
	StatusStarting  Status = "starting"  // self-reg landed, waiting for runner to pick up the job
	StatusRunning   Status = "running"   // GitHub reports the job in_progress
	StatusCompleted Status = "completed" // workflow_job.completed received
	StatusFailed    Status = "failed"    // RunInstances failed, callback failed, or reap before run
	StatusCancelled Status = "cancelled" // workflow_job.completed with conclusion=cancelled (user-initiated)
	StatusReaped    Status = "reaped"    // sweeper terminated a stuck instance after timeout
)

type Job struct {
	ID                string     `json:"id"`
	GHJobID           int64      `json:"gh_job_id"`
	GHRunID           int64      `json:"gh_run_id"`
	InstallationID    int64      `json:"installation_id"`
	RepoFullName      string     `json:"repo_full_name"`
	ProjectID         string     `json:"project_id"`
	PoolID            string     `json:"pool_id"`
	Status            Status     `json:"status"`
	InstanceID        string     `json:"instance_id,omitempty"`
	CallbackTokenHash string     `json:"-"` // sha256 of the callback token. HMAC payload is (job_id, exp_unix). Raw token never stored.
	QueuedAt          time.Time  `json:"queued_at"`
	ClaimedAt         *time.Time `json:"claimed_at,omitempty"`
	StartedAt         *time.Time `json:"started_at,omitempty"`
	CompletedAt       *time.Time `json:"completed_at,omitempty"`
	FailureStage      string     `json:"failure_stage,omitempty"`
	FailureMessage    string     `json:"failure_message,omitempty"`
	// FailureLog is the user-data bootstrap log captured from a
	// spawned instance when /api/runner/error fires.
	// Empty for jobs that completed normally or failed before any instance
	// came up.
	// Multi-line. The UI renders it in a code block.
	FailureLog string `json:"failure_log,omitempty"`
	// EstimatedCostUSD is filled at completion / reap / fail by
	// multiplying the instance's stamped price_per_hour by the
	// job's elapsed time.
	// Best-effort: nil when the instance had no stamped price, or when the
	// job never claimed an instance.
	EstimatedCostUSD *float64 `json:"estimated_cost_usd,omitempty"`
	// Attempts counts orchestrator spawn attempts. Capacity-class
	// errors (InsufficientInstanceCapacity etc.) reschedule the job
	// rather than failing it. Each reschedule bumps Attempts. The
	// final hard-failure path also bumps it.
	Attempts int `json:"attempts"`
	// NextRetryAt gates Job.Claim: rows with NextRetryAt > now are
	// invisible. Set by Reschedule along with the backoff. NULL = ready.
	NextRetryAt *time.Time `json:"next_retry_at,omitempty"`
	// SenderLogin is the GitHub user that triggered the workflow run
	// (push author, PR opener, manual rerun). Empty string when
	// missing from the payload or for jobs that pre-date the column.
	SenderLogin string `json:"sender_login,omitempty"`
	Payload     []byte `json:"-"` // raw GH workflow_job JSON
}

type ListFilter struct {
	Status    Status
	ProjectID string
	PoolID    string
	Repo      string
	Limit     int
	Offset    int
}

// DayBucket is one row in the daily success/failed timeseries used
// by the Overview chart. Day is a UTC YYYY-MM-DD string (sqlite's
// date() truncation). Counts are split by terminal status so the
// caller can render success vs failure stacks.
type DayBucket struct {
	Day       string `json:"day"`
	Completed int64  `json:"completed"`
	Failed    int64  `json:"failed"`
	Cancelled int64  `json:"cancelled"`
	Reaped    int64  `json:"reaped"`
}
