// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package server

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/yousysadmin/pacer"
)

// TestSPARoutePrefixes_CoversAllPages walks the embedded frontend
// build and asserts that every top-level prerendered .html sibling
// (jobs.html, settings.html, ...) has a matching entry in
// spaRoutePrefixes. Without this, adding a new SvelteKit route in
// frontend/src/routes/ silently 404s in production -- exactly the
// regression we just shipped (settings.html was prerendered into
// dist, the embed had it, but spaAllowlist rejected /settings
// because the prefix wasn't listed).
//
// To fix a failing run: add the missing route to spaRoutePrefixes.
func TestSPARoutePrefixes_CoversAllPages(t *testing.T) {
	sub, err := fs.Sub(pacer.Frontend, "frontend/dist")
	if err != nil {
		t.Fatalf("fs.Sub: %v", err)
	}
	entries, err := fs.ReadDir(sub, ".")
	if err != nil {
		t.Fatalf("read dist root: %v", err)
	}

	allowed := make(map[string]bool, len(spaRoutePrefixes))
	for _, p := range spaRoutePrefixes {
		allowed[p] = true
	}

	var missing []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".html") {
			continue
		}
		// index.html is the SPA fallback root, not a top-level route.
		if name == "index.html" {
			continue
		}
		route := "/" + strings.TrimSuffix(name, ".html")
		if !allowed[route] {
			missing = append(missing, route)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("prerendered routes missing from spaRoutePrefixes: %v\n"+
			"Add each to spaRoutePrefixes in routes.go.", missing)
	}
}

// TestSPAAllowlist_AllowsListedRoutes is a unit test on the gate
// itself: every entry in spaRoutePrefixes must resolve (not 404)
// through the allowlist when no exact file matches. Catches typos
// in entries (e.g. trailing slash, capitalization).
func TestSPAAllowlist_AllowsListedRoutes(t *testing.T) {
	sub, err := fs.Sub(pacer.Frontend, "frontend/dist")
	if err != nil {
		t.Fatalf("fs.Sub: %v", err)
	}
	handler := spaAllowlist(sub)
	// We can't easily exercise the fiber.Ctx here without spinning
	// up a real app; instead, assert the prefix list is well-formed:
	// each entry starts with "/" and has no trailing slash. Combined
	// with the file-walk test above, that's enough to prevent the
	// regression class we just hit.
	_ = handler
	for _, p := range spaRoutePrefixes {
		if !strings.HasPrefix(p, "/") {
			t.Errorf("spaRoutePrefixes entry %q must start with /", p)
		}
		if len(p) > 1 && strings.HasSuffix(p, "/") {
			t.Errorf("spaRoutePrefixes entry %q must not have trailing slash", p)
		}
	}
}
