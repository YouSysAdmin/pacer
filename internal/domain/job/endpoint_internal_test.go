// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package job

import (
	"context"
	"testing"
	"time"

	"github.com/yousysadmin/pacer/internal/core/env"
	"github.com/yousysadmin/pacer/internal/core/ghapp"
	jobmodel "github.com/yousysadmin/pacer/internal/models/job"
)

// The refresh throttle map is keyed per viewed job; entries must be
// dropped once the job leaves running or the map grows for the life
// of the process (one entry per job ever opened in the modal).
func TestRefreshSteps_DropsThrottleEntryForFinishedJob(t *testing.T) {
	h := &Handler{Runtime: &env.Runtime{
		// Non-nil GHApp so the guard under test (status gate) is
		// reached; the completed status returns before any network or
		// store call, so the zero-value client is never exercised.
		GHApp: &ghapp.Client{},
	}}
	j := &jobmodel.Job{ID: "j-done", GHJobID: 1, Status: jobmodel.StatusCompleted}

	h.lastRefreshAt.Store(j.ID, time.Now())
	if _, ok := h.refreshStepsIfRunning(context.Background(), j); ok {
		t.Fatal("refresh must be skipped for a completed job")
	}
	if _, still := h.lastRefreshAt.Load(j.ID); still {
		t.Fatal("throttle entry should be deleted once the job is no longer running")
	}
}

// Running jobs keep their throttle entry -- that's what coalesces the
// modal's 5s polls into one upstream fetch per cycle.
func TestRefreshSteps_ThrottleSkipsFreshEntry(t *testing.T) {
	h := &Handler{Runtime: &env.Runtime{GHApp: &ghapp.Client{}}}
	j := &jobmodel.Job{ID: "j-run", GHJobID: 1, Status: jobmodel.StatusRunning}

	h.lastRefreshAt.Store(j.ID, time.Now())
	if _, ok := h.refreshStepsIfRunning(context.Background(), j); ok {
		t.Fatal("refresh must be throttled within refreshThrottle window")
	}
	if _, still := h.lastRefreshAt.Load(j.ID); !still {
		t.Fatal("throttle entry for a running job must be kept")
	}
}
