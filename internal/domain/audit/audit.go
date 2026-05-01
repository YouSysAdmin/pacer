// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// Package audit owns the SQLite-backed audit log.
// Append-only - every state-changing handler (project / repo CRUD, webhook ingest,
// runner callbacks) and every orchestrator / reaper action calls Put.
// Entries are immutable; pruning will live alongside the
// eventual audit-log UI.
//
// Action constants are defined on the model side
// (`internal/models/audit/entry.go`) so handlers can reference
// `auditmodel.ActionXxx` without importing this package.
//
// File layout:
//
//	audit.go      package doc + shared constants (this file)
//	store.go      SQLite-backed Store + queries
package audit
