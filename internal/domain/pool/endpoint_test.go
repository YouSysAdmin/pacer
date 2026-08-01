// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package pool

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"

	"github.com/yousysadmin/pacer/internal/core/env"
	auditdomain "github.com/yousysadmin/pacer/internal/domain/audit"
	projectdomain "github.com/yousysadmin/pacer/internal/domain/project"
	"github.com/yousysadmin/pacer/internal/domain/store"
	poolmodel "github.com/yousysadmin/pacer/internal/models/pool"
	"github.com/yousysadmin/pacer/internal/testutil"
)

type harness struct {
	app *fiber.App
	db  *sql.DB
	rt  *env.Runtime
}

// newHarness wires the pool handler the way routes.go does. The
// Runtime is assembled by hand (testutil/runtimeutil builds the full
// store aggregate, which imports this package -- test import cycle).
// EC2 stays nil (aws.disabled posture), so materializeLT stamps the
// lt-dev placeholder and never talks to AWS.
func newHarness(t *testing.T) *harness {
	t.Helper()
	db := testutil.OpenTestDB(t)
	rt := &env.Runtime{
		Config: &env.Config{},
		Store: &store.Store{
			Pool:    NewStore(db),
			Project: projectdomain.NewStore(db),
			Audit:   auditdomain.NewStore(db),
		},
	}
	h := &Handler{Runtime: rt}
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Post("/api/projects/:project_id/pools", h.Create)
	app.Put("/api/pools/:id", h.Update)
	app.Delete("/api/pools/:id", h.Delete)
	return &harness{app: app, db: db, rt: rt}
}

func (h *harness) do(t *testing.T, method, path string, body any) *http.Response {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.app.Test(req, -1)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	return resp
}

func poolInput(name string, isDefault bool) map[string]any {
	return map[string]any{
		"name":               name,
		"is_default":         isDefault,
		"ami_id":             "ami-test",
		"instance_types":     []string{"t3.large"},
		"subnet_ids":         []string{"subnet-1"},
		"security_group_ids": []string{"sg-1"},
	}
}

func seedProject(t *testing.T, h *harness, id, name string) {
	t.Helper()
	if _, err := h.db.ExecContext(context.Background(), `
		INSERT INTO projects (id, name, max_concurrent_runners, tags, scope, org_name, runner_group_id, disabled)
		VALUES (?, ?, 0, '{}', 'repo', '', 0, 0)`, id, name); err != nil {
		t.Fatalf("seed project: %v", err)
	}
}

func TestPool_Delete_BlockedByQueuedJobs(t *testing.T) {
	h := newHarness(t)
	seedProject(t, h, "p-1", "demo")
	db := h.db
	store := NewStore(db)
	mustPutPool(t, store, &poolmodel.Pool{
		ID: "po-1", ProjectID: "p-1", Name: "default",
		AMIID: "ami-x", InstanceTypes: []string{"t3.large"},
		SubnetIDs: []string{"subnet-1"}, SecurityGroupIDs: []string{"sg-1"},
		MaxRuntimeMinutes: 60, MaxConcurrentRunners: 5,
	})
	// A queued job -- no instance yet, so the old in-flight-only gate
	// missed it and Delete would NULL its pool_id, making it invisible
	// to Job.Claim forever.
	insertJob(t, db, "job-q", "p-1", "po-1", "queued")

	resp := h.do(t, "DELETE", "/api/pools/po-1", nil)
	if resp.StatusCode != 409 {
		t.Fatalf("status: want 409, got %d", resp.StatusCode)
	}
	got, err := store.Get(context.Background(), "po-1")
	if err != nil || got == nil {
		t.Fatalf("pool must survive: %v (pool=%v)", err, got)
	}
	var poolID string
	if err := db.QueryRowContext(context.Background(),
		`SELECT pool_id FROM jobs WHERE id = ?`, "job-q").Scan(&poolID); err != nil {
		t.Fatalf("read job: %v", err)
	}
	if poolID != "po-1" {
		t.Fatalf("queued job lost its pool binding: %q", poolID)
	}
}

func TestPool_Delete_BlockedByInFlightJobs(t *testing.T) {
	h := newHarness(t)
	seedProject(t, h, "p-1", "demo")
	store := NewStore(h.db)
	mustPutPool(t, store, &poolmodel.Pool{
		ID: "po-1", ProjectID: "p-1", Name: "default",
		AMIID: "ami-x", InstanceTypes: []string{"t3.large"},
		SubnetIDs: []string{"subnet-1"}, SecurityGroupIDs: []string{"sg-1"},
		MaxRuntimeMinutes: 60, MaxConcurrentRunners: 5,
	})
	insertJob(t, h.db, "job-r", "p-1", "po-1", "running")

	resp := h.do(t, "DELETE", "/api/pools/po-1", nil)
	if resp.StatusCode != 409 {
		t.Fatalf("status: want 409, got %d", resp.StatusCode)
	}
}

func TestPool_Delete_AllowedWithOnlyTerminalJobs(t *testing.T) {
	h := newHarness(t)
	seedProject(t, h, "p-1", "demo")
	store := NewStore(h.db)
	mustPutPool(t, store, &poolmodel.Pool{
		ID: "po-1", ProjectID: "p-1", Name: "default",
		AMIID: "ami-x", InstanceTypes: []string{"t3.large"},
		SubnetIDs: []string{"subnet-1"}, SecurityGroupIDs: []string{"sg-1"},
		MaxRuntimeMinutes: 60, MaxConcurrentRunners: 5,
	})
	insertJob(t, h.db, "job-c", "p-1", "po-1", "completed")
	insertJob(t, h.db, "job-f", "p-1", "po-1", "failed")

	resp := h.do(t, "DELETE", "/api/pools/po-1", nil)
	if resp.StatusCode != 204 {
		t.Fatalf("status: want 204, got %d", resp.StatusCode)
	}
	got, _ := store.Get(context.Background(), "po-1")
	if got != nil {
		t.Fatal("pool should be deleted")
	}
}

// Guards the ensureSingleDefault call surviving the materialize-first
// reorder: creating a new default pool must clear the previous
// default, and the partial unique index must not reject the save.
func TestPool_CreateDefault_FlipsSiblingDefault(t *testing.T) {
	h := newHarness(t)
	seedProject(t, h, "p-1", "demo")

	if resp := h.do(t, "POST", "/api/projects/p-1/pools", poolInput("first", true)); resp.StatusCode != 201 {
		t.Fatalf("create first: want 201, got %d", resp.StatusCode)
	}
	if resp := h.do(t, "POST", "/api/projects/p-1/pools", poolInput("second", true)); resp.StatusCode != 201 {
		t.Fatalf("create second: want 201, got %d", resp.StatusCode)
	}

	store := NewStore(h.db)
	pools, err := store.ListByProject(context.Background(), "p-1")
	if err != nil {
		t.Fatalf("ListByProject: %v", err)
	}
	if len(pools) != 2 {
		t.Fatalf("want 2 pools, got %d", len(pools))
	}
	var defaults int
	for _, p := range pools {
		if p.IsDefault {
			defaults++
			if p.Name != "second" {
				t.Fatalf("default should be %q, got %q", "second", p.Name)
			}
		}
	}
	if defaults != 1 {
		t.Fatalf("want exactly 1 default pool, got %d", defaults)
	}
}
