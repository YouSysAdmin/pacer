// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// Package stats holds the read-only result models used by the
// /api/stats endpoint.
// All numbers are best-effort estimates: cost is launch-time price * elapsed time,
// ignores EBS / data transfer, and uses a single launch-time spot snapshot
// rather than tracking price drift mid-run.
package stats

import (
	"time"

	"github.com/yousysadmin/pacer/internal/models/job"
)

// GroupBy is the rollup axis for /api/stats.
type GroupBy string

const (
	ByProject GroupBy = "project"
	ByPool    GroupBy = "pool"
	ByRepo    GroupBy = "repo"
)

// Bucket is one rolled-up row.
// Key + Name describe the grouping dimension;
// the metrics summarize jobs whose completed_at falls inside the requested window.
type Bucket struct {
	Key             string  `json:"key"`               // project_id / pool_id / repo full_name
	Name            string  `json:"name"`              // human label for the key
	Jobs            int64   `json:"jobs"`              // count of completed/reaped/failed jobs in window
	RunnerMinutes   float64 `json:"runner_minutes"`    // sum of (completed_at - launched_at) in minutes
	EstCostUSD      float64 `json:"est_cost_usd"`      // sum of estimated_cost_usd, best-effort
	JobsWithoutCost int64   `json:"jobs_without_cost"` // jobs in window with NULL cost (pricing fetch failed at spawn)
}

// Totals is the unfiltered top-line for the same window.
type Totals struct {
	Jobs            int64   `json:"jobs"`
	RunnerMinutes   float64 `json:"runner_minutes"`
	EstCostUSD      float64 `json:"est_cost_usd"`
	JobsWithoutCost int64   `json:"jobs_without_cost"`
}

// Window names the requested time range; echoed in the response so
// the UI can label its chart axis.
type Window struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}

// Response is the full /api/stats payload.
type Response struct {
	Window  Window   `json:"window"`
	GroupBy GroupBy  `json:"group_by"`
	Totals  Totals   `json:"totals"`
	Buckets []Bucket `json:"buckets"`
}

// TimeseriesResponse is the /api/stats/timeseries payload. One row
// per UTC calendar day within Window, with terminal-status job
// counts so the UI can stack success vs failed bars.
type TimeseriesResponse struct {
	Window Window          `json:"window"`
	Days   []job.DayBucket `json:"days"`
}

// UserBucket is one row in the /api/stats/top-users response: the
// GitHub sender that triggered N terminal-state jobs in the window,
// with the same cost / runner-time roll-ups as the project/pool/repo
// rollup uses. Empty Login is filtered out at the SQL layer (rows
// pre-dating the sender_login column hold "").
type UserBucket struct {
	Login         string  `json:"login"`
	Jobs          int64   `json:"jobs"`
	RunnerMinutes float64 `json:"runner_minutes"`
	EstCostUSD    float64 `json:"est_cost_usd"`
}

// TopUsersResponse is the /api/stats/top-users payload.
type TopUsersResponse struct {
	Window Window       `json:"window"`
	Limit  int          `json:"limit"`
	Users  []UserBucket `json:"users"`
}
