// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package server

import (
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/filesystem"
	"github.com/gofiber/fiber/v2/middleware/limiter"

	"github.com/yousysadmin/pacer"
	"github.com/yousysadmin/pacer/internal/core/env"
	"github.com/yousysadmin/pacer/internal/domain/audit"
	"github.com/yousysadmin/pacer/internal/domain/auth"
	"github.com/yousysadmin/pacer/internal/domain/backup"
	jobdomain "github.com/yousysadmin/pacer/internal/domain/job"
	pooldomain "github.com/yousysadmin/pacer/internal/domain/pool"
	"github.com/yousysadmin/pacer/internal/domain/project"
	"github.com/yousysadmin/pacer/internal/domain/repo"
	"github.com/yousysadmin/pacer/internal/domain/runner"
	"github.com/yousysadmin/pacer/internal/domain/settings"
	"github.com/yousysadmin/pacer/internal/domain/stats"
	"github.com/yousysadmin/pacer/internal/domain/systemhealth"
	"github.com/yousysadmin/pacer/internal/domain/webhook"
)

func registerRoutes(app *fiber.App, rt *env.Runtime) {
	app.Get("/healthz", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok"})
	})

	api := app.Group("/api", noStoreCache)

	// GitHub webhook ingest - HMAC-verified at the handler.
	// Skipped when github.disabled=true (UI-only dev mode); GHApp is nil and
	// the orchestrator that consumes webhook-enqueued jobs isn't
	// running, so the route would 500 anyway.
	// NOT under requireAuth: GitHub authenticates via the X-Hub-Signature HMAC,
	// not a session cookie.
	if rt.GHApp != nil {
		wh := &webhook.Handler{Runtime: rt}
		api.Post("/webhook", wh.Receive)
	}

	// Auth endpoints - open (login obviously can't require an
	// existing session; logout / me are cheap enough to not gate).
	// /auth/login carries a per-IP rate limit so a leaked URL can't
	// be hammered for slow brute-force; bcrypt cost-12 already makes
	// it slow, this caps the absolute throughput.
	ah := &auth.Handler{Runtime: rt}
	loginLimiter := limiter.New(limiter.Config{
		Max:        10,
		Expiration: time.Minute,
	})
	api.Post("/auth/login", loginLimiter, ah.Login)
	api.Post("/auth/logout", ah.Logout)
	api.Get("/auth/me", requireAuth(rt), ah.Me)
	api.Get("/auth/info", ah.Info)

	// OIDC start + callback are open by definition (the user has no
	// session yet). The callback rate-limiter prevents replay loops
	// and is cheap insurance for a public endpoint.
	if rt.OIDC != nil {
		callbackLimiter := limiter.New(limiter.Config{
			Max:        30,
			Expiration: time.Minute,
		})
		api.Get("/auth/oidc/start", ah.OIDCStart)
		api.Get("/auth/oidc/callback", callbackLimiter, ah.OIDCCallback)
	}

	// Runner self-registration callbacks - HMAC-verified per request
	// against the callback token minted into the spawning instance's
	// user-data.
	// MUST be registered BEFORE the apiAuth Group below:
	// `api.Group("", requireAuth(rt))` registers requireAuth as
	// middleware on every /api/* route declared after that line.
	// Skipped in UI-only dev mode (Register depends on rt.GHApp to
	// mint JIT configs).
	if rt.GHApp != nil {
		rnH := &runner.Handler{
			Runtime: rt,
			GHApp:   rt.GHApp,
			HMACKey: []byte(rt.Config.GitHub.CallbackHMACSecret),
		}
		// /runner/bootstrap is authenticated by the global bootstrap
		// API token (Authorization: Bearer ...) rather than per-job
		// HMAC, since the in-instance script doesn't yet have its
		// per-job callback token at this point -- that's what
		// bootstrap returns.
		api.Post("/runner/bootstrap", rnH.Bootstrap)
		api.Post("/runner/register", rnH.Register)
		api.Post("/runner/complete", rnH.Complete)
		api.Post("/runner/error", rnH.Error)
	}

	// Everything below requires an authenticated operator when
	// auth.disabled is false.
	// Sub-group so we add the middleware once instead of per-route.
	apiAuth := api.Group("", requireAuth(rt))

	// Project CRUD.
	// Project is the logical grouping; EC2 launch settings live on its pools.
	ph := &project.Handler{Runtime: rt}
	apiAuth.Post("/projects", ph.Create)
	apiAuth.Get("/projects", ph.List)
	apiAuth.Get("/projects/:id", ph.Get)
	apiAuth.Put("/projects/:id", ph.Update)
	apiAuth.Delete("/projects/:id", ph.Delete)

	// Pool CRUD.
	// Pools are nested under a project for create + list;
	// individual pool reads / updates / deletes use a flat
	// /api/pools/:id surface.
	poolH := &pooldomain.Handler{Runtime: rt}
	apiAuth.Post("/projects/:project_id/pools", poolH.Create)
	apiAuth.Get("/projects/:project_id/pools", poolH.ListByProject)
	apiAuth.Get("/pools", poolH.List)
	apiAuth.Get("/pools/:id", poolH.Get)
	apiAuth.Put("/pools/:id", poolH.Update)
	apiAuth.Delete("/pools/:id", poolH.Delete)

	// Repo bindings.
	// full_name is "owner/name" - split into two
	// path segments so Fiber doesn't choke on the slash.
	rh := &repo.Handler{Runtime: rt}
	apiAuth.Post("/repos", rh.Bind)
	apiAuth.Get("/repos", rh.List)
	apiAuth.Get("/repos/:owner/:name", rh.Get)
	apiAuth.Delete("/repos/:owner/:name", rh.Unbind)
	apiAuth.Get("/projects/:id/repos", rh.ListByProject)

	// Jobs - read-only.
	// UI consumes /api/jobs?status=&limit= for
	// the dashboard table.
	jh := &jobdomain.Handler{Runtime: rt}
	apiAuth.Get("/jobs", jh.List)
	apiAuth.Get("/jobs/:id", jh.Get)

	// Stats - cost + activity rollup over completed jobs.
	// Read-only.
	// Best-effort estimates (launch-time price * elapsed time, no
	// EBS / data transfer).
	sh := &stats.Handler{Runtime: rt}
	apiAuth.Get("/stats", sh.Get)
	apiAuth.Get("/stats/timeseries", sh.Timeseries)
	apiAuth.Get("/stats/top-users", sh.TopUsers)

	// Audit log - read-only, paginated, time-windowed. Append-only;
	// the prune endpoint is the only mutation path and it logs its
	// own action so the trace survives the cleanup it describes.
	auh := &audit.Handler{Runtime: rt}
	apiAuth.Get("/audit", auh.List)
	apiAuth.Post("/audit/prune", auh.Prune)

	// Config backup - export the full project/pool/repo set as a
	// single JSON document, or import one back. Import is upsert by
	// name (no UUID match across systems); pools re-materialize their
	// LT through the same ec2lt path the pool handler uses.
	bh := &backup.Handler{Runtime: rt}
	apiAuth.Get("/backup/export", bh.Export)
	apiAuth.Post("/backup/import", bh.Import)

	// Settings - operator-managed DB-backed config. Today: just the
	// bootstrap API token (status read + rotate). Rotation also
	// re-materializes every pool's LT so the new token lands in
	// user-data without a manual pool-save click-fest.
	seH := &settings.Handler{Runtime: rt}
	apiAuth.Get("/settings/bootstrap-token", seH.GetBootstrapToken)
	apiAuth.Post("/settings/bootstrap-token/rotate", seH.RotateBootstrapToken)

	// System health - background-worker status surfaced for the UI
	// banner, plus a manual reconcile trigger that forces an
	// immediate reaper sweep (so an operator who just fixed an IAM
	// perm doesn't wait the full 60s for the next tick).
	shH := &systemhealth.Handler{Runtime: rt}
	apiAuth.Get("/health", shH.List)
	apiAuth.Post("/reconcile", shH.Reconcile)

	// SPA (embedded prerendered Svelte build).
	// Register LAST so
	// /api/* and /healthz match before this catch-all.
	// NotFoundFile
	// = index.html is the SPA-fallback shape SvelteKit expects.
	sub, err := fs.Sub(pacer.Frontend, "frontend/dist")
	if err != nil {
		app.Use("/", func(c *fiber.Ctx) error {
			return c.Status(500).SendString("spa embed unavailable: " + err.Error())
		})
		return
	}
	app.Use("/", spaAllowlist(sub), spaCacheControl, filesystem.New(filesystem.Config{
		Root:         http.FS(sub),
		Index:        "index.html",
		NotFoundFile: "index.html",
	}))
}

