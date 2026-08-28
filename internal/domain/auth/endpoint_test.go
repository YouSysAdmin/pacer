// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package auth_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/yousysadmin/pacer/internal/core/authenticator"
	"github.com/yousysadmin/pacer/internal/core/env"
	"github.com/yousysadmin/pacer/internal/core/validation"
	"github.com/yousysadmin/pacer/internal/domain/auth"
	auditmodel "github.com/yousysadmin/pacer/internal/models/audit"
	usermodel "github.com/yousysadmin/pacer/internal/models/user"
	"github.com/yousysadmin/pacer/internal/testutil/runtimeutil"
)

func init() { validation.Init() }

const jwtSecret = "0123456789abcdef0123456789abcdef"

func newApp(t *testing.T, cfg *env.Config) (*fiber.App, *env.Runtime) {
	t.Helper()
	rt := runtimeutil.NewRuntime(t, cfg)
	h := &auth.Handler{Runtime: rt}
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Post("/api/auth/login", h.Login)
	app.Post("/api/auth/logout", h.Logout)
	app.Get("/api/auth/me", h.Me)
	app.Get("/api/auth/info", h.Info)
	return app, rt
}

func localCfg(publicURL string) *env.Config {
	return &env.Config{
		Server: env.ServerConfig{PublicURL: publicURL},
		Auth:   env.AuthConfig{JWTSecret: jwtSecret, Local: env.AuthLocalConfig{Enabled: true, Email: "ops@example.com"}},
	}
}

func seedUser(t *testing.T, rt *env.Runtime, email, password string, disabled bool) {
	t.Helper()
	hash, err := authenticator.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.Store.User.Put(context.Background(), &usermodel.User{
		ID: "u-" + email, Email: email, PasswordHash: hash, Role: usermodel.RoleAdmin, Disabled: disabled,
	}); err != nil {
		t.Fatal(err)
	}
}

