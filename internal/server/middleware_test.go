// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package server

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"

	"github.com/yousysadmin/pacer/internal/core/env"
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
