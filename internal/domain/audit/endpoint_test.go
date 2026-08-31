// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package audit_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/yousysadmin/pacer/internal/core/env"
	"github.com/yousysadmin/pacer/internal/core/validation"
	"github.com/yousysadmin/pacer/internal/domain/audit"
	auditmodel "github.com/yousysadmin/pacer/internal/models/audit"
	"github.com/yousysadmin/pacer/internal/testutil/runtimeutil"
)

func init() { validation.Init() }

func newApp(t *testing.T) (*fiber.App, *env.Runtime) {
	t.Helper()
	rt := runtimeutil.NewRuntime(t, &env.Config{})
	h := &audit.Handler{Runtime: rt}
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Get("/api/audit", h.List)
	app.Post("/api/audit/prune", h.Prune)
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

func decodeBody(t *testing.T, r *http.Response, into any) {
	t.Helper()
	buf := new(bytes.Buffer)
	if _, err := buf.ReadFrom(r.Body); err != nil {
		t.Fatalf("read body: %v", err)
	}
	if err := json.Unmarshal(buf.Bytes(), into); err != nil {
		t.Fatalf("decode body %q: %v", buf.String(), err)
	}
}

func TestPrune_DeletesOnlyOlderThanCutoff(t *testing.T) {
	app, rt := newApp(t)
	ctx := t.Context()
	now := time.Now().UTC()

	// 10 rows: 5 older than 30 days, 5 newer.
	for i := range 5 {
		_ = rt.Store.Audit.Put(ctx, &auditmodel.Entry{
			ID:         fmt.Sprintf("old-%d", i),
			Action:     auditmodel.ActionProjectCreated,
			OccurredAt: now.Add(-60 * 24 * time.Hour).Add(time.Duration(i) * time.Minute),
		})
	}
	for i := range 5 {
		_ = rt.Store.Audit.Put(ctx, &auditmodel.Entry{
			ID:         fmt.Sprintf("new-%d", i),
			Action:     auditmodel.ActionProjectCreated,
			OccurredAt: now.Add(-5 * 24 * time.Hour).Add(time.Duration(i) * time.Minute),
		})
	}

	resp := postJSON(t, app, "/api/audit/prune", map[string]any{"older_than_days": 30})
	if resp.StatusCode != 200 {
		t.Fatalf("status: want 200, got %d", resp.StatusCode)
	}
	var body struct {
		Deleted       int       `json:"deleted"`
		Cutoff        time.Time `json:"cutoff"`
		OlderThanDays int       `json:"older_than_days"`
	}
	decodeBody(t, resp, &body)
	if body.Deleted != 5 {
		t.Fatalf("deleted: want 5, got %d", body.Deleted)
	}
	if body.OlderThanDays != 30 {
		t.Errorf("older_than_days echo: %d", body.OlderThanDays)
	}

	// The 5 "new" rows must survive. None of the "old" rows do.
	n, _ := rt.Store.Audit.Count(ctx, auditmodel.ListFilter{})
	// 5 surviving rows + 1 audit.pruned row that the endpoint
	// wrote to record its own action.
	if n != 6 {
		t.Errorf("post-prune count: want 6 (5 survivors + 1 prune record), got %d", n)
	}

	// The prune record must exist and carry the cutoff in its detail.
	pruneRows, _ := rt.Store.Audit.List(ctx, auditmodel.ListFilter{
		Action: auditmodel.ActionAuditPruned,
	})
	if len(pruneRows) != 1 {
		t.Fatalf("audit.pruned rows: want 1, got %d", len(pruneRows))
	}
	if pruneRows[0].Detail == "" {
		t.Error("audit.pruned detail empty - cutoff + count should be recorded")
	}
}

func TestPrune_AuditRecordSurvivesItsOwnCutoff(t *testing.T) {
	// The whole point of the post-prune audit row: it must land
	// AFTER the cutoff so it isn't deleted by the very prune it
	// describes. Regression for the obvious off-by-one footgun.
	app, rt := newApp(t)
	ctx := t.Context()

	// 30-day prune. The audit-of-prune row is stamped at "now",
	// which is by definition > now-30d.
	resp := postJSON(t, app, "/api/audit/prune", map[string]any{"older_than_days": 30})
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}

	rows, _ := rt.Store.Audit.List(ctx, auditmodel.ListFilter{
		Action: auditmodel.ActionAuditPruned,
	})
	if len(rows) != 1 {
		t.Fatalf("want 1 audit.pruned row, got %d", len(rows))
	}
	// And a second prune doesn't wipe the first one (the prior
	// run's record is also < 1 day old).
	_ = postJSON(t, app, "/api/audit/prune", map[string]any{"older_than_days": 30})
	rows, _ = rt.Store.Audit.List(ctx, auditmodel.ListFilter{
		Action: auditmodel.ActionAuditPruned,
	})
	if len(rows) != 2 {
		t.Fatalf("want 2 audit.pruned rows after second prune, got %d", len(rows))
	}
}

func TestPrune_RejectsBadInput(t *testing.T) {
	app, _ := newApp(t)
	cases := []struct {
		name string
		body any
	}{
		{"zero days", map[string]any{"older_than_days": 0}},
		{"negative days", map[string]any{"older_than_days": -1}},
		{"too many days", map[string]any{"older_than_days": 4000}},
		{"missing field", map[string]any{}},
		{"non-integer", map[string]any{"older_than_days": "thirty"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp := postJSON(t, app, "/api/audit/prune", c.body)
			if resp.StatusCode != 400 {
				t.Fatalf("want 400, got %d", resp.StatusCode)
			}
		})
	}
}

func TestPrune_EmptyTable_DeletedZero(t *testing.T) {
	// No rows exist - prune must succeed cleanly and report 0
	// deleted. The prune-record row still lands (and that's the
	// only row that exists afterward).
	app, rt := newApp(t)
	resp := postJSON(t, app, "/api/audit/prune", map[string]any{"older_than_days": 7})
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	var body struct {
		Deleted int `json:"deleted"`
	}
	decodeBody(t, resp, &body)
	if body.Deleted != 0 {
		t.Errorf("deleted on empty table: want 0, got %d", body.Deleted)
	}
	n, _ := rt.Store.Audit.Count(t.Context(), auditmodel.ListFilter{})
	if n != 1 {
		t.Errorf("post-prune count: want 1 (the prune record itself), got %d", n)
	}
}
