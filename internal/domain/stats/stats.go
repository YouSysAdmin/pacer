// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// Package stats is the read-only cost + activity rollup over the
// jobs table.
// All work happens in store.go (a single SQL pass per rollup axis) and endpoint.go (HTTP edge).
package stats
