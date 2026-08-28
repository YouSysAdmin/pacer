// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package auth

import (
	"log/slog"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"github.com/yousysadmin/pacer/internal/core/authenticator"
	"github.com/yousysadmin/pacer/internal/core/env"
	"github.com/yousysadmin/pacer/internal/core/response"
	"github.com/yousysadmin/pacer/internal/core/validation"
	"github.com/yousysadmin/pacer/internal/models/audit"
	usermodel "github.com/yousysadmin/pacer/internal/models/user"
)

type Handler struct {
	Runtime *env.Runtime
}

// loginInput carries credentials for /api/auth/login. The
// normalize:"normalize" tag (trim+lower) on Email runs before
// validation so a "  Foo@Example.com " becomes "foo@example.com"
// and matches the lower-cased rows in the users table. Password is
// trimmed only -- never lower-cased -- and kept short of an
// unreasonable upper bound so a runaway POST can't waste bcrypt's
// CPU budget on a 1 MB attempt.
type loginInput struct {
	Email    string `json:"email"    validate:"required,email,max=320" normalize:"normalize"`
	Password string `json:"password" validate:"required,min=1,max=256" normalize:"trim"`
}

// Login validates email + password against the users table, sets the
// session cookie, and returns the public user record.
// Uniform error for missing user / wrong password / disabled account so the
// response never leaks which leg failed.
func (h *Handler) Login(c *fiber.Ctx) error {
	if !h.Runtime.Config.Auth.Local.Enabled {
		return response.BadRequest(c, "local login is disabled")
	}
	in, err := validation.BindAndValidate[loginInput](c)
	if err != nil {
		fes := validation.Humanize(err)
		return response.BadRequestFields(c, validation.Summary(fes), fes)
	}

	u, err := h.Runtime.Store.User.Get(c.UserContext(), in.Email)
	if err != nil {
		return response.Internal(c, err)
	}
	// Run the bcrypt verify on every leg (including unknown email or
	// disabled account) so response timing does not leak which leg
	// failed.
	// The dummy verify is a real cost-12 bcrypt over an unguessable secret -
	// it consumes the same time as the real path and always returns false.
	if u == nil || u.Disabled {
		_ = authenticator.VerifyDummyPassword(in.Password)
		h.auditLoginFailed(c, in.Email, "unknown_or_disabled")
		return response.Unauthorized(c, "invalid credentials")
	}
	if !authenticator.VerifyPassword(u.PasswordHash, in.Password) {
		h.auditLoginFailed(c, in.Email, "bad_password")
		return response.Unauthorized(c, "invalid credentials")
	}

	tok, err := authenticator.CreateToken(
		h.Runtime.Config.Auth.JWTSecret,
		u.ID, u.Email,
		sessionTTL(h.Runtime),
	)
	if err != nil {
		return response.Internal(c, err)
	}

	c.Cookie(buildSessionCookie(h.Runtime, tok, sessionTTL(h.Runtime)))
	if err := h.Runtime.Store.User.TouchLastLogin(c.UserContext(), u.Email); err != nil {
		slog.Warn("auth: touch last login failed", "email", u.Email, "err", err)
	}
	slog.Info("auth: login", "user_id", u.ID, "email", u.Email)
	return response.Success(c, fiber.Map{"user": u})
}

// Logout clears the session cookie.
// No server-side state to wipe -- the JWT is stateless, so an attacker
// who already exfiltrated a live cookie keeps that cookie valid until exp.
// For an operator-console v1 that's an acceptable trade vs. running a
// per-session deny-list.
func (h *Handler) Logout(c *fiber.Ctx) error {
	c.Cookie(buildSessionCookie(h.Runtime, "", -time.Hour))
	return response.NoContent(c)
}

// Me returns the user resolved from the session cookie, or a hint
// that auth is disabled entirely. Shapes:
//   - 200 {"user": {...}}          authenticated
//   - 200 {"auth_disabled": true}  auth.disabled=true. No user concept
//   - 401 {"error": "..."}         auth is on but caller has no session
func (h *Handler) Me(c *fiber.Ctx) error {
	if h.Runtime.Config.Auth.Disabled {
		return response.Success(c, fiber.Map{"auth_disabled": true})
	}
	u, ok := c.Locals(UserLocalKey).(*usermodel.User)
	if !ok || u == nil {
		return response.Unauthorized(c, "not authenticated")
	}
	return response.Success(c, fiber.Map{"user": u})
}

// UserLocalKey is the c.Locals slot the auth middleware writes the
// resolved *user.User into.  Exported so middleware + endpoint can
// agree on the key without a cyclic import.
const UserLocalKey = "auth.user"

// Info returns the auth posture for the unauthenticated login page so
// it can render the right control (local form vs OIDC button vs
// nothing when auth.disabled). Open endpoint -- not gated.
func (h *Handler) Info(c *fiber.Ctx) error {
	cfg := h.Runtime.Config.Auth
	if cfg.Disabled {
		return response.Success(c, fiber.Map{"auth_disabled": true})
	}
	out := fiber.Map{
		"local_enabled": cfg.Local.Enabled,
		"oidc_enabled":  cfg.OIDC.Enabled,
	}
	if h.Runtime.OIDC != nil {
		out["oidc_label"] = h.Runtime.OIDC.IssuerHost()
	}
	return response.Success(c, out)
}

// auditLoginFailed records a failed login attempt.
// Records the email the caller offered (lowercased+trimmed) and a coarse reason
// code -- never the password or stored hash.
// Best-effort: a store error here must not block the 401 response.
func (h *Handler) auditLoginFailed(c *fiber.Ctx, email, reason string) {
	_ = h.Runtime.Store.Audit.Put(c.UserContext(), &audit.Entry{
		ID:         uuid.NewString(),
		Action:     audit.ActionLoginFailed,
		TargetType: "user",
		TargetID:   email,
		Detail: audit.Detail(map[string]any{
			"email":  email,
			"reason": reason,
		}),
		ClientIP:   c.IP(),
		OccurredAt: time.Now().UTC(),
	})
	slog.Warn("auth: login failed", "email", email, "reason", reason, "client_ip", c.IP())
}

func sessionTTL(rt *env.Runtime) time.Duration {
	if rt.Config.Auth.SessionTTL == "" {
		return 12 * time.Hour
	}
	d, err := time.ParseDuration(rt.Config.Auth.SessionTTL)
	if err != nil || d <= 0 {
		return 12 * time.Hour
	}
	return d
}

// buildSessionCookie centralizes the cookie shape so login + logout
// produce a matching pair.
// HttpOnly + SameSite=Strict + Secure when the operator advertises
// the tool over HTTPS. Path=/ so the SPA + every /api/* call shares it.
//
// Secure is derived from server.public_url rather than c.Protocol()
// so a TLS-terminating reverse proxy (HTTPS to the client, plain HTTP
// upstream) still gets the Secure flag set.
func buildSessionCookie(rt *env.Runtime, value string, ttl time.Duration) *fiber.Cookie {
	return &fiber.Cookie{
		Name:     SessionCookie,
		Value:    value,
		Path:     "/",
		HTTPOnly: true,
		SameSite: "Strict",
		Secure:   cookieSecure(rt),
		Expires:  time.Now().Add(ttl),
	}
}

// cookieSecure reports whether the operator's advertised public URL
// is HTTPS. Used by both the session cookie and the OIDC state
// cookie. Returns false when public_url is empty (local dev).
func cookieSecure(rt *env.Runtime) bool {
	return strings.HasPrefix(strings.ToLower(rt.Config.Server.PublicURL), "https://")
}
