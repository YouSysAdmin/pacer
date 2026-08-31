// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package env

import (
	"strings"
	"testing"
)

// validBase returns a config that passes Validate with GitHub, AWS
// and auth all enabled. Each test mutates one field from here.
func validBase() *Config {
	return &Config{
		Server:   ServerConfig{PublicURL: "https://pacer.example.com"},
		GitHub:   GitHubConfig{AppID: 1, PrivateKeyPath: "/k.pem", WebhookSecret: "w", CallbackHMACSecret: "c"},
		Database: DatabaseConfig{Path: "pacer.db"},
		Auth: AuthConfig{
			JWTSecret: strings.Repeat("x", 32),
			Local:     AuthLocalConfig{Enabled: true, Email: "Ops@Example.com"},
		},
		Retention: RetentionConfig{AuditDays: 90, WebhookDays: 7, JobLogDays: 31},
	}
}

func TestValidate_HappyPath(t *testing.T) {
	c := validBase()
	if err := c.Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.Auth.Local.Email != "ops@example.com" {
		t.Errorf("email not normalized: %q", c.Auth.Local.Email)
	}
}

func TestValidate_Rejections(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*Config)
		want string
	}{
		{"aws disabled with github on", func(c *Config) { c.AWS.Disabled = true }, "aws.disabled requires github.disabled"},
		{"missing app id", func(c *Config) { c.GitHub.AppID = 0 }, "github.app_id"},
		{"missing public url", func(c *Config) { c.Server.PublicURL = "" }, "server.public_url required"},
		{"bare host public url", func(c *Config) { c.Server.PublicURL = "pacer.example.com" }, "server.public_url"},
		{"bad log level", func(c *Config) { c.Logging.Level = "warning" }, "logging.level"},
		{"bad log format", func(c *Config) { c.Logging.Format = "yaml" }, "logging.format"},
		{"missing db path", func(c *Config) { c.Database.Path = "" }, "database.path"},
		{"zero audit days", func(c *Config) { c.Retention.AuditDays = 0 }, "retention.audit_days"},
		{"zero job log days", func(c *Config) { c.Retention.JobLogDays = 0 }, "retention.job_log_days"},
		{"bad tls mode", func(c *Config) { c.Server.TLS.Mode = "magic" }, "server.tls.mode"},
		{"short jwt secret", func(c *Config) { c.Auth.JWTSecret = "short" }, "at least 32"},
		{"bad session ttl", func(c *Config) { c.Auth.SessionTTL = "soon" }, "auth.session_ttl"},
		{"no auth method", func(c *Config) { c.Auth.Local.Enabled = false }, "no method configured"},
		{"local without email", func(c *Config) { c.Auth.Local.Email = "" }, "auth.local.email"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := validBase()
			tc.mut(c)
			err := c.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestValidate_PublicURLTrailingSlashTrimmed(t *testing.T) {
	c := validBase()
	c.Server.PublicURL = "https://pacer.example.com/"
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if c.Server.PublicURL != "https://pacer.example.com" {
		t.Errorf("got %q", c.Server.PublicURL)
	}
}

func TestValidate_PublicURLOptionalWhenGitHubDisabled(t *testing.T) {
	c := validBase()
	c.GitHub.Disabled = true
	c.AWS.Disabled = true
	c.Server.PublicURL = ""
	if err := c.Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidate_OIDCDisablesLocal(t *testing.T) {
	c := validBase()
	c.Auth.OIDC = AuthOIDCConfig{
		Enabled:      true,
		Issuer:       "https://idp.example.com",
		ClientID:     "id",
		ClientSecret: "s",
		RedirectURL:  "https://pacer.example.com/api/auth/oidc/callback",
	}
	err := c.Validate()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.Auth.Local.Enabled {
		t.Error("local auth should be auto-disabled when OIDC is enabled")
	}
}