// spaRoutePrefixes is the set of top-level paths the SvelteKit client
// router owns. Each one matches itself exactly AND any subpath (for
// dynamic routes like /jobs/123). Add an entry here when a new
// top-level page lands in frontend/src/routes/.
//
// Anything outside these prefixes that isn't a real embedded asset
// returns 404 instead of falling back to index.html. Without that
// gate, scanner probes (/wp-admin, /.git/config, /xmlrpc.php) get a
// 200 with the full SPA shell, which both wastes bytes and registers
// as a hit in their tooling.
var spaRoutePrefixes = []string{
	"/projects",
	"/pools",
	"/jobs",
	"/repos",
	"/stats",
	"/audit",
	"/backup",
	"/login",
	"/settings",
}

// spaAllowlist gates the embedded filesystem middleware: only requests
// for a real embedded file OR an owned SPA route reach the
// filesystem layer. Everything else is 404'd up front.
//
// The embed file set is materialized once via fs.WalkDir at startup;
// per-request lookup is O(1) map access plus a short prefix loop.
// Mounted at app.Use("/", ...), so /healthz and /api/* (registered
// earlier) are unaffected.
func spaAllowlist(sub fs.FS) fiber.Handler {
	files := make(map[string]struct{})
	_ = fs.WalkDir(sub, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		files["/"+path] = struct{}{}
		return nil
	})
	return func(c *fiber.Ctx) error {
		p := c.Path()
		if p == "/" {
			return c.Next()
		}
		if _, ok := files[p]; ok {
			return c.Next()
		}
		for _, r := range spaRoutePrefixes {
			if p == r || strings.HasPrefix(p, r+"/") {
				return c.Next()
			}
		}
		return c.SendStatus(fiber.StatusNotFound)
	}
}

