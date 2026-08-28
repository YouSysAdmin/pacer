// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// Package user is the persistence + (eventually) handler surface for
// the operator-console user record.
// Single-operator deploy: in practice there's exactly one row in the users table,
// minted at bootstrap.
// The model + the UserStore interface live alongside the other domain packages.
// Only the SQLite store impl lives here today.
package user
