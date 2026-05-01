// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// Package project owns the project domain - both the HTTP API and
// the SQLite-backed persistence.
// A project is a logical grouping that owns a set of repos and
// one or more pools.  The EC2 launch settings (AMI / instance types / subnets / SGs / IAM profile / ...)
// live on the pool, not here.
//
// Project.MaxConcurrentRunners is a project-wide ceiling across all
// pools (zero = no project-level cap; per-pool caps still apply).
//
// Runner-label generation lives in the pool package
// (`internal/domain/pool/pool.go::RunnerLabels`) since labels include
// the pool name; project never spawns runners on its own.
//
// File layout:
//
//	project.go    package doc + shared constants (this file)
//	endpoint.go   Fiber Handler + HTTP methods
//	store.go      SQLite-backed Store + queries
package project
