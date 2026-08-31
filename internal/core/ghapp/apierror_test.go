// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package ghapp

import (
	"errors"
	"net/http"
	"strings"
	"testing"
)

// TestAPIError_Temporary is the decision the whole type exists for:
// whether a runner should spend ~72 seconds of billed instance time
// asking GitHub the same question again.
func TestAPIError_Temporary(t *testing.T) {
	cases := []struct {
		code int
		want bool
	}{
		{http.StatusUnauthorized, false},        // token revoked -- a human must fix it
		{http.StatusForbidden, false},           // App lost access to the repo
		{http.StatusNotFound, false},            // repo or org gone
		{http.StatusUnprocessableEntity, false}, // bad runner group / labels
		{http.StatusRequestTimeout, true},
		{http.StatusTooManyRequests, true}, // rate limited: back off and retry
		{http.StatusInternalServerError, true},
		{http.StatusBadGateway, true},
		{http.StatusServiceUnavailable, true},
	}
	for _, c := range cases {
		e := &APIError{Op: "jitconfig", StatusCode: c.code}
		if got := e.Temporary(); got != c.want {
			t.Errorf("status %d: Temporary() = %v, want %v", c.code, got, c.want)
		}
	}
}

// TestAPIError_MessageCarriesGitHubsWords: the message ends up in a
// job's failure_log, where it is the only explanation an operator
// gets. "403 Forbidden" alone does not distinguish a revoked
// installation from a repo the App was never added to.
func TestAPIError_MessageCarriesGitHubsWords(t *testing.T) {
	e := newAPIError("jitconfig", http.StatusForbidden, "403 Forbidden",
		[]byte(`{"message":"Resource not accessible by integration"}`))

	msg := e.Error()
	for _, want := range []string{"jitconfig", "403 Forbidden", "Resource not accessible by integration"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q missing %q", msg, want)
		}
	}
}

// TestAPIError_UnwrapsGitHubEnvelope: the operator should read
// GitHub's sentence, not our own escaping of GitHub's JSON. The raw
// form arrives in a failure log as
// {\"message\":\"Bad credentials\",\"documentation_url\":...}.
func TestAPIError_UnwrapsGitHubEnvelope(t *testing.T) {
	e := newAPIError("jitconfig", http.StatusForbidden, "403 Forbidden", []byte(
		`{"message":"Resource not accessible by integration","documentation_url":"https://docs.github.com/rest","status":"403"}`))
	if e.Body != "Resource not accessible by integration" {
		t.Fatalf("body: got %q", e.Body)
	}
	if strings.Contains(e.Error(), "documentation_url") {
		t.Errorf("boilerplate should not reach the log: %q", e.Error())
	}
}

// A 422 puts the actionable part in "errors", not in "message"
// ("Validation Failed" alone says nothing).
func TestAPIError_KeepsPerFieldErrors(t *testing.T) {
	e := newAPIError("jitconfig", http.StatusUnprocessableEntity, "422 Unprocessable Entity", []byte(
		`{"message":"Validation Failed","errors":[{"resource":"Runner","field":"runner_group_id","code":"invalid"}]}`))
	for _, want := range []string{"Validation Failed", "Runner.runner_group_id invalid"} {
		if !strings.Contains(e.Body, want) {
			t.Errorf("body %q missing %q", e.Body, want)
		}
	}
}

// A proxy answering with HTML is not the GitHub envelope, and must
// still leave something behind rather than an empty reason.
func TestAPIError_NonEnvelopeBodyKept(t *testing.T) {
	e := newAPIError("jitconfig", http.StatusBadGateway, "502 Bad Gateway",
		[]byte("<html>\n  <body>502 Bad Gateway</body>\n</html>"))
	if !strings.Contains(e.Body, "502 Bad Gateway</body>") {
		t.Fatalf("non-JSON body dropped: %q", e.Body)
	}
}

func TestAPIError_EmptyBodyStillReadable(t *testing.T) {
	e := newAPIError("installation token", http.StatusUnauthorized, "401 Unauthorized", nil)
	if got, want := e.Error(), "installation token: 401 Unauthorized"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestAPIError_BodyIsFlattenedAndCapped: a proxy answering with an
// HTML page must not push 40 KB of markup into every failure log,
// and newlines would break the one-line-per-event shape of the log.
func TestAPIError_BodyIsFlattenedAndCapped(t *testing.T) {
	e := newAPIError("jitconfig", 500, "500 Internal Server Error",
		[]byte("line one\n\tline two   with   gaps\n"))
	if strings.ContainsAny(e.Body, "\n\t") {
		t.Fatalf("body not flattened: %q", e.Body)
	}
	if e.Body != "line one line two with gaps" {
		t.Fatalf("body: got %q", e.Body)
	}

	long := newAPIError("jitconfig", 500, "500", []byte(strings.Repeat("x", bodyExcerptMax*2)))
	if len(long.Body) > bodyExcerptMax+3 {
		t.Fatalf("body not capped: %d chars", len(long.Body))
	}
	if !strings.HasSuffix(long.Body, "...") {
		t.Fatalf("truncation not marked: %q", long.Body[len(long.Body)-10:])
	}
}

// TestAPIError_IsUnwrappable: callers match with errors.AsType, which
// only works if the error travels as a pointer all the way up.
func TestAPIError_IsUnwrappable(t *testing.T) {
	var err error = newAPIError("jitconfig", http.StatusForbidden, "403 Forbidden", []byte("no"))
	got, ok := errors.AsType[*APIError](err)
	if !ok {
		t.Fatal("APIError did not match through errors.AsType")
	}
	if got.StatusCode != http.StatusForbidden {
		t.Fatalf("status: got %d", got.StatusCode)
	}
}
