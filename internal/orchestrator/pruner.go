// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package orchestrator

import (
	"context"
	"log/slog"
	"time"

	"github.com/yousysadmin/pacer/internal/core/env"
)

const (
	// PruneInterval is the cadence at which the prune sweep runs. The
	// table is small per row but inserts on every webhook delivery, so
	// daily is plenty -- slower would still be correct, faster just
	// burns a write.
	PruneInterval = 24 * time.Hour

	// webhookDeliveryRetention is how long a delivery row sticks
	// around. GitHub's redelivery window is on the order of minutes;
	// keeping a week gives operators a useful debug trail without
	// letting the table grow forever.
	webhookDeliveryRetention = 7 * 24 * time.Hour
)

// Pruner sweeps housekeeping tables that grow with traffic.
// Currently: webhook_deliveries.
type Pruner struct {
	Runtime *env.Runtime
}

func NewPruner(rt *env.Runtime) *Pruner {
	return &Pruner{Runtime: rt}
}

// Run sweeps every PruneInterval until ctx is cancelled. The first
// tick fires after one interval (not at startup) so a flapping
// process doesn't repeatedly delete rows; on a healthy install the
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
			p.tick(ctx)
		}
	}
}

func (p *Pruner) tick(ctx context.Context) {
	cutoff := time.Now().UTC().Add(-webhookDeliveryRetention)
	n, err := p.Runtime.Store.Webhook.DeleteOlderThan(ctx, cutoff)
	if err != nil {
		slog.Error("pruner: webhook_deliveries failed", "err", err)
		return
	}
	if n > 0 {
		slog.Info("pruner: webhook_deliveries pruned", "rows", n, "cutoff", cutoff)
	}
}
