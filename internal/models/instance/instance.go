// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// Package instance models the local mirror of a spawned EC2 instance.
// State is the tool's view, refreshed by self-registration callbacks
// and by the reaper's periodic DescribeInstances sweep.
package instance

import "time"

type State string

const (
	StateStarting   State = "starting"   // RunInstances accepted, awaiting self-reg
	StateRunning    State = "running"    // self-registered, runner active
	StateTerminated State = "terminated" // EC2 confirmed terminated
	StateReaped     State = "reaped"     // tool terminated due to timeout / orphan
)

type Instance struct {
	ID           string `json:"id"` // i-xxxx
	JobID        string `json:"job_id"`
	ProjectID    string `json:"project_id"`
	PoolID       string `json:"pool_id"`
	InstanceType string `json:"instance_type,omitempty"`
	AZ           string `json:"az,omitempty"`
	State        State  `json:"state"`
	Spot         bool   `json:"spot"`
	// PricePerHour + PriceModel are the launch-time snapshot.
	// Nil PricePerHour means the pricing fetch failed -- cost rollups
	// then leave estimated_cost_usd NULL for jobs that ran on this
	// instance.
	// PriceModel is one of pricing.ModelOnDemand /ModelSpot, or "" when no quote was stamped.
	PricePerHour *float64   `json:"price_per_hour,omitempty"`
	PriceModel   string     `json:"price_model,omitempty"`
	LaunchedAt   time.Time  `json:"launched_at"`
	RegisteredAt *time.Time `json:"registered_at,omitempty"`
	TerminatedAt *time.Time `json:"terminated_at,omitempty"`
	LastSeenAt   *time.Time `json:"last_seen_at,omitempty"`
	// GHRunnerID is GitHub's integer runner identity, returned by
	// generate-jitconfig at register time. The reaper uses it to
	// DELETE the runner from GitHub when the instance is lost so
	// the workflow_job fast-fails instead of hanging on heartbeat
	// timeout. Zero when the runner never registered.
	GHRunnerID int64 `json:"gh_runner_id,omitzero"`
}
