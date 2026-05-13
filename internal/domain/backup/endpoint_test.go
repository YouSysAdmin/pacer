// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package backup_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gofiber/fiber/v2"

	"github.com/yousysadmin/pacer/internal/core/env"
	"github.com/yousysadmin/pacer/internal/domain/backup"
	projectmodel "github.com/yousysadmin/pacer/internal/models/project"
	"github.com/yousysadmin/pacer/internal/testutil/runtimeutil"
)

func newApp(t *testing.T) (*fiber.App, *env.Runtime) {
	t.Helper()
	rt := runtimeutil.NewRuntime(t, &env.Config{})
	// Set an atomic bootstrap-API-token so materializeLT's load
	// doesn't panic in tests. EC2 is nil so the real AWS call is
	// short-circuited to the lt-dev-* placeholder path.
	var tok atomic.Value
	tok.Store("test-token")
	rt.BootstrapAPIToken = tok
	h := &backup.Handler{Runtime: rt}
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Post("/api/backup/import", h.Import)
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

type importResp struct {
	Projects struct{ Created, Updated int } `json:"projects"`
	Pools    struct{ Created, Updated int } `json:"pools"`
	Repos    struct{ Created, Updated int } `json:"repos"`
	Errors   []string                       `json:"errors"`
}

func decodeImport(t *testing.T, r *http.Response) importResp {
	t.Helper()
	var out importResp
	if err := json.NewDecoder(r.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}

// validPool returns a minimal pool snapshot that satisfies every
// required field + Normalize default (so we can flip ONE field at a
// time in the tests below to exercise a specific validator).
func validPool() map[string]any {
	return map[string]any{
		"name":               "ci-default",
		"ami_id":             "ami-0123456789abcdef0",
		"instance_types":     []string{"t3.small"},
		"subnet_ids":         []string{"subnet-12345678"},
		"security_group_ids": []string{"sg-12345678"},
	}
}

func validBackup(projects ...map[string]any) map[string]any {
	return map[string]any{
		"version":      1,
		"exported_at":  "2025-01-01T00:00:00Z",
		"projects":     projects,
	}
}

func TestImport_HappyPath(t *testing.T) {
	app, rt := newApp(t)
	body := validBackup(map[string]any{
		"name":  "demo",
		"pools": []map[string]any{validPool()},
		"repos": []map[string]any{{"full_name": "octocat/hello"}},
	})
	resp := postJSON(t, app, "/api/backup/import", body)
	if resp.StatusCode != 200 {
		t.Fatalf("status: want 200, got %d", resp.StatusCode)
	}
	r := decodeImport(t, resp)
	if len(r.Errors) != 0 {
		t.Fatalf("errors: %v", r.Errors)
	}
	if r.Projects.Created != 1 || r.Pools.Created != 1 || r.Repos.Created != 1 {
		t.Fatalf("counts: %+v", r)
	}
	got, err := rt.Store.Project.GetByName(context.Background(), "demo")
	if err != nil || got == nil {
		t.Fatalf("GetByName: %v %v", got, err)
	}
}

func TestImport_RejectsReservedTagOnProject(t *testing.T) {
	app, rt := newApp(t)
	body := validBackup(map[string]any{
		"name": "demo",
		"tags": map[string]string{"gha:managed-by": "attacker"},
	})
	resp := postJSON(t, app, "/api/backup/import", body)
	r := decodeImport(t, resp)
	if len(r.Errors) == 0 {
		t.Fatal("expected error for reserved gha:* tag on project")
	}
	if !strings.Contains(strings.Join(r.Errors, " "), "reserved") {
		t.Fatalf("expected reason to mention 'reserved': %v", r.Errors)
	}
	// Project must NOT have been persisted.
	got, _ := rt.Store.Project.GetByName(context.Background(), "demo")
	if got != nil {
		t.Fatal("project should not have been persisted")
	}
}

func TestImport_RejectsReservedTagCaseInsensitive(t *testing.T) {
	app, _ := newApp(t)
	body := validBackup(map[string]any{
		"name": "demo",
		"tags": map[string]string{"GHA:foo": "x"},
	})
	resp := postJSON(t, app, "/api/backup/import", body)
	r := decodeImport(t, resp)
	if len(r.Errors) == 0 {
		t.Fatal("expected error for case-variant gha:* tag")
	}
}

func TestImport_RejectsReservedTagOnPool(t *testing.T) {
	app, _ := newApp(t)
	p := validPool()
	p["tags"] = map[string]string{"gha:project": "spoof"}
	body := validBackup(map[string]any{
		"name":  "demo",
		"pools": []map[string]any{p},
	})
	resp := postJSON(t, app, "/api/backup/import", body)
	r := decodeImport(t, resp)
	if len(r.Errors) == 0 {
		t.Fatal("expected error for reserved gha:* tag on pool")
	}
	if r.Pools.Created != 0 {
		t.Fatalf("pool should not have been persisted: %+v", r)
	}
}

func TestImport_RejectsBadRunnerUser(t *testing.T) {
	app, _ := newApp(t)
	p := validPool()
	// Shell-metacharacter payload that the posix_user validator
	// catches at the CRUD edge.
	p["runner_user"] = "nobody;rm -rf /"
	body := validBackup(map[string]any{
		"name":  "demo",
		"pools": []map[string]any{p},
	})
	resp := postJSON(t, app, "/api/backup/import", body)
	r := decodeImport(t, resp)
	if len(r.Errors) == 0 {
		t.Fatal("expected error for shell-injection runner_user")
	}
	// Friendly message references the field label + the charset
	// rule, without leaking the json field name.
	joined := strings.Join(r.Errors, " ")
	if !strings.Contains(joined, "Run runner as") || !strings.Contains(joined, "lowercase") {
		t.Fatalf("expected reason to reference the runner-user field + charset rule: %v", r.Errors)
	}
}

func TestImport_RejectsSelfHostedShadow(t *testing.T) {
	app, _ := newApp(t)
	p := validPool()
	p["extra_labels"] = []string{"self-hosted"}
	body := validBackup(map[string]any{
		"name":  "demo",
		"pools": []map[string]any{p},
	})
	resp := postJSON(t, app, "/api/backup/import", body)
	r := decodeImport(t, resp)
	if len(r.Errors) == 0 {
		t.Fatal("expected error for self-hosted in extra_labels")
	}
}

func TestImport_RejectsInvalidScope(t *testing.T) {
	app, _ := newApp(t)
	body := validBackup(map[string]any{
		"name":  "demo",
		"scope": "weird",
	})
	resp := postJSON(t, app, "/api/backup/import", body)
	r := decodeImport(t, resp)
	if len(r.Errors) == 0 {
		t.Fatal("expected error for invalid scope")
	}
}

func TestImport_OrgScopeRequiresOrgName(t *testing.T) {
	app, _ := newApp(t)
	body := validBackup(map[string]any{
		"name":  "demo",
		"scope": "org",
	})
	resp := postJSON(t, app, "/api/backup/import", body)
	r := decodeImport(t, resp)
	if len(r.Errors) == 0 {
		t.Fatal("expected error for org scope without org_name")
	}
	if !strings.Contains(strings.Join(r.Errors, " "), "Org login") {
		t.Fatalf("expected reason to mention the org login field: %v", r.Errors)
	}
}

func TestImport_RejectsMalformedRepoFullName(t *testing.T) {
	app, _ := newApp(t)
	body := validBackup(map[string]any{
		"name":  "demo",
		"repos": []map[string]any{{"full_name": "not-a-slash-pair"}},
	})
	resp := postJSON(t, app, "/api/backup/import", body)
	r := decodeImport(t, resp)
	if len(r.Errors) == 0 {
		t.Fatal("expected error for malformed repo full_name")
	}
	if r.Repos.Created != 0 {
		t.Fatalf("repo should not have been persisted: %+v", r)
	}
}

func TestImport_RejectsPoolNameNonCanonical(t *testing.T) {
	app, _ := newApp(t)
	p := validPool()
	// runner_label_strict requires SanitizeLabel(s) == s. Mixed case
	// fails because Sanitize lowercases.
	p["name"] = "CI-Default"
	body := validBackup(map[string]any{
		"name":  "demo",
		"pools": []map[string]any{p},
	})
	resp := postJSON(t, app, "/api/backup/import", body)
	r := decodeImport(t, resp)
	if len(r.Errors) == 0 {
		t.Fatal("expected error for non-canonical pool name")
	}
}

func TestImport_DefaultsScopeToRepo(t *testing.T) {
	app, rt := newApp(t)
	body := validBackup(map[string]any{
		"name": "demo",
	})
	resp := postJSON(t, app, "/api/backup/import", body)
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	r := decodeImport(t, resp)
	if len(r.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", r.Errors)
	}
	got, err := rt.Store.Project.GetByName(context.Background(), "demo")
	if err != nil || got == nil {
		t.Fatalf("GetByName: %v %v", got, err)
	}
	if got.Scope != projectmodel.ScopeRepo {
		t.Fatalf("scope: want %q, got %q", projectmodel.ScopeRepo, got.Scope)
	}
}

func TestImport_PartialImportProceedsAfterRowError(t *testing.T) {
	// One project is invalid (reserved tag) and one is valid. The
	// valid project should still be persisted -- per-row failures
	// don't abort the whole import.
	app, rt := newApp(t)
	body := validBackup(
		map[string]any{
			"name": "broken",
			"tags": map[string]string{"gha:nope": "x"},
		},
		map[string]any{
			"name": "ok",
		},
	)
	resp := postJSON(t, app, "/api/backup/import", body)
	r := decodeImport(t, resp)
	if len(r.Errors) == 0 {
		t.Fatal("expected error for the broken project")
	}
	if r.Projects.Created != 1 {
		t.Fatalf("good project should still be created: %+v", r)
	}
	got, _ := rt.Store.Project.GetByName(context.Background(), "ok")
	if got == nil {
		t.Fatal("valid project should be persisted alongside the rejected one")
	}
	broken, _ := rt.Store.Project.GetByName(context.Background(), "broken")
	if broken != nil {
		t.Fatal("invalid project must not be persisted")
	}
}
