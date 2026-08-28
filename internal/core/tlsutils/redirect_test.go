// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package tlsutils

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRedirectToHTTPS_AllowlistedHostKept(t *testing.T) {
	h := redirectToHTTPS([]string{"pacer.example.com"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://pacer.example.com:80/api/x?a=1", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusPermanentRedirect {
		t.Fatalf("code %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "https://pacer.example.com/api/x?a=1" {
		t.Fatalf("location %q", loc)
	}
}

func TestRedirectToHTTPS_ForeignHostRewritten(t *testing.T) {
	h := redirectToHTTPS([]string{"pacer.example.com"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://evil.example/login", nil)
	req.Host = "evil.example"
	h.ServeHTTP(rec, req)
	if loc := rec.Header().Get("Location"); loc != "https://pacer.example.com/login" {
		t.Fatalf("open redirect: %q", loc)
	}
}

func TestRedirectToHTTPS_NoAllowlistEchoesHost(t *testing.T) {
	h := redirectToHTTPS(nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://any.example/", nil)
	req.Host = "any.example"
	h.ServeHTTP(rec, req)
	if loc := rec.Header().Get("Location"); loc != "https://any.example/" {
		t.Fatalf("location %q", loc)
	}
}
