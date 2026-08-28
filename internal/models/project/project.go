// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// Package project models a runner project - a logical grouping that
// owns a set of repos and one or more pools.
// EC2-shape configuration (AMI, instance types, subnets, etc.) lives on the pool, not here.
//
// MaxConcurrentRunners is a project-wide ceiling across all pools.
// Zero means no project-level cap, per-pool caps still apply.
//
// Tags cascade to every spawned instance, volume, and launch template
// across all the project's pools.
// Pool.Tags overrides on key conflict.  Reserved prefix: keys starting with `gha:` are
// tool-managed and rejected by the API.
package project

import "time"

// Scope picks how runners register and how webhooks route to a project.
//
//   - "repo" (default) -- 1..N repos bind to the project. JIT config
//     hits /repos/{owner}/{name}/.... Runners carry an <owner>-<repo>
//     narrowing label so they only claim jobs from the bound repo.
//   - "org" -- routes by `repository.owner.login`. No per-repo
//     bindings. JIT config hits /orgs/{org}/... with a runner_group_id.
//     The <owner>-<repo> label is dropped so runners are shared across
//     every repo in the org (or every repo in the chosen runner group).
const (
	ScopeRepo = "repo"
	ScopeOrg  = "org"
)

type Project struct {
	ID                   string            `json:"id"`
	Name                 string            `json:"name"`
	MaxConcurrentRunners int               `json:"max_concurrent_runners"` // 0 = no project-wide ceiling
	Tags                 map[string]string `json:"tags"`
	// Scope selects the runner-registration model. See package consts.
	// Empty string is treated as ScopeRepo for backward compatibility.
	Scope string `json:"scope"`
	// OrgName is the GitHub org login (e.g. "octocat") for org-scoped
	// projects. Empty for repo scope.
	OrgName string `json:"org_name,omitempty"`
	// RunnerGroupID is the org's runner group the JIT config registers
	// into. 0 = "Default" group (id=1). Only meaningful for org scope.
	RunnerGroupID int       `json:"runner_group_id,omitempty"`
	Disabled      bool      `json:"disabled"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}
