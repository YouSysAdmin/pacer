// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// Package job owns the job lifecycle - both the read-only HTTP API
// and the SQLite-backed persistence.
// Job rows model a single GitHub workflow_job from "queued" webhook through
// completion or reap.
// Lifecycle:
//
//	queued -> claimed -> starting -> running -> completed | failed | reaped
//
// Rows are created by the webhook handler (queued), mutated by the
// orchestrator (claim, spawn stamp), the runner self-registration
// callback (running), and the reaper (reaped).
// This package only exposes read endpoints. The UI just observes.
//
// File layout:
//
//	job.go        package doc + shared constants (this file)
//	endpoint.go   Fiber Handler + HTTP methods (read-only)
//	store.go      SQLite-backed Store + queue/lifecycle queries
package job
