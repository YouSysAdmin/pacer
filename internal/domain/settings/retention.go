// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package settings

import (
	"context"
	"log/slog"
	"strconv"

	"github.com/yousysadmin/pacer/internal/core/env"
	settingsmodel "github.com/yousysadmin/pacer/internal/models/settings"
)

// RetentionLimits caps operator-supplied overrides. AuditMax matches
// the manual prune endpoint's cap; WebhookMax is intentionally lower
// since long webhook-delivery retention serves no debug purpose past
// a couple of weeks.
const (
	AuditMinDays   = 1
	AuditMaxDays   = 3650
	WebhookMinDays = 1
	WebhookMaxDays = 365
)

// EffectiveAuditDays returns the audit retention period the pruner +
// any consumer should honor: a valid DB override if present, else
// the YAML default. A malformed or out-of-range DB value is treated
// as "not set" -- we log a warning and fall back to the YAML
// default so a typo in the settings table can't take the pruner
// offline.
func EffectiveAuditDays(ctx context.Context, rt *env.Runtime) int {
	return effective(ctx, rt,
		settingsmodel.KeyAuditRetentionDays,
		rt.Config.Retention.AuditDays,
		AuditMinDays, AuditMaxDays)
}

// EffectiveWebhookDays mirrors EffectiveAuditDays for the
// webhook_deliveries table.
func EffectiveWebhookDays(ctx context.Context, rt *env.Runtime) int {
	return effective(ctx, rt,
		settingsmodel.KeyWebhookRetentionDays,
		rt.Config.Retention.WebhookDays,
		WebhookMinDays, WebhookMaxDays)
}

func effective(ctx context.Context, rt *env.Runtime, key string, fallback, min, max int) int {
	if rt == nil || rt.Store == nil || rt.Store.Settings == nil {
		return fallback
	}
	row, err := rt.Store.Settings.Get(ctx, key)
	if err != nil {
		slog.Warn("settings: retention override read failed; using YAML default",
			"key", key, "err", err, "default", fallback)
		return fallback
	}
	if row == nil || row.Value == "" {
		return fallback
	}
	n, perr := strconv.Atoi(row.Value)
	if perr != nil || n < min || n > max {
		slog.Warn("settings: retention override malformed or out of range; using YAML default",
			"key", key, "value", row.Value, "default", fallback,
			"min", min, "max", max)
		return fallback
	}
	return n
}
