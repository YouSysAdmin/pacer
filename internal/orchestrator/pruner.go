// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package orchestrator

import (
	"context"
	"log/slog"
	"time"

	"github.com/yousysadmin/pacer/internal/core/env"
	settingsdomain "github.com/yousysadmin/pacer/internal/domain/settings"
)

const (
	// PruneInterval is the cadence at which the prune sweep runs. The
	// tables are small per row but insert on every webhook delivery
	// and every state change, so daily is plenty - slower would
	// still be correct, faster just burns a write.
	PruneInterval = 24 * time.Hour
)

// Pruner sweeps housekeeping tables that grow with traffic:
// webhook_deliveries (debug trail of incoming webhooks) and
// audit_log (operator-visible state-change record). Retention
// periods are resolved per-tick from Runtime.Config + the settings
// table, so a Settings UI change takes effect on the next sweep
// without a process restart.
type Pruner struct {
	Runtime *env.Runtime
}

func NewPruner(rt *env.Runtime) *Pruner {
	return &Pruner{Runtime: rt}
}

// Run sweeps every PruneInterval until ctx is cancelled. The first
// tick fires after one interval (not at startup) so a flapping
// process doesn't repeatedly delete rows. On a healthy install the
// queue is bounded by traffic anyway.
func (p *Pruner) Run(ctx context.Context) {
	t := time.NewTicker(PruneInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			slog.Info("pruner stopping")
			return
		case <-t.C:
			p.Tick(ctx)
		}
	}
}

// Tick is one sweep of all housekeeping tables. Exported so tests
// (and a future manual "run pruner now" endpoint) can invoke it
// without waiting for the daily cadence.
func (p *Pruner) Tick(ctx context.Context) {
	now := time.Now().UTC()

	// Webhook deliveries: short retention, mostly for debugging
	// duplicate-delivery / redelivery edge cases.
	whDays := settingsdomain.EffectiveWebhookDays(ctx, p.Runtime)
	whCutoff := now.Add(-time.Duration(whDays) * 24 * time.Hour)
	if n, err := p.Runtime.Store.Webhook.DeleteOlderThan(ctx, whCutoff); err != nil {
		slog.Error("pruner: webhook_deliveries failed", "err", err)
	} else if n > 0 {
		slog.Info("pruner: webhook_deliveries pruned",
			"rows", n, "cutoff", whCutoff, "retention_days", whDays)
	}

	// Audit log: longer retention, operator-facing record of every
	// state change. Effective period honors any DB override. YAML
	// default applies when unset (90 days out of the box).
	auditDays := settingsdomain.EffectiveAuditDays(ctx, p.Runtime)
	auditCutoff := now.Add(-time.Duration(auditDays) * 24 * time.Hour)
	if n, err := p.Runtime.Store.Audit.DeleteOlderThan(ctx, auditCutoff); err != nil {
		slog.Error("pruner: audit_log failed", "err", err)
	} else if n > 0 {
		slog.Info("pruner: audit_log pruned",
			"rows", n, "cutoff", auditCutoff, "retention_days", auditDays)
	}

	// Job bootstrap logs: the only unbounded thing on the jobs table.
	// A failed job carries up to 64 KiB of captured output, so a
	// noisy week costs more disk than every other housekeeping table
	// combined. This CLEARS THE LOG and keeps the row - stats read
	// jobs directly, and deleting rows here would quietly shorten
	// every cost report to the retention window.
	jobLogDays := settingsdomain.EffectiveJobLogDays(ctx, p.Runtime)
	jobLogCutoff := now.Add(-time.Duration(jobLogDays) * 24 * time.Hour)
	if n, err := p.Runtime.Store.Job.ClearLogsOlderThan(ctx, jobLogCutoff); err != nil {
		slog.Error("pruner: job logs failed", "err", err)
	} else if n > 0 {
		slog.Info("pruner: job logs cleared",
			"rows", n, "cutoff", jobLogCutoff, "retention_days", jobLogDays)
	}
}
