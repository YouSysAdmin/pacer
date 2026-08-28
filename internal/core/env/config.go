// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// Package env owns Config (Viper-backed YAML loader) and the
// per-process Runtime that handlers receive through the *Runtime
// field on each domain Handler.
package env

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/spf13/viper"

	"github.com/yousysadmin/pacer/internal/core/tlsutils"
)

type Config struct {
	Server    ServerConfig    `mapstructure:"server"`
	AWS       AWSConfig       `mapstructure:"aws"`
	GitHub    GitHubConfig    `mapstructure:"github"`
	Database  DatabaseConfig  `mapstructure:"database"`
	Logging   LoggingConfig   `mapstructure:"logging"`
	Auth      AuthConfig      `mapstructure:"auth"`
	Retention RetentionConfig `mapstructure:"retention"`
}

// RetentionConfig is the YAML default for DB-row retention. The
// operator can override either field at runtime via the Settings UI;
// the YAML value is the floor everyone starts at. The pruner reads
// the effective value (DB override else this default) on every tick.
//
// Defaults applied in Load (audit_days=90, webhook_days=7). Both
// must be >= 1; Validate rejects zero / negative.
type RetentionConfig struct {
	// AuditDays is how long audit_log rows are kept before the
	// daily pruner deletes them. Default 90.
	AuditDays int `mapstructure:"audit_days"`
	// WebhookDays is how long webhook_deliveries rows are kept.
	// Default 7. GitHub redelivery windows are minutes, so this
	// only matters for operator debugging.
	WebhookDays int `mapstructure:"webhook_days"`
}

// AuthConfig gates the operator-console auth surface. When enabled,
// /api/projects + /api/pools + /api/repos + /api/jobs + /api/stats
// require a session cookie. Webhook + runner callbacks stay HMAC-only
// regardless.
type AuthConfig struct {
	Disabled bool `mapstructure:"disabled"`

	// JWTSecret signs HS256 session JWTs. Required when !Disabled.
	// Generate with `openssl rand -hex 32`.
	JWTSecret string `mapstructure:"jwt_secret"`

	// SessionTTL is the cookie lifetime. Empty -> 12h.
	SessionTTL string `mapstructure:"session_ttl"`

	Local AuthLocalConfig `mapstructure:"local"`
	OIDC  AuthOIDCConfig  `mapstructure:"oidc"`
}

type AuthLocalConfig struct {
	Enabled bool `mapstructure:"enabled"`
	// Email is the bootstrap user's address. On first run with an
	// empty users table, the tool inserts a user with this email and
	// a generated password (logged to stderr ONCE).
	Email string `mapstructure:"email"`
}

type AuthOIDCConfig struct {
	Enabled      bool     `mapstructure:"enabled"`
	Issuer       string   `mapstructure:"issuer"`
	ClientID     string   `mapstructure:"client_id"`
	ClientSecret string   `mapstructure:"client_secret"`
	RedirectURL  string   `mapstructure:"redirect_url"`
	Scopes       []string `mapstructure:"scopes"`

	// RequireEmailVerified rejects sign-ins whose ID token doesn't
	// carry email_verified=true. Default true; flip to false only for
	// IdPs that don't surface the claim.
	RequireEmailVerified *bool `mapstructure:"require_email_verified"`

	// Allowlists. All-empty means "any user the IdP authenticates is
	// admitted" (the IdP is the gate). Domains compared case-insensitive
	// against the email's host part; emails compared case-insensitive
	// after lowercase+trim.
	AllowedDomains []string `mapstructure:"allowed_domains"`
	AllowedEmails  []string `mapstructure:"allowed_emails"`

	// Group allowlist. GroupsClaim names the claim key (varies by
	// IdP: "groups" for most, "cognito:groups" for Cognito, "roles"
	// for some Keycloak setups). Empty -> group check disabled.
	GroupsClaim   string   `mapstructure:"groups_claim"`
	AllowedGroups []string `mapstructure:"allowed_groups"`
}

type LoggingConfig struct {
	Level  string `mapstructure:"level"`
	Format string `mapstructure:"format"`
	Output string `mapstructure:"output"`
	Color  bool   `mapstructure:"color"`
}

