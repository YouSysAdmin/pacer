// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// Package repo owns the repo-to-project binding domain - both the
// HTTP API and the SQLite-backed persistence.
// Repos use "owner/name" (GitHub's repository.full_name webhook field) as the primary key.
// One repo binds to at most one project. The binding optionally
// overrides the project's max_concurrent_runners cap.
//
// HTTP routes for repos take owner + name as separate path params
// (`/api/repos/:owner/:name`) so Fiber doesn't choke on the slash.
//
// File layout:
//
//	repo.go       package doc + shared constants (this file)
//	endpoint.go   Fiber Handler + HTTP methods
//	store.go      SQLite-backed Store + queries
package repo
