// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package ghapp

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func newTestClient(t *testing.T, srvURL string) *Client {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	old := apiBase
	apiBase = srvURL
	t.Cleanup(func() { apiBase = old })
	return &Client{
		appID:      1,
		privateKey: key,
		http:       &http.Client{Timeout: 30 * time.Second},
		tokens:     map[int64]*cachedToken{},
	}
}

func tokenResponse(w http.ResponseWriter) {
	w.WriteHeader(http.StatusCreated)
	_, _ = w.Write([]byte(`{"token":"ghs_test","expires_at":"2099-01-01T00:00:00Z"}`))
}

func TestInstallationToken_HappyPathAndCache(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		tokenResponse(w)
	}))
	defer srv.Close()
	c := newTestClient(t, srv.URL)

	tok, err := c.InstallationToken(t.Context(), 42)
	if err != nil {
		t.Fatalf("InstallationToken: %v", err)
	}
	if tok != "ghs_test" {
		t.Fatalf("token: %q", tok)
	}
	// Second call must come from the cache.
	if _, err := c.InstallationToken(t.Context(), 42); err != nil {
		t.Fatalf("cached InstallationToken: %v", err)
	}
	if calls != 1 {
		t.Fatalf("upstream calls: want 1, got %d", calls)
	}
}

func TestInstallationToken_UpstreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"message":"bad credentials"}`, http.StatusUnauthorized)
	}))
	defer srv.Close()
	c := newTestClient(t, srv.URL)

	if _, err := c.InstallationToken(t.Context(), 42); err == nil {
		t.Fatal("want error on upstream 401")
	}
}

// The singleflight closure must not die with the INITIATING caller's
// context: if the first request that opens the flight is aborted, a
// concurrent waiter (e.g. a runner's JIT-config mint) must still get
// the token.
func TestInstallationToken_InitiatorCancelDoesNotFailWaiters(t *testing.T) {
	inFlight := make(chan struct{})
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(inFlight)
		<-release
		tokenResponse(w)
	}))
	defer srv.Close()
	c := newTestClient(t, srv.URL)

	ctxA, cancelA := context.WithCancel(t.Context())
	var wg sync.WaitGroup
	wg.Go(func() {
		// Initiates the flight. Its result is irrelevant (may error or
		// succeed depending on how far the fetch got).
		_, _ = c.InstallationToken(ctxA, 42)
	})

	<-inFlight // upstream call is in progress, flight is open
	wg.Add(1)
	var tokB string
	var errB error
	go func() {
		defer wg.Done()
		tokB, errB = c.InstallationToken(t.Context(), 42)
	}()

	// Give B a moment to join the open flight, then abort A mid-fetch.
	time.Sleep(20 * time.Millisecond)
	cancelA()
	time.Sleep(20 * time.Millisecond)
	close(release)
	wg.Wait()

	if errB != nil {
		t.Fatalf("waiter failed after initiator cancel: %v", errB)
	}
	if tokB != "ghs_test" {
		t.Fatalf("waiter token: %q", tokB)
	}
}

func TestJITConfigOrg_EscapesOrgPathSegment(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/access_tokens") {
			tokenResponse(w)
			return
		}
		gotPath = r.URL.EscapedPath()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"encoded_jit_config":"abc","runner":{"id":7}}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv.URL)
	if _, _, err := c.JITConfigOrg(t.Context(), 1, "evil/../../admin", "r", []string{"a"}, 1); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if gotPath != "/orgs/evil%2F..%2F..%2Fadmin/actions/runners/generate-jitconfig" {
		t.Fatalf("org segment not escaped: %q", gotPath)
	}
}
