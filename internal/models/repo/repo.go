// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// Package repo binds a GitHub repository (by full_name) to a project.
// Optional per-repo concurrency cap overrides the project default.
// Optional per-repo tag map is the innermost layer of the
// project / pool / repo tag cascade - repo tags override pool
// tags which override project tags on key conflict.
// Repo tags are stamped at orchestrator spawn time on the instance + volumes
// only. They don't affect the pool's launch template (which is
// shared across repos).
package repo

import "time"

type Repo struct {
	FullName             string            `json:"full_name"` // "org/repo". Primary key
	ProjectID            string            `json:"project_id"`
	MaxConcurrentRunners *int              `json:"max_concurrent_runners,omitempty"` // nil = inherit project
	Tags                 map[string]string `json:"tags,omitempty"`
	CreatedAt            time.Time         `json:"created_at"`
}
