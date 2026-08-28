// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package tlsutils

import (
	"crypto/tls"
	"fmt"
)

// Mode names accepted by Config.Mode. Anything else errors at Build()
// so typos like `self-signed` or `letsencrypt` surface at startup
// instead of silently falling through to plain HTTP.
const (
	ModeNone   = "none"
	ModeManual = "manual"
	ModeSelf   = "self"
	ModeACME   = "acme"
)

// Config is the operator-facing TLS surface. ACME nests its block
// because it has four fields, while manual/self stay flat.
type Config struct {
	// Mode selects the TLS strategy. Empty is treated as ModeNone.
	Mode string `mapstructure:"mode" yaml:"mode"`

	// Manual mode - paths to a PEM-encoded cert + key pair.
	Cert string `mapstructure:"cert" yaml:"cert,omitempty"`
	Key  string `mapstructure:"key"  yaml:"key,omitempty"`

	// Self-signed mode - FQDN becomes a SubjectAltName alongside
	// "localhost". Alg is "ed25519" or "rsa" (empty -> rsa).
	FQDN string `mapstructure:"fqdn" yaml:"fqdn,omitempty"`
	Alg  string `mapstructure:"alg"  yaml:"alg,omitempty"`

	// ACME mode - Let's Encrypt via golang.org/x/crypto/acme/autocert.
	ACME ACMEBlock `mapstructure:"acme" yaml:"acme,omitempty"`
}

type ACMEBlock struct {
	Email string `mapstructure:"email" yaml:"email,omitempty"`

	// CacheDir persists issued certs + keys across restarts. Give it a
	// real volume in production so reboots don't re-request and burn
	// through Let's Encrypt rate limits. Empty -> ./certs.
	CacheDir string `mapstructure:"cache_dir" yaml:"cache_dir,omitempty"`

	// HTTPAddr handles the HTTP-01 challenge. Must resolve to port 80
	// externally - LE won't negotiate a custom port. Empty -> :80.
	HTTPAddr string `mapstructure:"http_addr" yaml:"http_addr,omitempty"`

	// Hosts whitelists FQDNs the manager will obtain certs for. Stops
	// an attacker pointing example.com at the server and burning
	// through the rate limit on bogus hostnames. At least one required.
	Hosts []string `mapstructure:"hosts" yaml:"hosts,omitempty"`
}

func (c Config) Enabled() bool {
	switch c.Mode {
	case "", ModeNone:
		return false
	default:
		return true
	}
}

// Build resolves the config into a *tls.Config ready for net.Listener
// wrapping. Returns (nil, nil) for ModeNone so callers can branch on
// the result without a separate Enabled check. Per-mode validation
// lives here so `pacer version` skips cert-path / host checks.
func Build(c Config) (*tls.Config, error) {
	switch c.Mode {
	case "", ModeNone:
		return nil, nil
	case ModeManual:
		if c.Cert == "" || c.Key == "" {
			return nil, fmt.Errorf("server.tls.mode=manual requires both server.tls.cert and server.tls.key")
		}
		return LoadManualTLS(ManualTLS{CertFile: c.Cert, KeyFile: c.Key})
	case ModeSelf:
		if c.FQDN == "" {
			return nil, fmt.Errorf("server.tls.mode=self requires server.tls.fqdn (SAN for the generated cert)")
		}
		return SelfSignedTLS(c.FQDN, c.Alg)
	case ModeACME:
		if len(c.ACME.Hosts) == 0 {
			return nil, fmt.Errorf("server.tls.mode=acme requires at least one host under server.tls.acme.hosts")
		}
		cfg := AutoTLS(ACME{
			Enable:   true,
			Email:    c.ACME.Email,
			CacheDir: c.ACME.CacheDir,
			HTTPAddr: c.ACME.HTTPAddr,
			Hosts:    c.ACME.Hosts,
		})
		if cfg == nil {
			return nil, fmt.Errorf("autocert manager failed to build for hosts %v", c.ACME.Hosts)
		}
		return cfg, nil
	default:
		return nil, fmt.Errorf("unknown server.tls.mode %q: want one of %q, %q, %q, %q", c.Mode, ModeNone, ModeManual, ModeSelf, ModeACME)
	}
}