type ServerConfig struct {
	Addr      string          `mapstructure:"addr"`
	PublicURL string          `mapstructure:"public_url"`
	TLS       tlsutils.Config `mapstructure:"tls"`

	// TrustedProxies is the list of proxy IPs / CIDRs Fiber will trust
	// for X-Forwarded-For. Empty = c.IP() returns the direct peer's
	// address (the right answer for direct-internet exposure). Set this
	// to your load-balancer / reverse-proxy CIDRs when terminating TLS
	// upstream so the rate limiter, audit log, and slog access log all
	// see the real client IP. Examples: ["127.0.0.1", "10.0.0.0/8",
	// "::1"].
	TrustedProxies []string `mapstructure:"trusted_proxies"`
}

// AWSConfig
// `.Disabled` flips the tool into a no-AWS mode for local UI
// dev. When true: skip credential resolution, leave Runtime.EC2 nil,
// skip launch-template materialization (placeholder LT id stamped
// instead). Combine with github.disabled for full UI-only dev.
type AWSConfig struct {
	Disabled bool   `mapstructure:"disabled"`
	Region   string `mapstructure:"region"`
	Profile  string `mapstructure:"profile"`
}

// GitHubConfig
// `.Disabled` flips the tool into UI-only mode: webhook +
// runner endpoints aren't registered, orchestrator + reaper don't
// start, App private key isn't loaded. Every other github.* field
// becomes optional.
type GitHubConfig struct {
	Disabled           bool   `mapstructure:"disabled"`
	AppID              int64  `mapstructure:"app_id"`
	PrivateKeyPath     string `mapstructure:"private_key_path"`
	WebhookSecret      string `mapstructure:"webhook_secret"`
	CallbackHMACSecret string `mapstructure:"callback_hmac_secret"`
}

type DatabaseConfig struct {
	Engine string `mapstructure:"engine"`
	Path   string `mapstructure:"path"`
}

// Load reads the YAML file at path (or ./pacer.yaml when path is
// empty), merges in PACER_*-prefixed environment overrides, and
// returns the resolved Config. A missing file is not an error when
// env vars supply the required values.
func Load(path string) (*Config, error) {
	v := viper.New()
	if path != "" {
		v.SetConfigFile(path)
	} else {
		v.AddConfigPath(".")
		v.SetConfigName("pacer")
		v.SetConfigType("yaml")
	}
	v.SetEnvPrefix("PACER")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	v.SetDefault("server.addr", ":3000")
	v.SetDefault("aws.region", "us-east-1")
	v.SetDefault("database.engine", "sqlite")
	v.SetDefault("database.path", "pacer.db")
	v.SetDefault("logging.level", "info")
	v.SetDefault("logging.format", "json")
	v.SetDefault("logging.output", "stdout")
	v.SetDefault("retention.audit_days", 90)
	v.SetDefault("retention.webhook_days", 7)

	if err := v.ReadInConfig(); err != nil {
		if _, ok := errors.AsType[viper.ConfigFileNotFoundError](err); !ok {
			return nil, fmt.Errorf("read config: %w", err)
		}
	}

	var c Config
	if err := v.Unmarshal(&c); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}
	return &c, nil
}

