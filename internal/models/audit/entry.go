// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// Package audit models the immutable audit log.
// Every state-changing handler appends an Entry; pruning is the only mutation.
package audit

import (
	"encoding/json"
	"time"
)

// Detail builds the audit Entry's Detail string from a key/value
// map, using encoding/json so exotic Unicode round-trips cleanly
// (the older fmt.Sprintf %q approach produces Go-quoted -- not
// JSON-quoted -- strings, which differ for some control / Unicode
// codepoints).
//
// Best-effort: a Marshal failure is silently swallowed and the empty
// string returned. The audit Entry's Detail column is `,omitempty`,
// so an empty result just elides the field. Audit writes themselves
// ignore errors; this matches that posture.
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
	ActionInstanceTerminated = "instance.terminated"
	ActionInstanceReaped     = "instance.reaped"
	ActionConfigExported     = "config.exported"
	ActionConfigImported     = "config.imported"
	ActionUserCreated        = "user.created"
	ActionLoginFailed        = "auth.login_failed"
	ActionOIDCLoginOK        = "auth.oidc.login_succeeded"
	ActionOIDCLoginDenied    = "auth.oidc.login_denied"
	ActionOIDCLoginFailed    = "auth.oidc.login_failed"
	ActionUserOIDCLinked     = "user.oidc_linked"
)

type Entry struct {
	ID          string    `json:"id"`
	ActorUserID string    `json:"actor_user_id,omitempty"` // empty for system actors
	ActorEmail  string    `json:"actor_email,omitempty"`
	Action      string    `json:"action"`
	TargetType  string    `json:"target_type,omitempty"`
	TargetID    string    `json:"target_id,omitempty"`
	Detail      string    `json:"detail,omitempty"` // JSON; never put secrets here
	ClientIP    string    `json:"client_ip,omitempty"`
	RequestID   string    `json:"request_id,omitempty"`
	OccurredAt  time.Time `json:"occurred_at"`
}

type ListFilter struct {
	Action     string
	Actor      string
	TargetType string
	TargetID   string
	// Since / Until form an inclusive-exclusive time window.
	// Zero values disable that side of the filter.
	Since  time.Time
	Until  time.Time
	Limit  int
	Offset int
}
