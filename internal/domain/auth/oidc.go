// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package auth

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"github.com/yousysadmin/pacer/internal/core/authenticator"
	"github.com/yousysadmin/pacer/internal/core/env"
	pacoidc "github.com/yousysadmin/pacer/internal/core/oidc"
	"github.com/yousysadmin/pacer/internal/core/response"
	"github.com/yousysadmin/pacer/internal/models/audit"
	usermodel "github.com/yousysadmin/pacer/internal/models/user"
)

// OIDCStart kicks off the SSO redirect: mints state/nonce/PKCE, sets
// the short-lived signed cookie, and 302s the browser at the IdP's
// authorization endpoint.
func (h *Handler) OIDCStart(c *fiber.Ctx) error {
	if h.Runtime.OIDC == nil {
		return response.BadRequest(c, "OIDC is not enabled")
	}
	jwtSecret := []byte(h.Runtime.Config.Auth.JWTSecret)
	redirect, cookieValue, err := h.Runtime.OIDC.Authorize(jwtSecret)
	if err != nil {
		return response.Internal(c, err)
	}
	c.Cookie(buildOIDCStateCookie(h.Runtime, cookieValue, pacoidc.StateCookieTTL))
	return c.Redirect(redirect, fiber.StatusFound)
}

// OIDCCallback completes the SSO round-trip: verifies the state
// cookie, exchanges the code, validates the ID token, runs the
// allowlist, finds-or-creates the user, mints a session JWT cookie,
// then redirects to /. Allowlist denials show a generic "access
// denied" page so the failure mode doesn't leak which leg of the
// allowlist refused.
func (h *Handler) OIDCCallback(c *fiber.Ctx) error {
	if h.Runtime.OIDC == nil {
		return response.BadRequest(c, "OIDC is not enabled")
	}

	if errParam := c.Query("error"); errParam != "" {
		// Both values are caller-controlled and reach the audit table.
		// Cap them so an unauthenticated client cannot bloat rows.
		desc := truncate(c.Query("error_description"), idpErrorMaxLen)
		h.auditOIDCFailed(c, "", fmt.Sprintf("idp returned error: %s (%s)", truncate(errParam, idpErrorMaxLen), desc))
		c.Cookie(buildOIDCStateCookie(h.Runtime, "", -time.Hour))
		return redirectLogin(c, "sso_idp_error")
	}

	state := c.Query("state")
	code := c.Query("code")
	if state == "" || code == "" {
		h.auditOIDCFailed(c, "", "callback missing state or code")
		c.Cookie(buildOIDCStateCookie(h.Runtime, "", -time.Hour))
		return redirectLogin(c, "sso_bad_callback")
	}

	cookieValue := c.Cookies(pacoidc.StateCookie)
	if cookieValue == "" {
		h.auditOIDCFailed(c, "", "state cookie missing on callback")
		return redirectLogin(c, "sso_state_missing")
	}

	jwtSecret := []byte(h.Runtime.Config.Auth.JWTSecret)
	claims, err := h.Runtime.OIDC.Exchange(c.UserContext(), jwtSecret, state, code, cookieValue)
	c.Cookie(buildOIDCStateCookie(h.Runtime, "", -time.Hour))
	if err != nil {
		h.auditOIDCFailed(c, "", "exchange/verify: "+err.Error())
		return redirectLogin(c, "sso_token_invalid")
	}

	if err := h.Runtime.OIDC.Admit(claims); err != nil {
		h.auditOIDCDenied(c, claims, err.Error())
		return redirectLogin(c, "sso_access_denied")
	}

	u, err := h.findOrCreateOIDCUser(c, claims)
	if err != nil {
		h.auditOIDCFailed(c, claims.Email, "user provisioning: "+err.Error())
		return response.Internal(c, err)
	}
	if u.Disabled {
		h.auditOIDCDenied(c, claims, "user disabled in pacer")
		return redirectLogin(c, "sso_access_denied")
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

	_ = h.Runtime.Store.Audit.Put(c.UserContext(), &audit.Entry{
		ID:          uuid.NewString(),
		ActorUserID: u.ID,
		ActorEmail:  u.Email,
		Action:      audit.ActionOIDCLoginOK,
		TargetType:  "user",
		TargetID:    u.ID,
		Detail: audit.Detail(map[string]any{
			"sub": claims.Subject,
		}),
		ClientIP:   c.IP(),
		OccurredAt: time.Now().UTC(),
	})
	slog.Info("auth: oidc login", "user_id", u.ID, "email", u.Email)
	return c.Redirect("/", fiber.StatusFound)
}

// findOrCreateOIDCUser maps the IdP claims to a local users row.
// Lookup precedence: oidc_subject (most stable, survives email
// changes at the IdP) -> email (links a pre-existing local user to
// this IdP identity on first SSO sign-in) -> create new.
func (h *Handler) findOrCreateOIDCUser(c *fiber.Ctx, claims *pacoidc.Claims) (*usermodel.User, error) {
	email := strings.ToLower(strings.TrimSpace(claims.Email))

	u, err := h.Runtime.Store.User.GetByOIDCSubject(c.UserContext(), claims.Subject)
	if err != nil {
		return nil, err
	}
	if u != nil {
		return u, nil
	}

	// Linking an IdP identity to an existing local account by email is
	// only safe when the IdP vouches for that email. Without the flag
	// a user who controls their own email claim could take over a
	// local admin account.
	if email != "" {
		existing, err := h.Runtime.Store.User.Get(c.UserContext(), email)
		if err != nil {
			return nil, err
		}
		if existing != nil && !bool(claims.EmailVerified) {
			return nil, fmt.Errorf("email %q matches an existing account but the IdP did not mark it verified, refusing to link", email)
		}
		if existing != nil {
			existing.OIDCSubject = claims.Subject
			if err := h.Runtime.Store.User.Put(c.UserContext(), existing); err != nil {
				return nil, err
			}
			_ = h.Runtime.Store.Audit.Put(c.UserContext(), &audit.Entry{
				ID:          uuid.NewString(),
				ActorUserID: existing.ID,
				ActorEmail:  existing.Email,
				Action:      audit.ActionUserOIDCLinked,
				TargetType:  "user",
				TargetID:    existing.ID,
				ClientIP:    c.IP(),
				OccurredAt:  time.Now().UTC(),
			})
			return existing, nil
		}
	}

	if email == "" {
		return nil, fmt.Errorf("id_token has no email and no existing user matches sub %q", claims.Subject)
	}
	// First OIDC user gets RoleAdmin so the operator who set up the
	// IdP can administer pacer. Subsequent JIT-provisioned users
	// default to RoleUser. Today middleware doesn't tier on role
	// (CLAUDE.md: v1 is "in or out"), but we don't want to silently
	// hand admin to anyone who happens to satisfy the allowlist -
	// future role enforcement would treat that as a privilege grant.
	count, err := h.Runtime.Store.User.Count(c.UserContext())
	if err != nil {
		return nil, err
	}
	role := usermodel.RoleUser
	if count == 0 {
		role = usermodel.RoleAdmin
	}
	fresh := &usermodel.User{
		ID:          uuid.NewString(),
		Email:       email,
		OIDCSubject: claims.Subject,
		Role:        role,
		CreatedAt:   time.Now().UTC(),
	}
	if err := h.Runtime.Store.User.Put(c.UserContext(), fresh); err != nil {
		return nil, err
	}
	_ = h.Runtime.Store.Audit.Put(c.UserContext(), &audit.Entry{
		ID:          uuid.NewString(),
		ActorUserID: fresh.ID,
		ActorEmail:  fresh.Email,
		Action:      audit.ActionUserCreated,
		TargetType:  "user",
		TargetID:    fresh.ID,
		Detail: audit.Detail(map[string]any{
			"via": "oidc",
			"sub": claims.Subject,
		}),
		ClientIP:   c.IP(),
		OccurredAt: time.Now().UTC(),
	})
	return fresh, nil
}

func (h *Handler) auditOIDCFailed(c *fiber.Ctx, email, reason string) {
	_ = h.Runtime.Store.Audit.Put(c.UserContext(), &audit.Entry{
		ID:         uuid.NewString(),
		ActorEmail: email,
		Action:     audit.ActionOIDCLoginFailed,
		TargetType: "user",
		TargetID:   email,
		Detail: audit.Detail(map[string]any{
			"reason": reason,
		}),
		ClientIP:   c.IP(),
		OccurredAt: time.Now().UTC(),
	})
	slog.Warn("auth: oidc login failed", "email", email, "reason", reason, "client_ip", c.IP())
}

func (h *Handler) auditOIDCDenied(c *fiber.Ctx, claims *pacoidc.Claims, reason string) {
	_ = h.Runtime.Store.Audit.Put(c.UserContext(), &audit.Entry{
		ID:         uuid.NewString(),
		ActorEmail: claims.Email,
		Action:     audit.ActionOIDCLoginDenied,
		TargetType: "user",
		TargetID:   claims.Email,
		Detail: audit.Detail(map[string]any{
			"sub":    claims.Subject,
			"reason": reason,
		}),
		ClientIP:   c.IP(),
		OccurredAt: time.Now().UTC(),
	})
	slog.Warn("auth: oidc denied", "email", claims.Email, "sub", claims.Subject, "reason", reason, "client_ip", c.IP())
}

// redirectLogin sends the browser back to /login with an error code
// the SPA can render as a banner. Generic codes -- never include the
// raw failure reason in the URL.
func redirectLogin(c *fiber.Ctx, code string) error {
	return c.Redirect("/login?err="+code, fiber.StatusFound)
}

func buildOIDCStateCookie(rt *env.Runtime, value string, ttl time.Duration) *fiber.Cookie {
	return &fiber.Cookie{
		Name:     pacoidc.StateCookie,
		Value:    value,
		Path:     "/",
		HTTPOnly: true,
		SameSite: "Lax", // Lax (not Strict) so the IdP redirect carries it back
		Secure:   cookieSecure(rt),
		Expires:  time.Now().Add(ttl),
	}
}

// idpErrorMaxLen caps IdP-supplied error text stored in the audit log.
const idpErrorMaxLen = 200

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
