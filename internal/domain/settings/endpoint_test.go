// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package settings_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"

	"github.com/yousysadmin/pacer/internal/core/env"
	"github.com/yousysadmin/pacer/internal/core/validation"
	settingsdomain "github.com/yousysadmin/pacer/internal/domain/settings"
	auditmodel "github.com/yousysadmin/pacer/internal/models/audit"
	settingsmodel "github.com/yousysadmin/pacer/internal/models/settings"
	"github.com/yousysadmin/pacer/internal/testutil/runtimeutil"
)

func init() { validation.Init() }

func newApp(t *testing.T, auditDays, webhookDays int) (*fiber.App, *env.Runtime) {
	t.Helper()
	rt := runtimeutil.NewRuntime(t, &env.Config{
		// Job-log retention is not parameterized: no existing caller
		// varies it, and a fixed default keeps the new assertions
		// readable.
		Retention: env.RetentionConfig{AuditDays: auditDays, WebhookDays: webhookDays, JobLogDays: 31},
	})
	h := &settingsdomain.Handler{Runtime: rt}
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Get("/api/settings/retention", h.GetRetention)
	app.Put("/api/settings/retention", h.PutRetention)
	return app, rt
}

func get(t *testing.T, app *fiber.App, path string) (*http.Response, []byte) {
	t.Helper()
	resp, err := app.Test(httptest.NewRequest(http.MethodGet, path, nil), -1)
	if err != nil {
		t.Fatalf("app.Test GET: %v", err)
	}
	b := new(bytes.Buffer)
	_, _ = b.ReadFrom(resp.Body)
	return resp, b.Bytes()
}

func putJSON(t *testing.T, app *fiber.App, path string, body any) (*http.Response, []byte) {
	t.Helper()
	bd, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPut, path, bytes.NewReader(bd))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("app.Test PUT: %v", err)
	}
	b := new(bytes.Buffer)
	_, _ = b.ReadFrom(resp.Body)
	return resp, b.Bytes()
}

func TestGetRetention_DefaultsWhenUnset(t *testing.T) {
	app, _ := newApp(t, 90, 7)
	resp, body := get(t, app, "/api/settings/retention")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body=%s", resp.StatusCode, body)
	}
	var got struct {
		AuditDays         int  `json:"audit_days"`
		WebhookDays       int  `json:"webhook_days"`
		AuditDefault      int  `json:"audit_default"`
		WebhookDefault    int  `json:"webhook_default"`
		AuditOverridden   bool `json:"audit_overridden"`
		WebhookOverridden bool `json:"webhook_overridden"`
	}
	_ = json.Unmarshal(body, &got)
	if got.AuditDays != 90 || got.WebhookDays != 7 {
		t.Errorf("effective: %+v", got)
	}
	if got.AuditDefault != 90 || got.WebhookDefault != 7 {
		t.Errorf("default echo: %+v", got)
	}
	if got.AuditOverridden || got.WebhookOverridden {
		t.Errorf("nothing overridden yet: %+v", got)
	}
}

func TestPutRetention_StoresOverrideAndReflectsInGet(t *testing.T) {
	app, rt := newApp(t, 90, 7)

	d30 := 30
	resp, body := putJSON(t, app, "/api/settings/retention", map[string]any{
		"audit_days": d30,
	})
	if resp.StatusCode != 200 {
		t.Fatalf("PUT status: %d body=%s", resp.StatusCode, body)
	}
	// Direct DB check: the override row exists with value "30".
	row, _ := rt.Store.Settings.Get(t.Context(), settingsmodel.KeyAuditRetentionDays)
	if row == nil || row.Value != "30" {
		t.Fatalf("settings row: got %+v", row)
	}
	// GET reflects the override.
	resp2, body2 := get(t, app, "/api/settings/retention")
	if resp2.StatusCode != 200 {
		t.Fatalf("GET status: %d body=%s", resp2.StatusCode, body2)
	}
	var got struct {
		AuditDays       int  `json:"audit_days"`
		AuditOverridden bool `json:"audit_overridden"`
	}
	_ = json.Unmarshal(body2, &got)
	if got.AuditDays != 30 || !got.AuditOverridden {
		t.Fatalf("post-PUT GET: %+v", got)
	}
}

func TestPutRetention_ZeroClearsOverride(t *testing.T) {
	app, rt := newApp(t, 90, 7)

	// Pre-seed an override.
	_ = rt.Store.Settings.Put(t.Context(),
		settingsmodel.KeyAuditRetentionDays, "30")

	// PUT 0 must clear the override (revert to YAML default).
	zero := 0
	resp, body := putJSON(t, app, "/api/settings/retention", map[string]any{
		"audit_days": zero,
	})
	if resp.StatusCode != 200 {
		t.Fatalf("PUT 0 status: %d body=%s", resp.StatusCode, body)
	}
	row, _ := rt.Store.Settings.Get(t.Context(), settingsmodel.KeyAuditRetentionDays)
	if row == nil {
		t.Fatal("row should still exist with empty value, not be deleted")
	}
	if row.Value != "" {
		t.Errorf("Value: want empty (default), got %q", row.Value)
	}
	// Effective falls back to YAML default.
	if eff := settingsdomain.EffectiveAuditDays(t.Context(), rt); eff != 90 {
		t.Errorf("effective post-clear: want 90, got %d", eff)
	}
}

