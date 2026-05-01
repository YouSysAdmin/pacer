// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// Package user is the dashboard user record.
package user

import "time"

type Role string

const (
	RoleAdmin Role = "admin"
	RoleUser  Role = "user"
)

// User is the operator-console account.
//
// Role + SuperUser are persisted but NOT enforced by any middleware
// today: v1's auth posture is "in or out". They exist on the model
// for forward-compat so the schema doesn't have to churn when role
// gating lands. Until then, treat them as descriptive metadata -
// don't add per-handler IsAdmin() checks, since the rest of the
// surface won't honor them and that creates a misleading signal.
// The first user (local bootstrap or first OIDC sign-in) is
// provisioned RoleAdmin so the eventual upgrade is a no-op for the
// person who set the install up.
type User struct {
	ID             string     `json:"id"`
	Email          string     `json:"email"`
	PasswordHash   string     `json:"-"` // empty for OIDC-only users
	OIDCSubject    string     `json:"-"` // IdP `sub` claim; empty for local-only users
	Role           Role       `json:"role"`
	SuperUser      bool       `json:"super_user"`
	Disabled       bool       `json:"disabled"`
	RefreshVersion int        `json:"refresh_version"`
	CreatedAt      time.Time  `json:"created_at"`
	LastLoginAt    *time.Time `json:"last_login_at,omitempty"`
}

// IsAdmin reports whether the user is provisioned at admin tier. See
// the type comment: middleware does NOT consult this in v1. It's
// available for forward-compat code that wants to opt in to role
// gating, e.g. a future settings page.
func (u *User) IsAdmin() bool { return u.SuperUser || u.Role == RoleAdmin }
