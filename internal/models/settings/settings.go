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

// KeyAuditRetentionDays / KeyWebhookRetentionDays are operator
// overrides for the YAML retention.audit_days / retention.webhook_days
// defaults. Stored as decimal strings ("90"); the pruner reads them
// on every tick so a Settings UI change takes effect at the next
// daily sweep without a process restart. Missing key = use YAML
// default; a malformed value logs a warning and falls back too.
const (
	KeyAuditRetentionDays   = "audit_retention_days"
	KeyWebhookRetentionDays = "webhook_retention_days"
)

// Setting is one row in the settings table. Value is opaque to the
// store (semantics live in callers).
type Setting struct {
	Key       string    `json:"key"`
	Value     string    `json:"value"`
	UpdatedAt time.Time `json:"updated_at"`
}
