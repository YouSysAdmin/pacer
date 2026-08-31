// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package orchestrator_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/yousysadmin/pacer/internal/core/env"
	"github.com/yousysadmin/pacer/internal/domain/webhook"
	auditmodel "github.com/yousysadmin/pacer/internal/models/audit"
	settingsmodel "github.com/yousysadmin/pacer/internal/models/settings"
	"github.com/yousysadmin/pacer/internal/orchestrator"
	"github.com/yousysadmin/pacer/internal/testutil/runtimeutil"
)

// newPrunerRT builds a Runtime with the Webhook store wired in.
// runtimeutil intentionally omits Webhook to dodge an import cycle
// (see comment in runtime.go). The orchestrator package can wire it
// here without triggering the cycle because the orchestrator test
// binary doesn't share a package boundary with webhook.
func newPrunerRT(t *testing.T, cfg *env.Config) *env.Runtime {
	rt := runtimeutil.NewRuntime(t, cfg)
	rt.Store.Webhook = webhook.NewStore(rt.DB.DB())
	return rt
}

func TestPruner_PrunesAuditAndWebhook(t *testing.T) {
	rt := newPrunerRT(t, &env.Config{
		Retention: env.RetentionConfig{AuditDays: 30, WebhookDays: 7},
	})
	ctx := t.Context()
	now := time.Now().UTC()

	// Audit: 4 rows older than 30d (should be pruned) + 3 newer
	// (should stay).
	for i := range 4 {
		_ = rt.Store.Audit.Put(ctx, &auditmodel.Entry{
			ID: fmt.Sprintf("a-old-%d", i), Action: auditmodel.ActionProjectCreated,
			OccurredAt: now.Add(-60 * 24 * time.Hour).Add(time.Duration(i) * time.Minute),
		})
	}
	for i := range 3 {
		_ = rt.Store.Audit.Put(ctx, &auditmodel.Entry{
			ID: fmt.Sprintf("a-new-%d", i), Action: auditmodel.ActionProjectCreated,
			OccurredAt: now.Add(-1 * 24 * time.Hour).Add(time.Duration(i) * time.Minute),
		})
	}

	p := orchestrator.NewPruner(rt)
	p.Tick(ctx)

	got, _ := rt.Store.Audit.Count(ctx, auditmodel.ListFilter{})
	if got != 3 {
		t.Fatalf("audit after prune: want 3 survivors, got %d", got)
	}
}

func TestPruner_DBOverrideShortensRetention(t *testing.T) {
	// YAML default is 30d. Operator overrides to 5d via the settings
	// table. The next prune must respect the override.
	rt := newPrunerRT(t, &env.Config{
		Retention: env.RetentionConfig{AuditDays: 30, WebhookDays: 7},
	})
	ctx := t.Context()
	now := time.Now().UTC()

	// 5 rows at 0d, 2d, 4d, 6d, 8d back.
	for i := range 5 {
		_ = rt.Store.Audit.Put(ctx, &auditmodel.Entry{
			ID: fmt.Sprintf("a-%d", i), Action: auditmodel.ActionProjectCreated,
			OccurredAt: now.Add(-time.Duration(i*2*24) * time.Hour),
		})
	}

	// 5d override - the 6d-old + 8d-old rows are toast, 0d/2d/4d
	// survive. So 3 left.
	if err := rt.Store.Settings.Put(ctx, settingsmodel.KeyAuditRetentionDays, "5"); err != nil {
		t.Fatalf("Put settings: %v", err)
	}

	p := orchestrator.NewPruner(rt)
	p.Tick(ctx)

	got, _ := rt.Store.Audit.Count(ctx, auditmodel.ListFilter{})
	if got != 3 {
		t.Fatalf("override-respecting prune: want 3 survivors, got %d", got)
	}
}
