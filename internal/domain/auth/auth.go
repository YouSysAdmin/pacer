// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// Package auth handles operator-console authentication: local
// (email + password) and OIDC SSO, plus the session cookie that
// gates the CRUD APIs. Webhook + runner callbacks stay HMAC-only
// and are NOT under requireAuth.
package auth

const (
	// SessionCookie is the name of the httpOnly session cookie.
	// Same-origin SPA + SameSite=Strict means we don't need CSRF
	// protection in v1.
	SessionCookie = "pacer_session"
)
