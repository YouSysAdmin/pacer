// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package orchestrator

import (
	"strings"
	"testing"

	"github.com/yousysadmin/pacer/internal/core/env"
	"github.com/yousysadmin/pacer/internal/core/health"
)

// Nothing polls the prune loop and the tables it trims misbehave only
// by growing, so an unrecovered panic here would stop pruning for the
// life of the process without a single symptom. The guard has to hold
// and it has to say so on Health.
func TestPruner_SafeTickSurvivesPanic(t *testing.T) {
	// A zero Store panics on the first store call inside Tick, which
	// is the shape of any nil-dereference bug in a sweep.
	rt := &env.Runtime{Health: health.New()}
	p := NewPruner(rt)

	p.safeTick(t.Context()) // must not take the test binary down

	msg, ok := rt.Health.Get(prunerHealthComponent)
	if !ok {
		t.Fatal("a recovered panic must be reported on Health")
	}
	if !strings.HasPrefix(msg, "panic:") {
		t.Fatalf("Health message: want a panic: prefix, got %q", msg)
	}
}
