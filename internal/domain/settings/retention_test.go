// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package settings_test

import (
	"context"
	"testing"

	"github.com/yousysadmin/pacer/internal/core/env"
	settingsdomain "github.com/yousysadmin/pacer/internal/domain/settings"
	settingsmodel "github.com/yousysadmin/pacer/internal/models/settings"
	"github.com/yousysadmin/pacer/internal/testutil/runtimeutil"
)

func TestEffectiveAuditDays_UsesYAMLWhenUnset(t *testing.T) {
	rt := newRT(t, 90, 7)
	if got := settingsdomain.EffectiveAuditDays(context.Background(), rt); got != 90 {
		t.Fatalf("unset audit override: want 90, got %d", got)
	}
}

func TestEffectiveAuditDays_HonorsValidOverride(t *testing.T) {
	rt := newRT(t, 90, 7)
	if err := rt.Store.Settings.Put(context.Background(),
		settingsmodel.KeyAuditRetentionDays, "30"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if got := settingsdomain.EffectiveAuditDays(context.Background(), rt); got != 30 {
		t.Fatalf("valid override: want 30, got %d", got)
	}
}

func TestEffectiveAuditDays_FallsBackOnGarbage(t *testing.T) {
	// A malformed value (typo, manual SQL mistake) must NOT take
	// the pruner offline -- we log a warning and use the YAML
	// default. Same for an out-of-range value.
	cases := []string{"thirty", "0", "-1", "99999"}
	for _, v := range cases {
		t.Run(v, func(t *testing.T) {
			rt := newRT(t, 90, 7)
			_ = rt.Store.Settings.Put(context.Background(),
				settingsmodel.KeyAuditRetentionDays, v)
			if got := settingsdomain.EffectiveAuditDays(context.Background(), rt); got != 90 {
				t.Errorf("garbage %q: want fallback 90, got %d", v, got)
			}
		})
	}
}

func TestEffectiveWebhookDays(t *testing.T) {
	rt := newRT(t, 90, 7)
	if got := settingsdomain.EffectiveWebhookDays(context.Background(), rt); got != 7 {
		t.Fatalf("default: want 7, got %d", got)
	}
	_ = rt.Store.Settings.Put(context.Background(),
		settingsmodel.KeyWebhookRetentionDays, "14")
	if got := settingsdomain.EffectiveWebhookDays(context.Background(), rt); got != 14 {
		t.Fatalf("override: want 14, got %d", got)
	}
}

func newRT(t *testing.T, auditDays, webhookDays int) *env.Runtime {
	cfg := &env.Config{Retention: env.RetentionConfig{
		AuditDays:   auditDays,
		WebhookDays: webhookDays,
	}}
	return runtimeutil.NewRuntime(t, cfg)
}
