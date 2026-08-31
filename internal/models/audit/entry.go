// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// Package audit models the immutable audit log.
// Every state-changing handler appends an Entry. Pruning is the only mutation.
package audit

import (
	"encoding/json"
	"time"
)

// Detail builds the audit Entry's Detail string from a key/value
// map, using encoding/json so exotic Unicode round-trips cleanly
// (the older fmt.Sprintf %q approach produces Go-quoted - not
// JSON-quoted - strings, which differ for some control / Unicode
// codepoints).
//
// Best-effort: a Marshal failure is silently swallowed and the empty
// string returned. The audit Entry's Detail column is `,omitempty`,
// so an empty result just elides the field. Audit writes themselves
// ignore errors. This matches that posture.
//
// Pass map[string]any so callers don't have to declare a struct per
// site. Keys should be stable JSON-shaped names matching what the
// UI will key off of (e.g. "scope", "tags_changed", "exit_code").
func Detail(kv map[string]any) string {
	if len(kv) == 0 {
		return ""
	}
	b, err := json.Marshal(kv)
	if err != nil {
		return ""
	}
	return string(b)
}

const (
	ActionProjectCreated     = "project.created"
	ActionProjectUpdated     = "project.updated"
	ActionProjectDeleted     = "project.deleted"
	ActionPoolCreated        = "pool.created"
	ActionPoolUpdated        = "pool.updated"
	ActionPoolDeleted        = "pool.deleted"
	ActionRepoBound          = "repo.bound"
	ActionRepoUnbound        = "repo.unbound"
	ActionJobEnqueued        = "job.enqueued"
	ActionJobCompleted       = "job.completed"
	ActionJobFailed          = "job.failed"
	ActionJobNoPoolMatch     = "job.no_pool_match"
	ActionJobSpawnRetry      = "job.spawn_retry"
	ActionJobSpawnExhausted  = "job.spawn_capacity_exhausted"
	ActionInstanceLaunched   = "instance.launched"
	ActionInstanceRegistered = "instance.registered"
	// GitHub refused to mint a JIT config. The runner only ever sees
	// a status code, so the reason it was refused lives here.
	ActionRunnerRegisterFailed = "runner.register_failed"
	ActionInstanceTerminated   = "instance.terminated"
	ActionInstanceReaped       = "instance.reaped"
	ActionInstanceLost         = "instance.lost"
	ActionConfigExported       = "config.exported"
	ActionConfigImported       = "config.imported"
	ActionUserCreated          = "user.created"
	ActionLoginFailed          = "auth.login_failed"
	ActionOIDCLoginOK          = "auth.oidc.login_succeeded"
	ActionOIDCLoginDenied      = "auth.oidc.login_denied"
	ActionOIDCLoginFailed      = "auth.oidc.login_failed"
	ActionUserOIDCLinked       = "user.oidc_linked"
	// Settings mutations. Rotating the bootstrap token invalidates
	// every in-flight spawn, so it must leave a trace.
	ActionBootstrapTokenRotated = "settings.bootstrap_token_rotated"
	ActionRetentionUpdated      = "settings.retention_updated"
	// ActionAuditPruned records a manual operator-driven cleanup of
	// the audit_log table. The entry survives the prune it describes
	// (its occurred_at is by definition after the cutoff), so the
	// log retains a self-documenting trace of who deleted what.
	ActionAuditPruned = "audit.pruned"
)

type Entry struct {
	ID          string    `json:"id"`
	ActorUserID string    `json:"actor_user_id,omitempty"` // empty for system actors
	ActorEmail  string    `json:"actor_email,omitempty"`
	Action      string    `json:"action"`
	TargetType  string    `json:"target_type,omitempty"`
	TargetID    string    `json:"target_id,omitempty"`
	Detail      string    `json:"detail,omitempty"` // JSON. Never put secrets here
	ClientIP    string    `json:"client_ip,omitempty"`
	RequestID   string    `json:"request_id,omitempty"`
	OccurredAt  time.Time `json:"occurred_at"`
}

type ListFilter struct {
	Action     string
	Actor      string
	TargetType string
	TargetID   string
	// Q is a substring search applied across the columns operators
	// actually need to look up: target_id (job_id, instance_id,
	// pool/project UUID), detail (the JSON blob - instance_id,
	// pool name, AWS state, etc. all live here), client_ip,
	// actor_email, request_id, and action. SQLite LIKE is
	// case-insensitive for ASCII, which is what every searchable
	// audit field is in practice. Empty Q disables the search.
	// Combines with the other filters via AND.
	Q string
	// Since / Until form an inclusive-exclusive time window.
	// Zero values disable that side of the filter.
	Since  time.Time
	Until  time.Time
	Limit  int
	Offset int
}