// noStoreCache forces Cache-Control: no-store on every /api/* response.
// JSON returned by the API is session-bound or otherwise dynamic;
// default browser heuristics could cache it on disk, leaking across
// users on shared machines or showing stale state after logout /
// privilege change. no-store is stricter than no-cache: it forbids
// storing entirely, in the browser AND in any intermediate proxy.
//
// Set BEFORE c.Next() so a specific handler can still override if it
// ever has reason to.
func noStoreCache(c *fiber.Ctx) error {
	c.Set(fiber.HeaderCacheControl, "no-store")
	return c.Next()
}

// spaCacheControl sets Cache-Control on the embedded SPA assets after
// the filesystem middleware writes the body. It runs only on requests
// the API didn't already handle (those return earlier in the chain),
// so /api/* responses are not touched.
//
// SvelteKit's adapter-static produces three classes of asset:
//
//   - /_app/immutable/* - content-hashed bundles (e.g.
//     /_app/immutable/chunks/B72M0WY1.js). Safe to cache forever; the
//     filename changes whenever the content changes. "immutable"
//     additionally tells the browser to skip even revalidation.
//   - *.html - per-route SPA shells. They reference the latest hashed
//     bundle; if a stale shell is served after a deploy the user
//     would load old JS pointing at API contracts that may have moved.
//     Use no-cache so the browser revalidates with If-Modified-Since,
//     getting a cheap 304 most of the time but always picking up new
//     deploys.
//   - everything else (fonts, logos) - stable filenames, no hash. A
//     1-day public cache balances bandwidth against the rare font/
//     logo replacement; the embedded ETag/Last-Modified will catch
//     updates on revalidation.
func spaCacheControl(c *fiber.Ctx) error {
	if err := c.Next(); err != nil {
		return err
	}
	path := c.Path()
	switch {
	case strings.HasPrefix(path, "/_app/immutable/"):
		c.Set(fiber.HeaderCacheControl, "public, max-age=31536000, immutable")
	case path == "/" || strings.HasSuffix(path, ".html"):
		c.Set(fiber.HeaderCacheControl, "no-cache")
	default:
		c.Set(fiber.HeaderCacheControl, "public, max-age=86400")
	}
	return nil
}