func TestPutRetention_RejectsOutOfRange(t *testing.T) {
	app, _ := newApp(t, 90, 7)
	cases := []struct {
		name string
		body any
	}{
		{"negative audit", map[string]any{"audit_days": -1}},
		{"audit too large", map[string]any{"audit_days": 99999}},
		{"negative webhook", map[string]any{"webhook_days": -5}},
		{"webhook too large", map[string]any{"webhook_days": 9999}},
		{"empty body", map[string]any{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp, _ := putJSON(t, app, "/api/settings/retention", c.body)
			if resp.StatusCode != 400 {
				t.Fatalf("status: want 400, got %d", resp.StatusCode)
			}
		})
	}
}

func TestPutRetention_BothFieldsAtOnce(t *testing.T) {
	app, _ := newApp(t, 90, 7)
	a, w := 60, 14
	resp, body := putJSON(t, app, "/api/settings/retention", map[string]any{
		"audit_days":   a,
		"webhook_days": w,
	})
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body=%s", resp.StatusCode, body)
	}
	resp2, body2 := get(t, app, "/api/settings/retention")
	if resp2.StatusCode != 200 {
		t.Fatalf("GET: %d body=%s", resp2.StatusCode, body2)
	}
	var got struct {
		AuditDays   int `json:"audit_days"`
		WebhookDays int `json:"webhook_days"`
	}
	_ = json.Unmarshal(body2, &got)
	if got.AuditDays != 60 || got.WebhookDays != 14 {
		t.Fatalf("got %+v", got)
	}
}

func TestPutRetention_WritesAuditRow(t *testing.T) {
	app, rt := newApp(t, 90, 7)
	resp, body := putJSON(t, app, "/api/settings/retention", map[string]any{"audit_days": 30, "webhook_days": 3})
	if resp.StatusCode != 200 {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	entries, err := rt.Store.Audit.List(t.Context(), auditmodel.ListFilter{Action: auditmodel.ActionRetentionUpdated, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("want 1 audit row, got %d", len(entries))
	}
	if !strings.Contains(entries[0].Detail, `"audit_days":30`) {
		t.Fatalf("detail missing values: %s", entries[0].Detail)
	}
}

// TestRetention_JobLogRoundTrip: the third period has to travel the
// whole way - GET echoes the default, PUT stores an override, and
// GET reflects it. A field wired into only one of the two shows up
// as a value that reverts on the next page load.
func TestRetention_JobLogRoundTrip(t *testing.T) {
	app, _ := newApp(t, 90, 7)

	var got struct {
		JobLogDays       int  `json:"job_log_days"`
		JobLogDefault    int  `json:"job_log_default"`
		JobLogOverridden bool `json:"job_log_overridden"`
	}
	_, body := get(t, app, "/api/settings/retention")
	_ = json.Unmarshal(body, &got)
	if got.JobLogDays != 31 || got.JobLogDefault != 31 {
		t.Fatalf("defaults: %+v", got)
	}
	if got.JobLogOverridden {
		t.Fatalf("nothing overridden yet: %+v", got)
	}

	resp, body := putJSON(t, app, "/api/settings/retention", map[string]any{"job_log_days": 14})
	if resp.StatusCode != 200 {
		t.Fatalf("put: %d %s", resp.StatusCode, body)
	}
	_ = json.Unmarshal(body, &got)
	if got.JobLogDays != 14 || !got.JobLogOverridden {
		t.Fatalf("put response: %+v", got)
	}

	_, body = get(t, app, "/api/settings/retention")
	_ = json.Unmarshal(body, &got)
	if got.JobLogDays != 14 || !got.JobLogOverridden {
		t.Fatalf("get after put: %+v", got)
	}

	// 0 is the documented "use the YAML default again" sentinel.
	_, body = putJSON(t, app, "/api/settings/retention", map[string]any{"job_log_days": 0})
	_ = json.Unmarshal(body, &got)
	if got.JobLogDays != 31 || got.JobLogOverridden {
		t.Fatalf("clearing the override: %+v", got)
	}
}

func TestRetention_JobLogRejectsOutOfRange(t *testing.T) {
	app, _ := newApp(t, 90, 7)
	for _, v := range []int{-1, 400} {
		resp, body := putJSON(t, app, "/api/settings/retention", map[string]any{"job_log_days": v})
		if resp.StatusCode != 400 {
			t.Errorf("job_log_days=%d: status %d, want 400 (%s)", v, resp.StatusCode, body)
		}
	}
}

// TestRetention_RejectsWholeBodyBeforeWriting: validation runs over
// every field first, so a bad third value cannot leave the first two
// applied.
func TestRetention_RejectsWholeBodyBeforeWriting(t *testing.T) {
	app, _ := newApp(t, 90, 7)
	resp, _ := putJSON(t, app, "/api/settings/retention", map[string]any{
		"audit_days": 10, "job_log_days": 9999,
	})
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d, want 400", resp.StatusCode)
	}

	var got struct {
		AuditDays int `json:"audit_days"`
	}
	_, body := get(t, app, "/api/settings/retention")
	_ = json.Unmarshal(body, &got)
	if got.AuditDays != 90 {
		t.Fatalf("audit_days was applied despite the rejected body: %d", got.AuditDays)
	}
}
