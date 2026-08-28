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
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/yousysadmin/pacer/internal/core/auditing"
	"github.com/yousysadmin/pacer/internal/core/ec2lt"
	"github.com/yousysadmin/pacer/internal/core/env"
	"github.com/yousysadmin/pacer/internal/core/response"
	"github.com/yousysadmin/pacer/internal/core/validation"
	"github.com/yousysadmin/pacer/internal/models/audit"
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
	UpdatedAt time.Time `json:"updated_at"`
}

// rotateResult reports what happened on POST /api/settings/bootstrap-token/rotate.
// PoolsRematerialized is the count of pools whose LT was bumped to
// carry the new token. PoolsFailed lists the names that errored so the
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
// rebake fails are surfaced in the response. The operator can re-save
// those by hand.
//
// Trade-off: in-flight instances launched against an old LT version
// still carry the old token in their user-data and will 401 against
// /api/runner/bootstrap. That's the operational cost of rotation. The
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
	auditing.PutCtx(c, h.Runtime.Store.Audit, audit.ActionBootstrapTokenRotated, "settings", settingsmodel.KeyBootstrapAPIToken,
		audit.Detail(map[string]any{
			"masked":               maskToken(token),
			"pools_rematerialized": done,
			"pools_failed":         failed,
		}))

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

// retentionStatus is what GET /api/settings/retention returns.
// audit / webhook are the EFFECTIVE values the pruner is using right
// now. The audit_default / webhook_default fields echo the YAML floor so the
// UI can render "(default: 90)" next to the input. The pruner
// re-resolves on every tick, so PUT takes effect at the next daily
// sweep -- documented in the UI.
type retentionStatus struct {
	AuditDays         int  `json:"audit_days"`
	WebhookDays       int  `json:"webhook_days"`
	AuditDefault      int  `json:"audit_default"`
	WebhookDefault    int  `json:"webhook_default"`
	AuditOverridden   bool `json:"audit_overridden"`
	WebhookOverridden bool `json:"webhook_overridden"`
}

// retentionInput is the body of PUT /api/settings/retention. Either
// field may be omitted. A nil pointer means "leave that setting
// alone." Use 0 to explicitly clear an override (revert to YAML
// default) -- the handler distinguishes nil from 0 via the pointer.
type retentionInput struct {
	AuditDays   *int `json:"audit_days,omitempty"`
	WebhookDays *int `json:"webhook_days,omitempty"`
}

// GetRetention returns the current effective retention periods and
// the YAML defaults so the UI can show "(default: N)" hints.
func (h *Handler) GetRetention(c *fiber.Ctx) error {
	ctx := c.UserContext()
	auditDays := EffectiveAuditDays(ctx, h.Runtime)
	webhookDays := EffectiveWebhookDays(ctx, h.Runtime)
	return response.Success(c, retentionStatus{
		AuditDays:         auditDays,
		WebhookDays:       webhookDays,
		AuditDefault:      h.Runtime.Config.Retention.AuditDays,
		WebhookDefault:    h.Runtime.Config.Retention.WebhookDays,
		AuditOverridden:   auditDays != h.Runtime.Config.Retention.AuditDays,
		WebhookOverridden: webhookDays != h.Runtime.Config.Retention.WebhookDays,
	})
}

// PutRetention writes operator overrides for one or both retention
// periods. Each field is optional (omit to leave unchanged) and
// accepts 0 as the explicit "clear override / use YAML default"
// sentinel. Out-of-range values are rejected so a misclicked Save
// can't push a value the pruner will then fall back from on the
// next tick.
func (h *Handler) PutRetention(c *fiber.Ctx) error {
	in, err := validation.BindAndValidate[retentionInput](c)
	if err != nil {
		fes := validation.Humanize(err)
		return response.BadRequestFields(c, validation.Summary(fes), fes)
	}
	if in.AuditDays == nil && in.WebhookDays == nil {
		return response.BadRequest(c, "at least one of audit_days / webhook_days required")
	}

	ctx := c.UserContext()
	if in.AuditDays != nil {
		v := *in.AuditDays
		if v != 0 && (v < AuditMinDays || v > AuditMaxDays) {
			return response.BadRequest(c, fmt.Sprintf(
				"audit_days must be 0 (use default) or %d..%d, got %d",
				AuditMinDays, AuditMaxDays, v))
		}
		val := ""
		if v != 0 {
			val = strconv.Itoa(v)
		}
		if err := h.Runtime.Store.Settings.Put(ctx, settingsmodel.KeyAuditRetentionDays, val); err != nil {
			return response.Internal(c, err)
		}
	}
	if in.WebhookDays != nil {
		v := *in.WebhookDays
		if v != 0 && (v < WebhookMinDays || v > WebhookMaxDays) {
			return response.BadRequest(c, fmt.Sprintf(
				"webhook_days must be 0 (use default) or %d..%d, got %d",
				WebhookMinDays, WebhookMaxDays, v))
		}
		val := ""
		if v != 0 {
			val = strconv.Itoa(v)
		}
		if err := h.Runtime.Store.Settings.Put(ctx, settingsmodel.KeyWebhookRetentionDays, val); err != nil {
			return response.Internal(c, err)
		}
	}

	auditDays := EffectiveAuditDays(ctx, h.Runtime)
	webhookDays := EffectiveWebhookDays(ctx, h.Runtime)
	auditing.PutCtx(c, h.Runtime.Store.Audit, audit.ActionRetentionUpdated, "settings", "retention",
		audit.Detail(map[string]any{
			"audit_days":   auditDays,
			"webhook_days": webhookDays,
		}))
	return response.Success(c, retentionStatus{
		AuditDays:         auditDays,
		WebhookDays:       webhookDays,
		AuditDefault:      h.Runtime.Config.Retention.AuditDays,
		WebhookDefault:    h.Runtime.Config.Retention.WebhookDays,
		AuditOverridden:   auditDays != h.Runtime.Config.Retention.AuditDays,
		WebhookOverridden: webhookDays != h.Runtime.Config.Retention.WebhookDays,
	})
}
