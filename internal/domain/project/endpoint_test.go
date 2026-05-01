// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package project_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"

	"github.com/yousysadmin/pacer/internal/core/env"
	"github.com/yousysadmin/pacer/internal/domain/project"
	"github.com/yousysadmin/pacer/internal/testutil/runtimeutil"
)

func newApp(t *testing.T) (*fiber.App, *env.Runtime) {
	t.Helper()
	rt := runtimeutil.NewRuntime(t, &env.Config{})
	h := &project.Handler{Runtime: rt}
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Post("/api/projects", h.Create)
	return app, rt
}

func postJSON(t *testing.T, app *fiber.App, path string, body any) *http.Response {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	return resp
}

func bodyText(t *testing.T, r *http.Response) string {
	t.Helper()
	buf := new(bytes.Buffer)
	if _, err := buf.ReadFrom(r.Body); err != nil {
		t.Fatalf("read body: %v", err)
	}
	return buf.String()
}

func TestProjectCreate_RejectsReservedTagPrefix(t *testing.T) {
	app, _ := newApp(t)

	cases := []map[string]string{
		{"gha:cost_center": "alpha"},
		{"GHA:something": "x"}, // case-insensitive
		{"gha:": "empty-key-suffix"},
	}
	for _, tags := range cases {
		resp := postJSON(t, app, "/api/projects", map[string]any{
			"name": "demo",
			"tags": tags,
		})
		if resp.StatusCode != 400 {
			t.Errorf("tags %v: want 400, got %d", tags, resp.StatusCode)
		}
		if !strings.Contains(bodyText(t, resp), "reserved") {
			t.Errorf("tags %v: response should mention 'reserved'", tags)
		}
	}
}

func TestProjectCreate_RejectsEmptyTagKey(t *testing.T) {
	app, _ := newApp(t)
	resp := postJSON(t, app, "/api/projects", map[string]any{
		"name": "demo",
		"tags": map[string]string{"": "value"},
	})
	if resp.StatusCode != 400 {
		t.Fatalf("status: want 400, got %d", resp.StatusCode)
	}
}

func TestProjectCreate_OrgScopeRequiresOrgName(t *testing.T) {
	app, _ := newApp(t)
	resp := postJSON(t, app, "/api/projects", map[string]any{
		"name":  "demo",
		"scope": "org",
	})
	if resp.StatusCode != 400 {
		t.Fatalf("status: want 400, got %d", resp.StatusCode)
	}
	if !strings.Contains(bodyText(t, resp), "org_name") {
		t.Fatal("response should explain org_name required")
	}
}

func TestProjectCreate_OrgScopeOK(t *testing.T) {
	app, _ := newApp(t)
	resp := postJSON(t, app, "/api/projects", map[string]any{
		"name":            "demo",
		"scope":           "org",
		"org_name":        "octocat",
		"runner_group_id": 0,
	})
	if resp.StatusCode != 201 {
		t.Fatalf("status: want 201, got %d (%s)", resp.StatusCode, bodyText(t, resp))
	}
}

func TestProjectCreate_OrgNameNoSlashOrSpace(t *testing.T) {
	app, _ := newApp(t)
	for _, bad := range []string{"oct/cat", "oct cat", "oct\tcat"} {
		resp := postJSON(t, app, "/api/projects", map[string]any{
			"name":     "demo-" + bad,
			"scope":    "org",
			"org_name": bad,
		})
		if resp.StatusCode != 400 {
			t.Errorf("org_name %q: want 400, got %d", bad, resp.StatusCode)
		}
	}
}

func TestProjectCreate_InvalidScope(t *testing.T) {
	app, _ := newApp(t)
	resp := postJSON(t, app, "/api/projects", map[string]any{
		"name":  "demo",
		"scope": "weird",
	})
	if resp.StatusCode != 400 {
		t.Fatalf("status: want 400, got %d", resp.StatusCode)
	}
}

func TestProjectCreate_NameRequired(t *testing.T) {
	app, _ := newApp(t)
	resp := postJSON(t, app, "/api/projects", map[string]any{})
	if resp.StatusCode != 400 {
		t.Fatalf("status: want 400, got %d", resp.StatusCode)
	}
}

func TestProjectCreate_DefaultsToRepoScope(t *testing.T) {
	app, rt := newApp(t)
	resp := postJSON(t, app, "/api/projects", map[string]any{
		"name": "demo",
	})
	if resp.StatusCode != 201 {
		t.Fatalf("status: want 201, got %d (%s)", resp.StatusCode, bodyText(t, resp))
	}
	got, err := rt.Store.Project.GetByName(context.Background(), "demo")
	if err != nil || got == nil {
		t.Fatalf("GetByName: %v %v", got, err)
	}
	if got.Scope != "repo" {
		t.Fatalf("default scope: want repo, got %q", got.Scope)
	}
}
