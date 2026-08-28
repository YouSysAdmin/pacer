// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// Package server is the HTTP edge.
// Fiber app + middleware + route registration.
// Domain logic lives in internal/domain/<thing>/.
package server

import (
	"crypto/tls"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/gofiber/fiber/v2"
	slogfiber "github.com/samber/slog-fiber"

	"github.com/yousysadmin/pacer/internal/core/env"
	"github.com/yousysadmin/pacer/internal/core/response"
	"github.com/yousysadmin/pacer/internal/core/tlsutils"
)

type Server struct {
	app    *fiber.App
	rt     *env.Runtime
	tlsCfg *tls.Config // nil = serve plain HTTP
}

type Options struct {
	Runtime *env.Runtime
}

// bodyLimit caps the request body across the whole API. GitHub
// webhook bodies are well under 256 KiB. The runner /error endpoint
// has its own 256 KiB cap that's checked before this one. 1 MiB is
// generous headroom for any operator-driven JSON CRUD without
// inviting memory-pressure DoS from a 10 MiB-per-request flood.
const bodyLimit = 1 * 1024 * 1024

// New builds the Fiber app and resolves the TLS config in one step.
// We materialize *tls.Config here (rather than at Start) so a bad
// cert path / missing ACME hosts surfaces synchronously during boot
// before the orchestrator + reaper goroutines kick off.
func New(opts Options) (*Server, error) {
	tlsCfg, err := tlsutils.Build(opts.Runtime.Config.Server.TLS)
	if err != nil {
		return nil, fmt.Errorf("tls: %w", err)
	}

	fiberCfg := fiber.Config{
		DisableStartupMessage: true,
		BodyLimit:             bodyLimit,
	}
	// Fiber treats the TCP peer's address as c.IP() by default.
	// When operators terminate TLS at a reverse proxy / ALB and only
	// trusted hosts ever connect to us, we honor X-Forwarded-For but
	// strictly limit it to the trusted-proxy list so an unproxied
	// caller cannot spoof their source IP through the header.
	if proxies := opts.Runtime.Config.Server.TrustedProxies; len(proxies) > 0 {
		fiberCfg.EnableTrustedProxyCheck = true
		fiberCfg.TrustedProxies = proxies
		fiberCfg.ProxyHeader = fiber.HeaderXForwardedFor
	}

	app := fiber.New(fiberCfg)
	app.Use(safeRecover)
	app.Use(securityHeaders)
	if tlsCfg != nil {
		app.Use(hstsHeader)
	}
	app.Use(slogfiber.NewWithFilters(opts.Runtime.Log, accessLogFilters()...))

	registerRoutes(app, opts.Runtime)
	return &Server{app: app, rt: opts.Runtime, tlsCfg: tlsCfg}, nil
}

// Start blocks serving HTTP or HTTPS depending on whether TLS was
// configured.
// Returns Fiber's listener error verbatim so serve.go's
// errCh path keeps working unchanged.
func (s *Server) Start() error {
	addr := s.rt.Config.Server.Addr
	if s.tlsCfg != nil {
		mode := s.rt.Config.Server.TLS.Mode
		ln, err := tls.Listen("tcp", addr, s.tlsCfg)
		if err != nil {
			return fmt.Errorf("tls listen %s: %w", addr, err)
		}
		slog.Info("server start", "addr", addr, "tls", mode)
		return s.app.Listener(ln)
	}
	slog.Info("server start", "addr", addr, "tls", "none")
	return s.app.Listen(addr)
}

func (s *Server) Shutdown() error {
	return s.app.Shutdown()
}

// safeRecover catches panics inside any downstream handler, logs the
// reason + stack trace server-side, and returns a generic 500 to the
// caller. Fiber's stock recover.New writes the panic value into the
// response body, which leaks internal state (paths, types, sometimes
// secrets in *fmt.wrapError values) to whoever triggered the panic.
func safeRecover(c *fiber.Ctx) (err error) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("panic recovered",
				"reason", fmt.Sprintf("%v", r),
				"path", c.Path(),
				"method", c.Method(),
				"client_ip", c.IP(),
				"stack", string(debug.Stack()),
			)
			err = response.Internal(c, nil)
		}
	}()
	return c.Next()
}

// securityHeaders sets baseline browser-side defenses on every
// response. The headers are cheap, idempotent, and don't depend on
// path - a bare 401 from an unprotected handler still gets them.
//
// CSP carve-outs and why each one exists:
//
//   - script-src 'unsafe-inline': SvelteKit's prerendered HTML emits
//     an inline hydration bootstrap (see frontend/dist/index.html).
//     Its body embeds the hashed asset paths so it changes every
//     frontend build, making a sha256- hash brittle. The SPA's XSS
//     surface is small - a single-operator console where all
//     rendering goes through Svelte's escaping - but this is the
//     trade we're explicit about.
//   - style-src 'unsafe-inline': SvelteKit emits scoped style
//     attributes. Without this, hydration breaks visually.
//
// Fonts (Inter Tight + Space Mono) are self-hosted under /fonts/.
// No third-party origin is allowlisted for fonts or styles so the
// SPA has zero runtime network dependency outside its own origin.
// 'unsafe-eval' is OFF for both script-src and style-src so any
// future bundling that needs eval has to opt in deliberately.
func securityHeaders(c *fiber.Ctx) error {
	c.Set("X-Content-Type-Options", "nosniff")
	c.Set("X-Frame-Options", "DENY")
	c.Set("Referrer-Policy", "strict-origin-when-cross-origin")
	c.Set("Cross-Origin-Opener-Policy", "same-origin")
	c.Set("Permissions-Policy", "geolocation=(), microphone=(), camera=()")
	c.Set("Content-Security-Policy",
		"default-src 'self'; "+
			"script-src 'self' 'unsafe-inline'; "+
			"style-src 'self' 'unsafe-inline'; "+
			"img-src 'self' data:; "+
			"font-src 'self'; "+
			"connect-src 'self'; "+
			"frame-ancestors 'none'; "+
			"base-uri 'self'; "+
			"form-action 'self'")
	return c.Next()
}

// hstsHeader is added only when this process terminates TLS, so a
// plain-HTTP deployment behind a proxy is not accidentally pinned.
func hstsHeader(c *fiber.Ctx) error {
	c.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
	return c.Next()
}

// accessLogFilters returns the slog-fiber filters applied to every
// request. Each filter returns false to drop the entry, true to keep.
//
// /api/auth/me is the SPA's "is the user logged in?" probe fired on
// every page load. A 401 there is the expected answer when there's
// no session cookie, not a security event. slog-fiber's default
// behavior promotes any 4xx to WARN, which floods the log on every
// unauthenticated visit to the login page. Drop just that exact
// (path, status) pair so login 401s and 401s on protected endpoints
// (projects/pools/...) still log -- those signal credential stuffing
// or someone probing the API.
func accessLogFilters() []slogfiber.Filter {
	return []slogfiber.Filter{
		func(c *fiber.Ctx) bool {
			return !(c.Path() == "/api/auth/me" && c.Response().StatusCode() == http.StatusUnauthorized)
		},
	}
}
