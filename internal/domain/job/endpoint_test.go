// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package job_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/yousysadmin/pacer/internal/core/env"
	"github.com/yousysadmin/pacer/internal/domain/job"
	jobmodel "github.com/yousysadmin/pacer/internal/models/job"
	poolmodel "github.com/yousysadmin/pacer/internal/models/pool"
	projectmodel "github.com/yousysadmin/pacer/internal/models/project"
	"github.com/yousysadmin/pacer/internal/testutil/runtimeutil"
)

func newApp(t *testing.T) (*fiber.App, *env.Runtime) {
	t.Helper()
	rt := runtimeutil.NewRuntime(t, &env.Config{})
	h := &job.Handler{Runtime: rt}
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Get("/api/jobs/:id", h.Get)
	return app, rt
}

func TestJobGet_IncludesProjectAndPoolNames(t *testing.T) {
	app, rt := newApp(t)
	ctx := context.Background()

	pr := &projectmodel.Project{
		ID:    "proj-uuid",
		Name:  "alpha",
		Scope: "repo",
		Tags:  map[string]string{},
	}
	if err := rt.Store.Project.Put(ctx, pr); err != nil {
		t.Fatalf("Project.Put: %v", err)
	}
	po := &poolmodel.Pool{
		ID:                   "pool-uuid",
		ProjectID:            pr.ID,
		Name:                 "linux-large",
		IsDefault:            true,
		Priority:             100,
		AMIID:                "ami-test",
		InstanceTypes:        []string{"t3.large"},
		SubnetIDs:            []string{"subnet-1"},
		SecurityGroupIDs:     []string{"sg-1"},
		RootVolumeGB:         30,
		MaxRuntimeMinutes:    60,
		MaxConcurrentRunners: 5,
		Spot:                 true,
		SpawnMethod:          "fleet",
		AllocationStrategy:   "cost",
		Tags:                 map[string]string{},
	}
	if err := rt.Store.Pool.Put(ctx, po); err != nil {
		t.Fatalf("Pool.Put: %v", err)
	}
	j := &jobmodel.Job{
		ID:             "job-1",
		GHJobID:        42,
		GHRunID:        100,
		InstallationID: 1,
		RepoFullName:   "octo/hello",
		ProjectID:      pr.ID,
		PoolID:         po.ID,
		Status:         jobmodel.StatusQueued,
		QueuedAt:       time.Now().UTC(),
		Payload:        []byte("{}"),
	}
	if err := rt.Store.Job.Put(ctx, j); err != nil {
		t.Fatalf("Job.Put: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/jobs/"+j.ID, nil)
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status: want 200, got %d", resp.StatusCode)
	}

	var body struct {
		Job         *jobmodel.Job `json:"job"`
		ProjectName string        `json:"project_name"`
		PoolName    string        `json:"pool_name"`
	}
	buf := new(bytes.Buffer)
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		t.Fatalf("read body: %v", err)
	}
	if err := json.Unmarshal(buf.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v (body=%s)", err, buf.String())
	}
	if body.Job == nil || body.Job.ID != j.ID {
		t.Fatalf("job not echoed: %+v", body.Job)
	}
	if got, want := body.ProjectName, "alpha"; got != want {
		t.Errorf("project_name: want %q, got %q", want, got)
	}
	if got, want := body.PoolName, "linux-large"; got != want {
		t.Errorf("pool_name: want %q, got %q", want, got)
	}
}

func TestJobGet_EmptyPoolIDLeavesNameBlank(t *testing.T) {
	// A job stamped before pool.Match runs (or that didn't match any
	// pool) has PoolID == "". The detail endpoint must still succeed
	// and return an empty pool_name; the frontend falls back to "—".
	app, rt := newApp(t)
	ctx := context.Background()

	pr := &projectmodel.Project{ID: "p1", Name: "alpha", Scope: "repo", Tags: map[string]string{}}
	if err := rt.Store.Project.Put(ctx, pr); err != nil {
		t.Fatalf("Project.Put: %v", err)
	}

	j := &jobmodel.Job{
		ID:             "job-nopool",
		GHJobID:        43,
		GHRunID:        101,
		InstallationID: 1,
		RepoFullName:   "octo/hello",
		ProjectID:      pr.ID,
		PoolID:         "",
		Status:         jobmodel.StatusQueued,
		QueuedAt:       time.Now().UTC(),
		Payload:        []byte("{}"),
	}
	if err := rt.Store.Job.Put(ctx, j); err != nil {
		t.Fatalf("Job.Put: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/jobs/"+j.ID, nil)
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status: want 200, got %d", resp.StatusCode)
	}

	var body struct {
		ProjectName string `json:"project_name"`
		PoolName    string `json:"pool_name"`
	}
	buf := new(bytes.Buffer)
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		t.Fatalf("read body: %v", err)
	}
	if err := json.Unmarshal(buf.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.ProjectName != "alpha" {
		t.Errorf("project_name: want alpha, got %q", body.ProjectName)
	}
	if body.PoolName != "" {
		t.Errorf("pool_name: want empty, got %q", body.PoolName)
	}
}

func TestJobList_FiltersByProjectPoolRepo(t *testing.T) {
	app, rt := newApp(t)
	h := &job.Handler{Runtime: rt}
	app.Get("/api/jobs", h.List)
	ctx := context.Background()

	for _, id := range []string{"p1", "p2"} {
		if err := rt.Store.Project.Put(ctx, &projectmodel.Project{ID: id, Name: id, Scope: "repo", Tags: map[string]string{}}); err != nil {
			t.Fatal(err)
		}
		if err := rt.Store.Pool.Put(ctx, &poolmodel.Pool{ID: "po-" + id, ProjectID: id, Name: "default", IsDefault: true,
			AMIID: "ami-1", InstanceTypes: []string{"t3.small"}, SubnetIDs: []string{"s-1"}, MaxRuntimeMinutes: 60, MaxConcurrentRunners: 5}); err != nil {
			t.Fatal(err)
		}
	}
	put := func(id, proj, repo string) {
		if err := rt.Store.Job.Put(ctx, &jobmodel.Job{ID: id, GHJobID: int64(len(id)) + time.Now().UnixNano(), GHRunID: 1, InstallationID: 1,
			RepoFullName: repo, ProjectID: proj, PoolID: "po-" + proj, Status: jobmodel.StatusQueued, Payload: []byte("{}")}); err != nil {
			t.Fatal(err)
		}
	}
	put("j1", "p1", "o/a")
	put("j2", "p1", "o/b")
	put("j3", "p2", "o/a")

	count := func(q string) int {
		resp, err := app.Test(httptest.NewRequest("GET", "/api/jobs"+q, nil), -1)
		if err != nil || resp.StatusCode != 200 {
			t.Fatalf("%s: %v %d", q, err, resp.StatusCode)
		}
		var out struct {
			Total int `json:"total"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return out.Total
	}
	if got := count(""); got != 3 {
		t.Fatalf("no filter: %d", got)
	}
	if got := count("?project_id=p1"); got != 2 {
		t.Fatalf("project filter: %d", got)
	}
	if got := count("?pool_id=po-p2"); got != 1 {
		t.Fatalf("pool filter: %d", got)
	}
	if got := count("?repo=o/a"); got != 2 {
		t.Fatalf("repo filter: %d", got)
	}
	if got := count("?repo=o/a&project_id=p2"); got != 1 {
		t.Fatalf("combined filter: %d", got)
	}
}