// Validate checks required fields. Defaults handle the rest. When
// github.disabled is true every github.* field becomes optional.
func (c *Config) Validate() error {
	// The orchestrator and reaper need a live EC2 client. Running them
	// without one would nil-deref on the first claimed job.
	if c.AWS.Disabled && !c.GitHub.Disabled {
		return fmt.Errorf("aws.disabled requires github.disabled: true (orchestrator cannot spawn without EC2)")
	}
	if !c.GitHub.Disabled {
		if c.GitHub.AppID == 0 {
			return fmt.Errorf("github.app_id required (set github.disabled: true to skip GitHub integration for local UI dev)")
		}
		if c.GitHub.PrivateKeyPath == "" {
			return fmt.Errorf("github.private_key_path required")
		}
		if c.GitHub.WebhookSecret == "" {
			return fmt.Errorf("github.webhook_secret required")
		}
		if c.GitHub.CallbackHMACSecret == "" {
			return fmt.Errorf("github.callback_hmac_secret required (signs runner self-registration tokens)")
		}
	}
	// public_url is baked into every LT user-data and drives the
	// Secure cookie flag, so when set it must be a real http(s) URL.
	if !c.GitHub.Disabled && c.Server.PublicURL == "" {
		return fmt.Errorf("server.public_url required (spawned instances POST callbacks here)")
	}
	if c.Server.PublicURL != "" {
		if err := validateHTTPSURL("server.public_url", c.Server.PublicURL); err != nil {
			return err
		}
		c.Server.PublicURL = strings.TrimRight(c.Server.PublicURL, "/")
	}
	switch strings.ToLower(c.Logging.Level) {
	case "", "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("logging.level %q invalid: want debug, info, warn, or error", c.Logging.Level)
	}
	switch strings.ToLower(c.Logging.Format) {
	case "", "json", "text":
	default:
		return fmt.Errorf("logging.format %q invalid: want json or text", c.Logging.Format)
	}
	if c.Database.Path == "" {
		return fmt.Errorf("database.path required")
	}
	// Retention defaults are applied in Load; Validate enforces the
	// minimum so a misconfigured override can't wipe today's rows.
	if c.Retention.AuditDays < 1 {
		return fmt.Errorf("retention.audit_days must be >= 1 (got %d)", c.Retention.AuditDays)
	}
	if c.Retention.WebhookDays < 1 {
		return fmt.Errorf("retention.webhook_days must be >= 1 (got %d)", c.Retention.WebhookDays)
	}
	switch c.Server.TLS.Mode {
	case "", tlsutils.ModeNone, tlsutils.ModeManual, tlsutils.ModeSelf, tlsutils.ModeACME:
	default:
		return fmt.Errorf("server.tls.mode %q invalid: want one of %q, %q, %q, %q",
			c.Server.TLS.Mode, tlsutils.ModeNone, tlsutils.ModeManual, tlsutils.ModeSelf, tlsutils.ModeACME)
	}
	if !c.Auth.Disabled {
		if c.Auth.JWTSecret == "" {
			return fmt.Errorf("auth.jwt_secret required when auth is enabled (set auth.disabled: true to skip)")
		}
		// HS256 is keyed on the raw bytes of the secret; under 32 is
		// brute-forceable. Fail fast on weak keys.
		if len(c.Auth.JWTSecret) < 32 {
			return fmt.Errorf("auth.jwt_secret must be at least 32 characters (generate with `openssl rand -hex 32`)")
		}
		if c.Auth.SessionTTL != "" {
			d, err := time.ParseDuration(c.Auth.SessionTTL)
			if err != nil || d <= 0 {
				return fmt.Errorf("auth.session_ttl %q invalid: want a Go duration like 12h or 30m", c.Auth.SessionTTL)
			}
		}
		if !c.Auth.Local.Enabled && !c.Auth.OIDC.Enabled {
			return fmt.Errorf("auth enabled but no method configured: enable auth.local or auth.oidc")
		}
		// OIDC takes precedence: auto-disable local when both are on.
		// Local is intended for first-setup / break-glass; flip the
		// YAML and restart for emergency fallback.
		if c.Auth.OIDC.Enabled && c.Auth.Local.Enabled {
			c.Auth.Local.Enabled = false
		}
		if c.Auth.Local.Enabled {
			if c.Auth.Local.Email == "" {
				return fmt.Errorf("auth.local.email required (bootstrap user email)")
			}
			c.Auth.Local.Email = strings.TrimSpace(strings.ToLower(c.Auth.Local.Email))
		}
		if c.Auth.OIDC.Enabled {
			if c.Auth.OIDC.Issuer == "" {
				return fmt.Errorf("auth.oidc.issuer required (e.g. https://accounts.example.com)")
			}
			if err := validateHTTPSURL("auth.oidc.issuer", c.Auth.OIDC.Issuer); err != nil {
				return err
			}
			if c.Auth.OIDC.ClientID == "" {
				return fmt.Errorf("auth.oidc.client_id required")
			}
			if c.Auth.OIDC.ClientSecret == "" {
				return fmt.Errorf("auth.oidc.client_secret required (set via PACER_AUTH_OIDC_CLIENT_SECRET to keep it out of YAML)")
			}
			if c.Auth.OIDC.RedirectURL == "" {
				return fmt.Errorf("auth.oidc.redirect_url required (e.g. https://pacer.example.com/api/auth/oidc/callback)")
			}
			if err := validateHTTPSURL("auth.oidc.redirect_url", c.Auth.OIDC.RedirectURL); err != nil {
				return err
			}
			if err := requireDistinctHosts(c.Auth.OIDC.Issuer, c.Auth.OIDC.RedirectURL); err != nil {
				return err
			}
			if len(c.Auth.OIDC.Scopes) == 0 {
				c.Auth.OIDC.Scopes = []string{"openid", "email", "profile"}
			}
			if c.Auth.OIDC.RequireEmailVerified == nil {
				c.Auth.OIDC.RequireEmailVerified = new(true)
			}
			if len(c.Auth.OIDC.AllowedGroups) > 0 && c.Auth.OIDC.GroupsClaim == "" {
				return fmt.Errorf("auth.oidc.allowed_groups set but groups_claim is empty (e.g. 'groups', 'cognito:groups', 'roles')")
			}
			c.Auth.OIDC.AllowedDomains = lowerTrimAll(c.Auth.OIDC.AllowedDomains)
			c.Auth.OIDC.AllowedEmails = lowerTrimAll(c.Auth.OIDC.AllowedEmails)
			// Reject "@example.com"-style entries in allowed_emails:
			// they look like domain wildcards but Admit does an exact
			// string match. Catching this at config time prevents an
			// operator typo from silently widening the allowlist (an
			// entry like "@example.com" never matches a real email
			// address, but a user-supplied "user@@example.com" or
			// similar would).
			for _, e := range c.Auth.OIDC.AllowedEmails {
				if strings.HasPrefix(e, "@") {
					return fmt.Errorf("auth.oidc.allowed_emails entry %q starts with '@' (use auth.oidc.allowed_domains for domain-wide allowlists)", e)
				}
				if !strings.Contains(e, "@") {
					return fmt.Errorf("auth.oidc.allowed_emails entry %q is not an email address", e)
				}
			}
			// Lower groups too. Most IdPs preserve the casing the
			// admin entered (Cognito + Keycloak ship UPPERCASE roles
			// out of the box) and the YAML rarely matches; treating
			// "Admins" and "admins" as the same group is the
			// least-surprise behavior. Group names from the claim are
			// lowered at compare time in oidc/allow.go.
			c.Auth.OIDC.AllowedGroups = lowerTrimAll(c.Auth.OIDC.AllowedGroups)
		}
	}
	return nil
}

