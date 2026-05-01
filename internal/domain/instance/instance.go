// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// Package instance owns the SQLite-backed mirror of spawned EC2
// instances.
// Rows are written by the orchestrator at RunInstances time (state=starting),
// updated by the runner self-registration callback (state=running,
// instance_type + AZ stamped), and flipped to terminated/reaped by
// the runner /api/runner/complete handler or the reaper sweep.
//
// No HTTP API yet - orchestrator + reaper + runner endpoints are the only writers/readers.
// When an instance UI lands, it will add an endpoint.go alongside store.go in this package.
//
// File layout:
//
//	instance.go package doc + shared constants (this file)
//	store.go    SQLite-backed Store + queries
package instance
