// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package repo_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"

	"github.com/yousysadmin/pacer/internal/core/env"
	"github.com/yousysadmin/pacer/internal/domain/repo"
	projectmodel "github.com/yousysadmin/pacer/internal/models/project"
	"github.com/yousysadmin/pacer/internal/testutil/runtimeutil"
)

func newApp(t *testing.T) (*fiber.App, *env.Runtime) {
	t.Helper()
	rt := runtimeutil.NewRuntime(t, &env.Config{})
	h := &repo.Handler{Runtime: rt}
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Post("/api/repos", h.Bind)
	return app, rt
}

func seedProject(t *testing.T, rt *env.Runtime) *projectmodel.Project {
	t.Helper()
	p := &projectmodel.Project{
		ID:    "proj-uuid",
		Name:  "alpha",
		Scope: projectmodel.ScopeRepo,
		Tags:  map[string]string{},
	}
	if err := rt.Store.Project.Put(t.Context(), p); err != nil {
		t.Fatalf("Project.Put: %v", err)
	}
	return p
}

func bind(t *testing.T, app *fiber.App, body map[string]any) *http.Response {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/repos", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	res, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	return res
}

// A negative cap is unsatisfiable in Job.Claim's comparison, so it
// would park every job for the repo in the queue with nothing logged
// to say why. Reject it at the edge.
func TestRepoBind_RejectsNegativeConcurrencyCap(t *testing.T) {
	app, rt := newApp(t)
	p := seedProject(t, rt)

	res := bind(t, app, map[string]any{
		"full_name":              "octocat/hello-world",
		"project_id":             p.ID,
		"max_concurrent_runners": -1,
	})
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("status: want 400, got %d", res.StatusCode)
	}

	r, err := rt.Store.Repo.Get(t.Context(), "octocat/hello-world")
	if err != nil {
		t.Fatalf("Repo.Get: %v", err)
	}
	if r != nil {
		t.Fatalf("rejected bind must not persist a row, got %+v", r)
	}
}

func TestRepoBind_AcceptsZeroAndPositiveCaps(t *testing.T) {
	app, rt := newApp(t)
	p := seedProject(t, rt)

	for _, tc := range []struct {
		name string
		repo string
		cap  int
	}{
		{"zero means no cap", "octocat/zero", 0},
		{"positive cap", "octocat/three", 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := bind(t, app, map[string]any{
				"full_name":              tc.repo,
				"project_id":             p.ID,
				"max_concurrent_runners": tc.cap,
			})
			if res.StatusCode != http.StatusCreated {
				t.Fatalf("status: want 201, got %d", res.StatusCode)
			}
			r, err := rt.Store.Repo.Get(t.Context(), tc.repo)
			if err != nil || r == nil {
				t.Fatalf("Repo.Get: %v %v", r, err)
			}
			if r.MaxConcurrentRunners == nil || *r.MaxConcurrentRunners != tc.cap {
				t.Fatalf("cap: want %d, got %v", tc.cap, r.MaxConcurrentRunners)
			}
		})
	}
}
