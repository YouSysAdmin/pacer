// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// Package settings is the HTTP edge for pacer-managed DB-backed
// config. Today this is just the bootstrap API token (status read +
// rotate). Rotation re-materializes every pool so the new token
// lands in each pool's LT user-data without an operator click-fest.
package settings

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/yousysadmin/pacer/internal/core/ec2lt"
	"github.com/yousysadmin/pacer/internal/core/env"
	"github.com/yousysadmin/pacer/internal/core/response"
	settingsmodel "github.com/yousysadmin/pacer/internal/models/settings"
)

type Handler struct {
	Runtime *env.Runtime
}

// bootstrapTokenStatus is what GET /api/settings/bootstrap-token
// returns. The raw token never leaves the server -- masked is the
// first 4 hex chars + ellipsis so the operator can confirm the
// token is set without exposing it.
type bootstrapTokenStatus struct {
	Set       bool      `json:"set"`
	Masked    string    `json:"masked,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

// rotateResult reports what happened on POST /api/settings/bootstrap-token/rotate.
// PoolsRematerialized is the count of pools whose LT was bumped to
// carry the new token; PoolsFailed lists the names that errored so the
// operator can re-save manually.
type rotateResult struct {
	RotatedAt           time.Time `json:"rotated_at"`
	PoolsRematerialized int       `json:"pools_rematerialized"`
	PoolsFailed         []string  `json:"pools_failed,omitempty"`
}

// GetBootstrapToken returns metadata about the bootstrap token. The
// raw value is intentionally NOT returned.
func (h *Handler) GetBootstrapToken(c *fiber.Ctx) error {
	s, err := h.Runtime.Store.Settings.Get(c.UserContext(), settingsmodel.KeyBootstrapAPIToken)
	if err != nil {
		return response.Internal(c, err)
	}
	if s == nil {
		return response.Success(c, bootstrapTokenStatus{Set: false})
	}
	return response.Success(c, bootstrapTokenStatus{
		Set:       true,
		Masked:    maskToken(s.Value),
		UpdatedAt: s.UpdatedAt,
	})
}

// RotateBootstrapToken regenerates the token, writes it to settings,
// then re-materializes every pool's LT in parallel so the new token
// is baked into the user-data of every future spawn. Pools whose LT
// rebake fails are surfaced in the response; the operator can re-save
// those by hand.
//
// Trade-off: in-flight instances launched against an old LT version
// still carry the old token in their user-data and will 401 against
// /api/runner/bootstrap. That's the operational cost of rotation; the
// failure surfaces immediately as a spawn-failure in the UI rather
// than a silent stranding.
func (h *Handler) RotateBootstrapToken(c *fiber.Ctx) error {
	ctx := c.UserContext()
	token, err := generateToken()
	if err != nil {
		return response.Internal(c, fmt.Errorf("generate token: %w", err))
	}
	if err := h.Runtime.Store.Settings.Put(ctx, settingsmodel.KeyBootstrapAPIToken, token); err != nil {
		return response.Internal(c, fmt.Errorf("persist token: %w", err))
	}
	// Cache on Runtime so the next bootstrap-endpoint hit doesn't
	// have to round-trip the DB.
	h.Runtime.BootstrapAPIToken.Store(token)

	done, failed := h.rematerializeAllPools(ctx)
	slog.Info("settings: bootstrap token rotated",
		"pools_rematerialized", done, "pools_failed", len(failed))

	return response.Success(c, rotateResult{
		RotatedAt:           time.Now().UTC(),
		PoolsRematerialized: done,
		PoolsFailed:         failed,
	})
}

// rematerializeAllPools iterates every pool and calls ec2lt.CreateOrUpdate.
// Failures are collected (pool names) and reported, not fatal -- a
// single bad pool shouldn't block the others from picking up the new
// token. Sequential rather than concurrent: SQLite's MaxOpenConns(1)
// already serializes writes, and bursts of CreateLaunchTemplateVersion
// against EC2 are throttled per-region.
func (h *Handler) rematerializeAllPools(ctx context.Context) (int, []string) {
	pools, err := h.Runtime.Store.Pool.List(ctx)
	if err != nil {
		slog.Error("settings: list pools for rematerialize failed", "err", err)
		return 0, nil
	}
	if h.Runtime.EC2 == nil {
		// aws.disabled dev mode -- no LT to bump.
		return 0, nil
	}
	var (
		done   int
		failed []string
	)
	for _, p := range pools {
		proj, err := h.Runtime.Store.Project.Get(ctx, p.ProjectID)
		if err != nil || proj == nil {
			failed = append(failed, p.Name)
			slog.Warn("settings: pool's project missing during rematerialize",
				"pool", p.Name, "project_id", p.ProjectID)
			continue
		}
		runnerVersion := p.RunnerVersion
		if h.Runtime.RunnerVersion != nil {
			runnerVersion = h.Runtime.RunnerVersion.Resolve(p.RunnerVersion)
		}
		bootstrapToken, _ := h.Runtime.BootstrapAPIToken.Load().(string)
		if err := ec2lt.CreateOrUpdate(ctx, h.Runtime.EC2, h.Runtime.IAM, p, proj.Name, proj.Tags,
			h.Runtime.Config.Server.PublicURL, runnerVersion, bootstrapToken); err != nil {
			failed = append(failed, p.Name)
			slog.Warn("settings: pool rematerialize failed",
				"pool", p.Name, "err", err)
			continue
		}
		if err := h.Runtime.Store.Pool.Put(ctx, p); err != nil {
			failed = append(failed, p.Name)
			slog.Warn("settings: pool put after rematerialize failed",
				"pool", p.Name, "err", err)
			continue
		}
		done++
	}
	return done, failed
}

// EnsureBootstrapToken runs at server startup: if the settings row is
// missing, generate one. Idempotent -- subsequent starts find the row
// and no-op. Loads the value into Runtime.BootstrapAPIToken either
// way so the bootstrap endpoint has it ready.
func EnsureBootstrapToken(ctx context.Context, rt *env.Runtime) error {
	existing, err := rt.Store.Settings.Get(ctx, settingsmodel.KeyBootstrapAPIToken)
	if err != nil {
		return fmt.Errorf("get bootstrap api token: %w", err)
	}
	if existing != nil {
		rt.BootstrapAPIToken.Store(existing.Value)
		return nil
	}
	token, err := generateToken()
	if err != nil {
		return fmt.Errorf("generate bootstrap api token: %w", err)
	}
	if err := rt.Store.Settings.Put(ctx, settingsmodel.KeyBootstrapAPIToken, token); err != nil {
		return fmt.Errorf("persist bootstrap api token: %w", err)
	}
	rt.BootstrapAPIToken.Store(token)
	slog.Info("settings: bootstrap API token generated (rotate via Settings -> Rotate in the UI)",
		"masked", maskToken(token))
	return nil
}

// generateToken returns 32 random bytes hex-encoded (64 chars).
func generateToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// maskToken returns "<first 4 chars>..." for status responses + audit
// logs. Sufficient for the operator to recognize a specific token
// without exposing the full value.
func maskToken(t string) string {
	if len(t) < 4 {
		return "****"
	}
	return t[:4] + "..."
}

