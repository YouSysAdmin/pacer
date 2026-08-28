// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// Package pool models a runner pool inside a project - a named
// bundle of EC2 launch settings (AMI, instance types, subnets,
// security groups, IAM profile, root volume, runtime cap,
// concurrency cap, spot toggle, tags, optional user-data tail).
// One pool maps to one EC2 launch template materialized by the tool.
//
// A project has one or more pools.
// The webhook handler picks a pool per job by matching workflow_job.labels[]
// against the pool's generated label set.
package pool

import "time"

type Pool struct {
	ID                   string   `json:"id"`
	ProjectID            string   `json:"project_id"`
	Name                 string   `json:"name"`
	IsDefault            bool     `json:"is_default"`
	Priority             int      `json:"priority"` // lower = preferred when multiple pools match
	AMIID                string   `json:"ami_id"`
	InstanceTypes        []string `json:"instance_types"` // priority order for spot fallback
	SubnetIDs            []string `json:"subnet_ids"`
	SecurityGroupIDs     []string `json:"security_group_ids"`
	IAMInstanceProfile   string   `json:"iam_instance_profile"`
	RootVolumeGB         int      `json:"root_volume_gb"`
	MaxRuntimeMinutes    int      `json:"max_runtime_minutes"`
	MaxConcurrentRunners int      `json:"max_concurrent_runners"`
	Spot                 bool     `json:"spot"`
	// SpawnMethod selects how the orchestrator launches instances:
	//   "fleet"          - CreateFleet(Type=instant); LT carries the
	//                      static shape, Overrides pass every
	//                      (instance_type x subnet) combo, AWS picks an
	//                      available one. Free multi-AZ. Default.
	//   "run_instances"  - serial RunInstances loop, one type at a
	//                      time, only first subnet. Kept as opt-down.
	//
	// Market (spot vs on-demand) is governed by Spot, NOT SpawnMethod --
	// both methods inherit MarketType from the LT (which is set from
	// Spot at materialize time). The two axes are orthogonal.
	SpawnMethod string `json:"spawn_method,omitempty"`
	// AllocationStrategy controls how Fleet picks among the (type,
	// subnet) overrides. Only meaningful when SpawnMethod="fleet".
	//   "cost"          - lowest-price (on-demand) / price-capacity-
	//                     optimized (spot). Default; AWS picks
	//                     cheapest + capacity-safe; the
	//                     instance_types list order doesn't matter.
	//   "lowest_price"  - lowest-price for both markets. PURE cheapest
	//                     -- ignores capacity signals, so spot
	//                     instances may land in shallow pools that
	//                     interrupt soon after launch. Pick this when
	//                     cost trumps reliability (short throwaway
	//                     jobs); avoid for long-running workloads.
	//   "capacity"      - lowest-price (on-demand) / capacity-optimized
	//                     (spot). Deepest spot pool, ignore price --
	//                     production-reliability default. On-demand
	//                     has no capacity concept so falls through to
	//                     lowest-price.
	//   "priority"      - prioritized (on-demand) / capacity-optimized-
	//                     prioritized (spot). Honors the
	//                     instance_types list order: first item is
	//                     preferred, second is fallback, etc. For
	//                     spot, capacity is still the first concern --
	//                     priority is a tiebreaker.
	AllocationStrategy string `json:"allocation_strategy,omitempty"`
	// ExtraLabels are operator-supplied runner labels appended to the
	// auto-derived [self-hosted, <project>, <pool>, <owner>-<repo>]
	// set.  Use them for cross-cutting capability tags ("gpu", "arm64",
	// "large", "windows") that workflows can target via runs-on.
	// Sanitized identically (case-insensitive; non-alnum/_ collapse to
	// '-'); the gha:* prefix is rejected to keep the tool-managed
	// namespace clean.
	ExtraLabels []string          `json:"extra_labels,omitempty"`
	Tags        map[string]string `json:"tags"`
	// RunnerVersion pins the actions/runner release the pool's
	// instances download at boot.  Empty string = use the
	// server-cached latest from internal/core/ghrunner.  Format is
	// the bare semver ("2.319.1"), no leading "v".
	RunnerVersion string `json:"runner_version,omitempty"`
	// RunnerUser is the OS user the actions/runner runs as on
	// spawned instances.  Empty = root (with RUNNER_ALLOW_RUNASROOT=1).
	// Set to a non-root user (e.g. "admin", "ec2-user", "ubuntu")
	// when the AMI installs CI tooling per-user (rbenv, nvm, asdf,
	// per-user gem/node prefixes); user-data chowns the runner home
	// and sudo's into that user before invoking ./run.sh.
	RunnerUser            string    `json:"runner_user,omitempty"`
	UserDataExtra         string    `json:"user_data_extra,omitempty"`
	LaunchTemplateID      string    `json:"launch_template_id,omitempty"`
	LaunchTemplateVersion int       `json:"launch_template_version,omitempty"`
	Disabled              bool      `json:"disabled"`
	CreatedAt             time.Time `json:"created_at"`
	UpdatedAt             time.Time `json:"updated_at"`
}

// MaxRuntimeMinutesCap bounds MaxRuntimeMinutes so a bad value can
// neither reap every instance instantly (zero) nor overflow a
// time.Duration (huge). Seven days.
const MaxRuntimeMinutesCap = 7 * 24 * 60

// EffectiveMaxRuntime is the runtime cap the orchestrator and reaper
// both apply. A nil pool or an out-of-range value clamps to the cap.
func (p *Pool) EffectiveMaxRuntime() time.Duration {
	if p == nil || p.MaxRuntimeMinutes <= 0 || p.MaxRuntimeMinutes > MaxRuntimeMinutesCap {
		return time.Duration(MaxRuntimeMinutesCap) * time.Minute
	}
	return time.Duration(p.MaxRuntimeMinutes) * time.Minute
}
