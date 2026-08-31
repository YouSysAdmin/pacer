// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package systemhealth_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"

	"github.com/yousysadmin/pacer/internal/core/env"
	"github.com/yousysadmin/pacer/internal/core/health"
	"github.com/yousysadmin/pacer/internal/domain/systemhealth"
)

type stubReaper struct {
	checked int
	err     error
	calls   int
}

func (s *stubReaper) Tick(_ context.Context) (int, error) {
	s.calls++
	return s.checked, s.err
}

func newApp(rt *env.Runtime) *fiber.App {
	h := &systemhealth.Handler{Runtime: rt}
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Get("/api/health", h.List)
	app.Post("/api/reconcile", h.Reconcile)
	return app
}

func decode(t *testing.T, r *http.Response, into any) {
	t.Helper()
	buf := new(bytes.Buffer)
	if _, err := buf.ReadFrom(r.Body); err != nil {
		t.Fatalf("read body: %v", err)
	}
	if err := json.Unmarshal(buf.Bytes(), into); err != nil {
		t.Fatalf("decode body %q: %v", buf.String(), err)
	}
}

func TestList_Empty(t *testing.T) {
	rt := &env.Runtime{Health: health.New()}
	app := newApp(rt)

	req := httptest.NewRequest("GET", "/api/health", nil)
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status: want 200, got %d", resp.StatusCode)
	}
	var body struct {
		Issues []health.Issue `json:"issues"`
	}
	decode(t, resp, &body)
	if len(body.Issues) != 0 {
		t.Fatalf("want empty issues, got %d", len(body.Issues))
	}
}

func TestList_WithIssues(t *testing.T) {
	rt := &env.Runtime{Health: health.New()}
	rt.Health.Set("reaper", "describe failed")
	rt.Health.Set("preflight", "missing perms: ec2:DescribeInstances")
	app := newApp(rt)

	req := httptest.NewRequest("GET", "/api/health", nil)
	resp, _ := app.Test(req, -1)
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	var body struct {
		Issues []health.Issue `json:"issues"`
	}
	decode(t, resp, &body)
	if len(body.Issues) != 2 {
		t.Fatalf("want 2 issues, got %d", len(body.Issues))
	}
	// Snapshot is alphabetical: preflight before reaper.
	if body.Issues[0].Component != "preflight" {
		t.Errorf("issue[0]: %s", body.Issues[0].Component)
	}
	if body.Issues[1].Component != "reaper" {
		t.Errorf("issue[1]: %s", body.Issues[1].Component)
	}
}

func TestList_NilHealth_EmptyResponse(t *testing.T) {
	// A Runtime constructed before Health is wired must not crash
	// the endpoint - the SPA polls at boot and a 500 on the first
	// poll would cycle through the 401 redirect path.
	rt := &env.Runtime{}
	app := newApp(rt)
	req := httptest.NewRequest("GET", "/api/health", nil)
	resp, _ := app.Test(req, -1)
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestReconcile_InvokesReaper(t *testing.T) {
	stub := &stubReaper{checked: 3, err: nil}
	rt := &env.Runtime{Health: health.New(), Reaper: stub}
	app := newApp(rt)

	req := httptest.NewRequest("POST", "/api/reconcile", nil)
	resp, _ := app.Test(req, -1)
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	if stub.calls != 1 {
		t.Fatalf("Tick should be called once, got %d", stub.calls)
	}
	var body struct {
		Checked int           `json:"checked"`
		Issue   *health.Issue `json:"issue,omitempty"`
	}
	decode(t, resp, &body)
	if body.Checked != 3 {
		t.Fatalf("checked: want 3, got %d", body.Checked)
	}
	if body.Issue != nil {
		t.Fatalf("issue should be nil on clean sweep, got %+v", body.Issue)
	}
}

func TestReconcile_SurfacesReaperIssue(t *testing.T) {
	// Reaper.Tick returned err == nil but earlier in the sweep
	// checkEC2HealthVia wrote Health - the reconcile body must
	// surface that issue so the operator sees what's wrong without
	// a follow-up /api/health call.
	rt := &env.Runtime{
		Health: health.New(),
		Reaper: &stubReaper{checked: 1},
	}
	rt.Health.Set("reaper", "describe instances failed: UnauthorizedOperation")
	app := newApp(rt)

	req := httptest.NewRequest("POST", "/api/reconcile", nil)
	resp, _ := app.Test(req, -1)
	var body struct {
		Checked int           `json:"checked"`
		Issue   *health.Issue `json:"issue"`
	}
	decode(t, resp, &body)
	if body.Issue == nil {
		t.Fatal("issue should be present in body")
	}
	if body.Issue.Component != "reaper" {
		t.Fatalf("issue.component: %s", body.Issue.Component)
	}
}

func TestReconcile_NoReaper_Returns503(t *testing.T) {
	rt := &env.Runtime{Health: health.New()} // Reaper nil
	app := newApp(rt)

	req := httptest.NewRequest("POST", "/api/reconcile", nil)
	resp, _ := app.Test(req, -1)
	if resp.StatusCode != 503 {
		t.Fatalf("status: want 503, got %d", resp.StatusCode)
	}
}

func TestReconcile_PanicRecoveredTick_StillReturns200(t *testing.T) {
	// A reaper that returned an error (because it panicked and
	// safeTick re-raised it as err) must still produce a 200 from
	// the endpoint: the goroutine is alive, the verdict is the
	// Health issue, and the operator wants to see it - not a 500
	// they have to debug separately.
	rt := &env.Runtime{
		Health: health.New(),
		Reaper: &stubReaper{checked: 0, err: errors.New("reaper panic: boom")},
	}
	rt.Health.Set("reaper", "panic: boom")
	app := newApp(rt)

	req := httptest.NewRequest("POST", "/api/reconcile", nil)
	resp, _ := app.Test(req, -1)
	if resp.StatusCode != 200 {
		t.Fatalf("status: want 200, got %d", resp.StatusCode)
	}
}
