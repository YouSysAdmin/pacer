// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// Package ghrunner caches the latest actions/runner release tag.
// Per-spawn user-data needs an exact runner version to download.
// Hitting the GitHub API on every spawn would bake in rate-limit
// pressure (60 req/h unauthenticated) and add 200ms+ to every
// spawn.
// Instead we poll once at startup, refresh in the
// background every refreshInterval, and expose the cached value
// synchronously to the orchestrator's user-data renderer.
//
// Public auth-free GitHub API.
// No App / installation token needed here -- this is reading a
// public release listing on a public repo.
package ghrunner

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	releasesURL     = "https://api.github.com/repos/actions/runner/releases/latest"
	refreshInterval = 6 * time.Hour
	// emptyCacheRetry is the poll cadence while no version is cached yet.
	emptyCacheRetry = 1 * time.Minute
	httpTimeout     = 10 * time.Second
)

// Resolver wraps the cached "latest" tag.
// Safe for concurrent reads. The background refresh goroutine is the only writer.
type Resolver struct {
	mu      sync.RWMutex
	version string // semver without leading "v" (e.g. "2.319.1")

	client *http.Client
}

// New constructs a Resolver and performs an initial blocking fetch so
// the first pool save does not bake an empty version. A failed fetch
// is logged, not returned: the resolver stays usable and Start's
// refresh loop fills the cache once GitHub is reachable again.
// ctx scopes only the initial fetch.
func New(ctx context.Context) *Resolver {
	r := &Resolver{client: &http.Client{Timeout: httpTimeout}}
	if err := r.fetchOnce(ctx); err != nil {
		slog.Warn("ghrunner: initial runner version fetch failed, will retry in background", "err", err)
	}
	return r
}

// Start launches the background refresh loop.
// Cancel ctx to stop. Errors during refresh are logged and the
// previous cached value is preserved -- a transient GitHub outage shouldn't break spawns.
func (r *Resolver) Start(ctx context.Context) {
	go func() {
		// Poll fast while the cache is empty, then settle into the
		// regular refresh cadence.
		interval := refreshInterval
		if r.Latest() == "" {
			interval = emptyCacheRetry
		}
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if err := r.fetchOnce(ctx); err != nil {
					slog.Warn("ghrunner: refresh failed (keeping previous version)", "err", err, "previous", r.Latest())
				} else if interval != refreshInterval {
					interval = refreshInterval
					t.Reset(interval)
				}
			}
		}
	}()
}

// Latest returns the cached version string ("2.319.1", no leading v).
// Empty only if the initial fetch failed AND no override is in use.
func (r *Resolver) Latest() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.version
}

// Resolve picks the version to bake into user-data: the per-pool
// pin if non-empty, otherwise the cached latest.
// A nil receiver is treated as "no resolver available" -- the orchestrator passes nil
// when AWS is disabled (UI-only dev), and the user-data template
// falls back to its own default constant in that case.
func (r *Resolver) Resolve(poolPin string) string {
	if poolPin != "" {
		return strings.TrimPrefix(poolPin, "v")
	}
	if r == nil {
		return ""
	}
	return r.Latest()
}

func (r *Resolver) fetchOnce(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", releasesURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "pacer")
	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("github returned %d", resp.StatusCode)
	}
	var body struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return err
	}
	v := strings.TrimPrefix(body.TagName, "v")
	if v == "" {
		return fmt.Errorf("empty tag_name in release payload")
	}
	r.mu.Lock()
	r.version = v
	r.mu.Unlock()
	slog.Info("ghrunner: cached latest runner version", "version", v)
	return nil
}