// validateHTTPSURL fails fast with a clear, field-named error when
// an operator drops a bare identifier (a Cognito pool ID, an Okta
// org name, etc.) into a field that needs a full URL. Without this
// the misconfiguration only surfaces deep inside go-oidc's
// discovery dial: "unsupported protocol scheme """ -- which doesn't
// point the operator at the offending YAML key.
//
// Accepts http for local dev (e.g. Keycloak on localhost during
// integration tests); operators running prod should keep https.
func validateHTTPSURL(field, raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%s %q is not a valid URL: %w", field, raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("%s %q must include a scheme (http:// or https://); got %q -- looks like you passed a bare identifier instead of an issuer URL", field, raw, u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("%s %q has no host part; expected something like https://accounts.example.com", field, raw)
	}
	return nil
}

// requireDistinctHosts catches the recurring operator mistake of
// pointing auth.oidc.issuer at pacer's own URL. issuer must be the
// IdP (Cognito, Auth0, Okta, ...); redirect_url must be pacer.
// Sharing a host means OIDC discovery fetches /.well-known/... from
// pacer itself, hits 404 / connection-refused, and the deep
// "dial tcp ...: connect refused" error doesn't point the operator
// at the right field.
func requireDistinctHosts(issuer, redirectURL string) error {
	iu, err := url.Parse(issuer)
	if err != nil {
		return nil // already validated upstream; bail to avoid double-erroring
	}
	ru, err := url.Parse(redirectURL)
	if err != nil {
		return nil
	}
	if strings.EqualFold(iu.Host, ru.Host) {
		return fmt.Errorf("auth.oidc.issuer (%q) and auth.oidc.redirect_url (%q) share the same host -- issuer must be the IdP (Cognito / Auth0 / Okta / Google / Keycloak), redirect_url must be pacer's own URL + /api/auth/oidc/callback", issuer, redirectURL)
	}
	return nil
}

func lowerTrimAll(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(strings.ToLower(s))
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}
