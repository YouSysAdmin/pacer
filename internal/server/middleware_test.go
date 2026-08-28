// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/yousysadmin/pacer/internal/core/authenticator"
	"github.com/yousysadmin/pacer/internal/core/env"
	authdomain "github.com/yousysadmin/pacer/internal/domain/auth"
	usermodel "github.com/yousysadmin/pacer/internal/models/user"
	"github.com/yousysadmin/pacer/internal/testutil/runtimeutil"
)

func TestRequireAuth_InvalidTokenMessageIsFixed(t *testing.T) {
	rt := runtimeutil.NewRuntime(t, &env.Config{
		GitHub: env.GitHubConfig{Disabled: true},
		AWS:    env.AWSConfig{Disabled: true},
		Auth:   env.AuthConfig{JWTSecret: strings.Repeat("k", 32)},
	})
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Get("/x", requireAuth(rt), func(c *fiber.Ctx) error { return c.SendStatus(200) })

	for name, hdr := range map[string]string{
		"garbage":  "Bearer not-a-jwt",
		"badalg":   "Bearer eyJhbGciOiJub25lIn0.e30.",
		"noheader": "",
	} {
		req := httptest.NewRequest("GET", "/x", nil)
		if hdr != "" {
			req.Header.Set("Authorization", hdr)
		}
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != 401 {
			t.Fatalf("%s: want 401, got %d", name, resp.StatusCode)
		}
		if strings.Contains(string(body), "signature") || strings.Contains(string(body), "signing method") {
			t.Fatalf("%s: JWT library text leaked: %s", name, body)
		}
	}
}

func TestSecurityHeaders_HSTSOnlyWithTLS(t *testing.T) {
	plain := fiber.New(fiber.Config{DisableStartupMessage: true})
	plain.Use(securityHeaders)
	plain.Get("/", func(c *fiber.Ctx) error { return c.SendStatus(200) })
	resp, _ := plain.Test(httptest.NewRequest("GET", "/", nil))
	if resp.Header.Get("Strict-Transport-Security") != "" {
		t.Fatal("plain HTTP must not send HSTS")
	}
	if resp.Header.Get("X-Frame-Options") != "DENY" {
		t.Fatal("baseline headers missing")
	}

	tlsApp := fiber.New(fiber.Config{DisableStartupMessage: true})
	tlsApp.Use(securityHeaders)
	tlsApp.Use(hstsHeader)
	tlsApp.Get("/", func(c *fiber.Ctx) error { return c.SendStatus(200) })
	resp, _ = tlsApp.Test(httptest.NewRequest("GET", "/", nil))
	if !strings.HasPrefix(resp.Header.Get("Strict-Transport-Security"), "max-age=") {
		t.Fatal("HSTS missing when TLS is on")
	}
}

func TestRequireAuth_SessionPaths(t *testing.T) {
	secret := strings.Repeat("k", 32)
	rt := runtimeutil.NewRuntime(t, &env.Config{
		GitHub: env.GitHubConfig{Disabled: true},
		AWS:    env.AWSConfig{Disabled: true},
		Auth:   env.AuthConfig{JWTSecret: secret},
	})
	ctx := t.Context()
	for _, u := range []*usermodel.User{
		{ID: "u-ok", Email: "ok@example.com", Role: usermodel.RoleAdmin},
		{ID: "u-off", Email: "off@example.com", Role: usermodel.RoleAdmin, Disabled: true},
	} {
		if err := rt.Store.User.Put(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Get("/x", requireAuth(rt), func(c *fiber.Ctx) error {
		u, _ := c.Locals(authdomain.UserLocalKey).(*usermodel.User)
		if u == nil {
			return c.SendStatus(500)
		}
		return c.SendString(u.Email)
	})
	mint := func(id, email string) string {
		tok, err := authenticator.CreateToken(secret, id, email, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}

	// Cookie path.
	req := httptest.NewRequest("GET", "/x", nil)
	req.AddCookie(&http.Cookie{Name: authdomain.SessionCookie, Value: mint("u-ok", "ok@example.com")})
	resp, _ := app.Test(req)
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || string(body) != "ok@example.com" {
		t.Fatalf("cookie session: %d %s", resp.StatusCode, body)
	}
	// Bearer fallback.
	req = httptest.NewRequest("GET", "/x", nil)
	req.Header.Set("Authorization", "Bearer "+mint("u-ok", "ok@example.com"))
	if resp, _ = app.Test(req); resp.StatusCode != 200 {
		t.Fatalf("bearer session: %d", resp.StatusCode)
	}
	// Valid token for a disabled user.
	req = httptest.NewRequest("GET", "/x", nil)
	req.AddCookie(&http.Cookie{Name: authdomain.SessionCookie, Value: mint("u-off", "off@example.com")})
	if resp, _ = app.Test(req); resp.StatusCode != 401 {
		t.Fatalf("disabled user: want 401, got %d", resp.StatusCode)
	}
	// Valid token for a deleted user.
	req = httptest.NewRequest("GET", "/x", nil)
	req.AddCookie(&http.Cookie{Name: authdomain.SessionCookie, Value: mint("u-gone", "gone@example.com")})
	if resp, _ = app.Test(req); resp.StatusCode != 401 {
		t.Fatalf("missing user: want 401, got %d", resp.StatusCode)
	}
	// Expired token.
	expired, _ := authenticator.CreateToken(secret, "u-ok", "ok@example.com", -time.Minute)
	req = httptest.NewRequest("GET", "/x", nil)
	req.AddCookie(&http.Cookie{Name: authdomain.SessionCookie, Value: expired})
	if resp, _ = app.Test(req); resp.StatusCode != 401 {
		t.Fatalf("expired: want 401, got %d", resp.StatusCode)
	}
}

func TestRequireAuth_DisabledPassesThrough(t *testing.T) {
	rt := runtimeutil.NewRuntime(t, &env.Config{
		GitHub: env.GitHubConfig{Disabled: true},
		AWS:    env.AWSConfig{Disabled: true},
		Auth:   env.AuthConfig{Disabled: true},
	})
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Get("/x", requireAuth(rt), func(c *fiber.Ctx) error { return c.SendStatus(200) })
	if resp, _ := app.Test(httptest.NewRequest("GET", "/x", nil)); resp.StatusCode != 200 {
		t.Fatalf("auth disabled: want 200, got %d", resp.StatusCode)
	}
}
