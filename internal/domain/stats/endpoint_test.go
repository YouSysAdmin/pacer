// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package stats_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/yousysadmin/pacer/internal/core/env"
	"github.com/yousysadmin/pacer/internal/domain/stats"
	instancemodel "github.com/yousysadmin/pacer/internal/models/instance"
	jobmodel "github.com/yousysadmin/pacer/internal/models/job"
	poolmodel "github.com/yousysadmin/pacer/internal/models/pool"
	projectmodel "github.com/yousysadmin/pacer/internal/models/project"
	"github.com/yousysadmin/pacer/internal/testutil/runtimeutil"
)

// newStatsApp wires the three stats routes against a migrated
// in-memory database.
func newStatsApp(t *testing.T) (*fiber.App, *env.Runtime) {
	t.Helper()
	rt := runtimeutil.NewRuntime(t, &env.Config{})
	h := &stats.Handler{Runtime: rt}
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Get("/api/stats", h.Get)
	app.Get("/api/stats/timeseries", h.Timeseries)
	app.Get("/api/stats/top-users", h.TopUsers)
	return app, rt
}

// seedScopedJobs lands two projects with one completed job each, so
// every assertion below can tell "scoped to p1" apart from "both".
// p1's job costs $1 and ran 60 minutes; p2's costs $2 and ran 30.
func seedScopedJobs(t *testing.T, rt *env.Runtime) {
	t.Helper()
	ctx := context.Background()

	completed := time.Now().UTC().Add(-2 * time.Hour)
	type spec struct {
		project  string
		sender   string
		cost     float64
		minutes  time.Duration
		jobID    string
		instance string
	}
	for _, s := range []spec{
		{"p1", "alice", 1.0, 60 * time.Minute, "j1", "i-1"},
		{"p2", "bob", 2.0, 30 * time.Minute, "j2", "i-2"},
	} {
		if err := rt.Store.Project.Put(ctx, &projectmodel.Project{
			ID: s.project, Name: s.project, Scope: "repo", Tags: map[string]string{},
		}); err != nil {
			t.Fatal(err)
		}
		if err := rt.Store.Pool.Put(ctx, &poolmodel.Pool{
			ID: "po-" + s.project, ProjectID: s.project, Name: "default", IsDefault: true,
			AMIID: "ami-1", InstanceTypes: []string{"t3.small"}, SubnetIDs: []string{"s-1"},
			MaxRuntimeMinutes: 60, MaxConcurrentRunners: 5,
		}); err != nil {
			t.Fatal(err)
		}
		// Job first: instances.job_id is a FK onto jobs, while
		// jobs.instance_id is a plain column.
		if err := rt.Store.Job.Put(ctx, &jobmodel.Job{
			ID: s.jobID, GHJobID: time.Now().UnixNano(), GHRunID: 1, InstallationID: 1,
			RepoFullName: "o/" + s.project, ProjectID: s.project, PoolID: "po-" + s.project,
			InstanceID: s.instance, SenderLogin: s.sender, Status: jobmodel.StatusCompleted,
			CompletedAt: &completed, Payload: []byte("{}"),
		}); err != nil {
			t.Fatal(err)
		}
		// Put deliberately does not write estimated_cost_usd -- the
		// column is owned by Mark{Completed,Failed,Reaped} and
		// FinalizeCost. Stamp it directly so the rollup has a cost to
		// sum without dragging the pricing arithmetic into this test.
		if _, err := rt.DB.DB().ExecContext(ctx,
			`UPDATE jobs SET estimated_cost_usd = ? WHERE id = ?`, s.cost, s.jobID); err != nil {
			t.Fatal(err)
		}
		if err := rt.Store.Instance.Put(ctx, &instancemodel.Instance{
			ID: s.instance, JobID: s.jobID, ProjectID: s.project, PoolID: "po-" + s.project,
			State: instancemodel.StateTerminated, LaunchedAt: completed.Add(-s.minutes),
		}); err != nil {
			t.Fatal(err)
		}
	}
}

func getJSON[T any](t *testing.T, app *fiber.App, path string) T {
	t.Helper()
	resp, err := app.Test(httptest.NewRequest("GET", path, nil), -1)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("GET %s: status %d, want 200", path, resp.StatusCode)
	}
	var out T
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return out
}

type rollupResp struct {
	Totals struct {
		Jobs            int     `json:"jobs"`
		RunnerMinutes   float64 `json:"runner_minutes"`
		EstCostUSD      float64 `json:"est_cost_usd"`
		JobsWithoutCost int     `json:"jobs_without_cost"`
	} `json:"totals"`
	Buckets []struct {
		Key        string  `json:"key"`
		Name       string  `json:"name"`
		Jobs       int     `json:"jobs"`
		EstCostUSD float64 `json:"est_cost_usd"`
	} `json:"buckets"`
}