func postLogin(t *testing.T, app *fiber.App, email, password string) *http.Response {
	t.Helper()
	b, _ := json.Marshal(map[string]string{"email": email, "password": password})
	req := httptest.NewRequest("POST", "/api/auth/login", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func sessionCookie(resp *http.Response) *http.Cookie {
	for _, c := range resp.Cookies() {
		if c.Name == auth.SessionCookie {
			return c
		}
	}
	return nil
}

func TestLogin_HappyPath_SetsSecureCookieOverHTTPS(t *testing.T) {
	app, rt := newApp(t, localCfg("https://pacer.example.com"))
	seedUser(t, rt, "ops@example.com", "s3cret-pass", false)

	resp := postLogin(t, app, "  OPS@Example.com ", "s3cret-pass")
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	ck := sessionCookie(resp)
	if ck == nil || ck.Value == "" {
		t.Fatal("session cookie missing")
	}
	if !ck.HttpOnly || !ck.Secure || ck.SameSite != http.SameSiteStrictMode || ck.Path != "/" {
		t.Fatalf("cookie flags: %+v", ck)
	}
	claims, err := authenticator.ParseToken(jwtSecret, ck.Value)
	if err != nil || claims.UserID != "u-ops@example.com" {
		t.Fatalf("cookie is not a valid session: %v %+v", err, claims)
	}
	var body struct {
		User struct {
			Email        string `json:"email"`
			PasswordHash string `json:"password_hash"`
		} `json:"user"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if body.User.Email != "ops@example.com" || body.User.PasswordHash != "" {
		t.Fatalf("body leaks or is wrong: %+v", body)
	}
	u, _ := rt.Store.User.Get(context.Background(), "ops@example.com")
	if u.LastLoginAt == nil {
		t.Fatal("last_login_at not touched")
	}
}

func TestLogin_PlainHTTPCookieNotSecure(t *testing.T) {
	app, rt := newApp(t, localCfg("http://localhost:3000"))
	seedUser(t, rt, "ops@example.com", "pw-pw-pw", false)
	resp := postLogin(t, app, "ops@example.com", "pw-pw-pw")
	if ck := sessionCookie(resp); ck == nil || ck.Secure {
		t.Fatalf("cookie must not be Secure on plain http: %+v", ck)
	}
}

func TestLogin_Rejections_UniformAndAudited(t *testing.T) {
	app, rt := newApp(t, localCfg("https://pacer.example.com"))
	seedUser(t, rt, "ops@example.com", "right-pass", false)
	seedUser(t, rt, "off@example.com", "right-pass", true)

	for name, in := range map[string][2]string{
		"wrong password": {"ops@example.com", "wrong-pass"},
		"unknown email":  {"nobody@example.com", "right-pass"},
		"disabled user":  {"off@example.com", "right-pass"},
	} {
		resp := postLogin(t, app, in[0], in[1])
		if resp.StatusCode != 401 {
			t.Fatalf("%s: want 401, got %d", name, resp.StatusCode)
		}
		var b map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&b)
		if b["error"] != "invalid credentials" {
			t.Fatalf("%s: non-uniform error %v", name, b)
		}
		if sessionCookie(resp) != nil {
			t.Fatalf("%s: cookie must not be set", name)
		}
	}
	entries, err := rt.Store.Audit.List(context.Background(), auditmodel.ListFilter{Action: auditmodel.ActionLoginFailed, Limit: 10})
	if err != nil || len(entries) != 3 {
		t.Fatalf("want 3 login_failed audit rows, got %d (%v)", len(entries), err)
	}
	for _, e := range entries {
		if strings.Contains(e.Detail, "right-pass") || strings.Contains(e.Detail, "wrong-pass") {
			t.Fatalf("audit row leaks password material: %s", e.Detail)
		}
	}
}

func TestLogin_ValidationAndDisabledLocal(t *testing.T) {
	app, _ := newApp(t, localCfg("https://pacer.example.com"))
	if resp := postLogin(t, app, "not-an-email", "x"); resp.StatusCode != 400 {
		t.Fatalf("bad email: want 400, got %d", resp.StatusCode)
	}
	if resp := postLogin(t, app, "a@b.co", ""); resp.StatusCode != 400 {
		t.Fatalf("empty password: want 400, got %d", resp.StatusCode)
	}
	cfg := localCfg("https://pacer.example.com")
	cfg.Auth.Local.Enabled = false
	app, _ = newApp(t, cfg)
	if resp := postLogin(t, app, "a@b.co", "x"); resp.StatusCode != 400 {
		t.Fatalf("local disabled: want 400, got %d", resp.StatusCode)
	}
}

func TestLogout_ExpiresCookie(t *testing.T) {
	app, _ := newApp(t, localCfg("https://pacer.example.com"))
	resp, _ := app.Test(httptest.NewRequest("POST", "/api/auth/logout", nil), -1)
	if resp.StatusCode != 204 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	ck := sessionCookie(resp)
	if ck == nil || ck.Value != "" || !ck.Expires.Before(time.Now()) {
		t.Fatalf("logout must clear the cookie with a past expiry: %+v", ck)
	}
}

func TestMe_Shapes(t *testing.T) {
	app, _ := newApp(t, localCfg("https://pacer.example.com"))
	resp, _ := app.Test(httptest.NewRequest("GET", "/api/auth/me", nil), -1)
	if resp.StatusCode != 401 {
		t.Fatalf("no session: want 401, got %d", resp.StatusCode)
	}
	cfg := localCfg("")
	cfg.Auth.Disabled = true
	app, _ = newApp(t, cfg)
	resp, _ = app.Test(httptest.NewRequest("GET", "/api/auth/me", nil), -1)
	var b map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&b)
	if resp.StatusCode != 200 || b["auth_disabled"] != true {
		t.Fatalf("auth disabled: %d %v", resp.StatusCode, b)
	}
}

func TestInfo_Shapes(t *testing.T) {
	app, _ := newApp(t, localCfg("https://pacer.example.com"))
	resp, _ := app.Test(httptest.NewRequest("GET", "/api/auth/info", nil), -1)
	var b map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&b)
	if b["local_enabled"] != true || b["oidc_enabled"] != false {
		t.Fatalf("info %v", b)
	}
	if _, ok := b["oidc_label"]; ok {
		t.Fatal("oidc_label must be absent without a provider")
	}
}
