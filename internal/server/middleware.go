// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package server

import (
	"strings"

	"github.com/gofiber/fiber/v2"

	"github.com/yousysadmin/pacer/internal/core/authenticator"
	"github.com/yousysadmin/pacer/internal/core/env"
	"github.com/yousysadmin/pacer/internal/core/response"
	authdomain "github.com/yousysadmin/pacer/internal/domain/auth"
)

// requireAuth gates protected /api/* routes on a valid session
// cookie.
// No-op when auth.disabled=true.
// Webhook + runner callbacks are HMAC-only and aren't wrapped
// with this middleware regardless.
//
// The middleware also writes the resolved *user.User into
// c.Locals(authdomain.UserLocalKey) so handlers (e.g. /api/auth/me)
// can pull it without a second DB read.
//
// AUTHORIZATION TIER: v1 is "in or out". Every authenticated,
// non-disabled user has full access to every protected route. The
// User.Role + User.SuperUser fields exist on the model + table for
// forward-compat (so the schema doesn't churn when role gating
// lands), but no handler enforces them today. If you need
// least-privilege gating before that lands, do it at the IdP /
// allowlist layer (auth.oidc.allowed_groups) - not by
// hand-rolling an admin check in a single handler, which creates
// a misleading "this is gated" signal across the rest.
func requireAuth(rt *env.Runtime) fiber.Handler {
	return func(c *fiber.Ctx) error {
		if rt.Config.Auth.Disabled {
			return c.Next()
		}
		raw := extractToken(c)
		if raw == "" {
			return response.Unauthorized(c, "authentication required")
		}
		claims, err := authenticator.ParseToken(rt.Config.Auth.JWTSecret, raw)
		if err != nil {
			return response.Unauthorized(c, "invalid session: "+err.Error())
		}
		u, err := rt.Store.User.GetByID(c.UserContext(), claims.UserID)
		if err != nil {
			return response.Internal(c, err)
		}
		if u == nil || u.Disabled {
			return response.Unauthorized(c, "user not found or disabled")
		}
		c.Locals(authdomain.UserLocalKey, u)
		return c.Next()
	}
}

// extractToken pulls the session token from the cookie first, then
// falls back to a Bearer header for CLI / curl callers.
func extractToken(c *fiber.Ctx) string {
	if v := c.Cookies(authdomain.SessionCookie); v != "" {
		return v
	}
	if authz := c.Get("Authorization"); strings.HasPrefix(authz, "Bearer ") {
		return strings.TrimPrefix(authz, "Bearer ")
	}
	return ""
}