// TestRollup_ProjectScopeNarrowsBucketsAndTotals is the assertion
// that matters for the console's scope selector: the TOTALS have to
// move with the filter too. A scoped page showing the global total
// would read as that one project's spend.
func TestRollup_ProjectScopeNarrowsBucketsAndTotals(t *testing.T) {
	app, rt := newStatsApp(t)
	seedScopedJobs(t, rt)

	all := getJSON[rollupResp](t, app, "/api/stats")
	if all.Totals.Jobs != 2 {
		t.Fatalf("unscoped jobs: got %d, want 2", all.Totals.Jobs)
	}
	if all.Totals.EstCostUSD != 3.0 {
		t.Fatalf("unscoped cost: got %v, want 3", all.Totals.EstCostUSD)
	}
	if len(all.Buckets) != 2 {
		t.Fatalf("unscoped buckets: got %d, want 2", len(all.Buckets))
	}

	scoped := getJSON[rollupResp](t, app, "/api/stats?project_id=p1")
	if scoped.Totals.Jobs != 1 {
		t.Fatalf("scoped jobs: got %d, want 1", scoped.Totals.Jobs)
	}
	if scoped.Totals.EstCostUSD != 1.0 {
		t.Fatalf("scoped cost: got %v, want 1", scoped.Totals.EstCostUSD)
	}
	if len(scoped.Buckets) != 1 || scoped.Buckets[0].Key != "p1" {
		t.Fatalf("scoped buckets: %+v", scoped.Buckets)
	}
	// The window's runner minutes must narrow with it, not carry the
	// other project's 30.
	if scoped.Totals.RunnerMinutes < 59 || scoped.Totals.RunnerMinutes > 61 {
		t.Fatalf("scoped runner minutes: got %v, want ~60", scoped.Totals.RunnerMinutes)
	}
}

// TestRollup_ScopeCombinesWithGroupBy: scoping and grouping are
// different questions, and asking both is what a scoped stats page
// does. Grouping by repo inside one project must not leak the other.
func TestRollup_ScopeCombinesWithGroupBy(t *testing.T) {
	app, rt := newStatsApp(t)
	seedScopedJobs(t, rt)

	out := getJSON[rollupResp](t, app, "/api/stats?group_by=repo&project_id=p2")
	if len(out.Buckets) != 1 {
		t.Fatalf("buckets: got %d, want 1: %+v", len(out.Buckets), out.Buckets)
	}
	if out.Buckets[0].Key != "o/p2" {
		t.Fatalf("bucket key: got %q, want o/p2", out.Buckets[0].Key)
	}
}

// TestRollup_UnknownProjectIsEmptyNotEverything pins the failure mode
// that a missing WHERE would hide: a project id that matches nothing
// must return nothing, not the whole window.
func TestRollup_UnknownProjectIsEmptyNotEverything(t *testing.T) {
	app, rt := newStatsApp(t)
	seedScopedJobs(t, rt)

	out := getJSON[rollupResp](t, app, "/api/stats?project_id=does-not-exist")
	if out.Totals.Jobs != 0 || len(out.Buckets) != 0 {
		t.Fatalf("unknown project: got %d jobs / %d buckets, want 0/0", out.Totals.Jobs, len(out.Buckets))
	}
}

type timeseriesResp struct {
	Days []struct {
		Day       string `json:"day"`
		Completed int    `json:"completed"`
	} `json:"days"`
}

func TestTimeseries_ProjectScope(t *testing.T) {
	app, rt := newStatsApp(t)
	seedScopedJobs(t, rt)

	all := getJSON[timeseriesResp](t, app, "/api/stats/timeseries")
	var total int
	for _, d := range all.Days {
		total += d.Completed
	}
	if total != 2 {
		t.Fatalf("unscoped completed: got %d, want 2", total)
	}

	scoped := getJSON[timeseriesResp](t, app, "/api/stats/timeseries?project_id=p1")
	total = 0
	for _, d := range scoped.Days {
		total += d.Completed
	}
	if total != 1 {
		t.Fatalf("scoped completed: got %d, want 1", total)
	}
}

type topUsersResp struct {
	Users []struct {
		Login string `json:"login"`
		Jobs  int    `json:"jobs"`
	} `json:"users"`
}

// TestTopUsers_ProjectScope also covers the bind-order trap: the
// project arg has to land before LIMIT, or the query silently
// filters on the wrong value.
func TestTopUsers_ProjectScope(t *testing.T) {
	app, rt := newStatsApp(t)
	seedScopedJobs(t, rt)

	all := getJSON[topUsersResp](t, app, "/api/stats/top-users")
	if len(all.Users) != 2 {
		t.Fatalf("unscoped users: got %d, want 2", len(all.Users))
	}

	scoped := getJSON[topUsersResp](t, app, "/api/stats/top-users?project_id=p2&limit=5")
	if len(scoped.Users) != 1 {
		t.Fatalf("scoped users: got %d, want 1: %+v", len(scoped.Users), scoped.Users)
	}
	if scoped.Users[0].Login != "bob" {
		t.Fatalf("scoped user: got %q, want bob", scoped.Users[0].Login)
	}
}

// TestStats_RejectsBadGroupBy is the error path on the same handler:
// adding a param must not have loosened validation.
func TestStats_RejectsBadGroupBy(t *testing.T) {
	app, _ := newStatsApp(t)
	resp, err := app.Test(httptest.NewRequest("GET", "/api/stats?group_by=nonsense", nil), -1)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 400 {
		t.Fatalf("status: got %d, want 400", resp.StatusCode)
	}
}
