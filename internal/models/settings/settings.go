// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// Package settings models the pacer-managed key-value config that
// lives in the database (as opposed to YAML-frozen config in
// env.Config). Today this is just the bootstrap API token; future
// single-row settings live alongside under different keys.
package settings

import "time"

// KeyBootstrapAPIToken is the secret the bootstrap script presents in
// `Authorization: Bearer <token>` when calling /api/runner/bootstrap.
// Auto-generated on first pacer start; rotatable via the Settings UI.
const KeyBootstrapAPIToken = "bootstrap_api_token"

// Setting is one row in the settings table. Value is opaque to the
// store (semantics live in callers).
type Setting struct {
	Key       string    `json:"key"`
	Value     string    `json:"value"`
	UpdatedAt time.Time `json:"updated_at"`
}
