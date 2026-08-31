// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package server

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/filesystem"

	"github.com/yousysadmin/pacer"
)

// routerSource reads the Vue router definition, the source of truth
// for which top-level paths the SPA owns. The Vite build emits a
// single index.html, so the dist tree carries no per-route trace --
// the router source is the only place the route list exists.
func routerSource(t *testing.T) string {
	t.Helper()
	src, err := os.ReadFile("../../frontend/src/router/index.ts")
	if err != nil {
		t.Fatalf("read router source: %v", err)
	}
	return string(src)
}

// routePathRE matches the `path: '/jobs'` literals in router/index.ts.
// Only absolute paths count -- the root child (`path: ”`) is the
// overview page at "/", which spaAllowlist admits unconditionally.
var routePathRE = regexp.MustCompile(`path:\s*'(/[a-z0-9/_-]*)'`)

// TestSPARoutePrefixes_CoversAllRoutes asserts that every top-level
// route registered in the Vue router has a matching entry in
// spaRoutePrefixes. Without this, adding a new page in
// frontend/src/router/index.ts silently 404s in production -- exactly
// the regression we shipped once with the Svelte build (/settings was
// in the bundle but spaAllowlist rejected the deep link because the
// prefix wasn't listed).
//
// To fix a failing run: add the missing route to spaRoutePrefixes.
func TestSPARoutePrefixes_CoversAllRoutes(t *testing.T) {
	src := routerSource(t)

	allowed := make(map[string]bool, len(spaRoutePrefixes))
	for _, p := range spaRoutePrefixes {
		allowed[p] = true
	}

	found := make(map[string]bool)
	for _, m := range routePathRE.FindAllStringSubmatch(src, -1) {
		p := m[1]
		if p == "/" {
			continue
		}
		// Reduce to the first segment: a nested "/jobs/queue" route
		// is covered by the "/jobs" prefix.
		seg := "/" + strings.SplitN(strings.TrimPrefix(p, "/"), "/", 2)[0]
		found[seg] = true
	}
	if len(found) == 0 {
		t.Fatal("no route paths parsed from frontend/src/router/index.ts; " +
			"the regex in this test has drifted from the router's shape")
	}

	var missing []string
	for p := range found {
		if !allowed[p] {
			missing = append(missing, p)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("router paths missing from spaRoutePrefixes: %v\n"+
			"Add each to spaRoutePrefixes in routes.go.", missing)
	}

	// And the reverse: a prefix with no backing route is either a typo
	// or a leftover from a deleted page -- both worth flagging.
	var stale []string
	for _, p := range spaRoutePrefixes {
		if !found[p] {
			stale = append(stale, p)
		}
	}
	if len(stale) > 0 {
		t.Fatalf("spaRoutePrefixes entries with no route in router/index.ts: %v\n"+
			"Remove each from routes.go or add the route.", stale)
	}
}

// TestSPARoutePrefixes_WellFormed catches typos in entries (trailing
// slash, missing leading slash) that would break the prefix match.
func TestSPARoutePrefixes_WellFormed(t *testing.T) {
	for _, p := range spaRoutePrefixes {
		if !strings.HasPrefix(p, "/") {
			t.Errorf("spaRoutePrefixes entry %q must start with /", p)
		}
		if len(p) > 1 && strings.HasSuffix(p, "/") {
			t.Errorf("spaRoutePrefixes entry %q must not have trailing slash", p)
		}
	}
}

// newSPATestApp wires the same middleware chain registerRoutes uses
// for the SPA -- allowlist, cache-control, filesystem with the
// index.html fallback -- against the real embedded build. Skips when
// the frontend hasn't been built (fresh clone before make frontend).
func newSPATestApp(t *testing.T) (*fiber.App, fs.FS) {
	t.Helper()
	sub, err := fs.Sub(pacer.Frontend, "frontend/dist")
	if err != nil {
		t.Fatalf("fs.Sub: %v", err)
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		t.Skip("frontend/dist not built; run make frontend first")
	}
	app := fiber.New()
	app.Use("/", spaAllowlist(sub), spaCacheControl, filesystem.New(filesystem.Config{
		Root:         http.FS(sub),
		Index:        "index.html",
		NotFoundFile: "index.html",
	}))
	return app, sub
}

func spaGet(t *testing.T, app *fiber.App, path string) *http.Response {
	t.Helper()
	resp, err := app.Test(httptest.NewRequest(http.MethodGet, path, nil))
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return resp
}

// TestSPAServing_DeepLinkGetsShellNoCache: a route deep link must be
// answered with the index.html shell AND Cache-Control: no-cache. A
// cached shell would keep referencing pre-deploy hashed bundles for
// up to a day.
func TestSPAServing_DeepLinkGetsShellNoCache(t *testing.T) {
	app, _ := newSPATestApp(t)
	for _, path := range []string{"/", "/jobs", "/settings", "/jobs/some-id"} {
		resp := spaGet(t, app, path)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s: status %d, want 200", path, resp.StatusCode)
		}
		if cc := resp.Header.Get(fiber.HeaderCacheControl); cc != "no-cache" {
			t.Errorf("GET %s: Cache-Control %q, want no-cache", path, cc)
		}
	}
}

// TestSPAServing_MissingChunkIs404 pins the sharp edge of the
// NotFoundFile fallback: a request for a hashed bundle that no longer
// exists (stale shell after a deploy) must get a real 404, NOT the
// HTML shell at 200 -- the browser reports the latter as the opaque
// "Importing a module script failed".
func TestSPAServing_MissingChunkIs404(t *testing.T) {
	app, _ := newSPATestApp(t)
	resp := spaGet(t, app, "/assets/definitely-missing-chunk.js")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing chunk: status %d, want 404", resp.StatusCode)
	}
}

// TestSPAServing_ScannerProbeIs404: anything that is neither a real
// embedded file nor an owned route stays a 404 so scanner probes don't
// get a 200 with the full shell.
func TestSPAServing_ScannerProbeIs404(t *testing.T) {
	app, _ := newSPATestApp(t)
	for _, path := range []string{"/wp-admin", "/.git/config", "/xmlrpc.php"} {
		resp := spaGet(t, app, path)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s: status %d, want 404", path, resp.StatusCode)
		}
	}
}

// TestSPAServing_HashedAssetImmutable: real files under /assets/ carry
// the forever cache header.
func TestSPAServing_HashedAssetImmutable(t *testing.T) {
	app, sub := newSPATestApp(t)
	var asset string
	_ = fs.WalkDir(sub, "assets", func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && asset == "" {
			asset = "/" + path
		}
		return nil
	})
	if asset == "" {
		t.Fatal("no files under assets/ in the embedded build")
	}
	resp := spaGet(t, app, asset)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status %d, want 200", asset, resp.StatusCode)
	}
	want := "public, max-age=31536000, immutable"
	if cc := resp.Header.Get(fiber.HeaderCacheControl); cc != want {
		t.Errorf("GET %s: Cache-Control %q, want %q", asset, cc, want)
	}
}
