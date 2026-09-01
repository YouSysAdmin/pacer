// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package ec2lt

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yousysadmin/pacer/internal/models/pool"
)

// TestUserData_ReportsRunStageFailure: ./run.sh exiting non-zero is
// caught by "|| RUNNER_EXIT=$?", so the ERR trap never fires. The
// script must report that case explicitly or the log dies with the
// instance a minute later.
func TestUserData_ReportsRunStageFailure(t *testing.T) {
	got, err := renderUserData(&pool.Pool{}, "https://pacer.example", "2.319.1", "tok")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, want := range []string{
		`if [ "$RUNNER_EXIT" -ne 0 ]; then`,
		`--arg stage "run"`,
		"/api/runner/error",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("user-data missing run-failure report %q", want)
		}
	}
	// It must still complete afterwards: the instance really did
	// terminate, and cost finalization hangs off that call.
	runIdx := strings.Index(got, `RUNNER FAIL stage=run`)
	completeIdx := strings.Index(got, "/api/runner/complete")
	if runIdx < 0 || completeIdx < 0 || completeIdx < runIdx {
		t.Fatalf("complete must still run after the run-failure report (run=%d complete=%d)", runIdx, completeIdx)
	}
}

// TestUserData_NoBareFailFlag: "curl -f" throws the response body
// away, reducing a 424 that carries GitHub's explanation to a bare
// status code.
func TestUserData_NoBareFailFlag(t *testing.T) {
	got, err := renderUserData(&pool.Pool{}, "https://pacer.example", "2.319.1", "tok")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for line := range strings.SplitSeq(got, "\n") {
		if !strings.Contains(line, "curl ") {
			continue
		}
		// IMDS and the runner tarball are not pacer API calls - they
		// have no error envelope to lose.
		if strings.Contains(line, "169.254.169.254") || strings.Contains(line, "-fsSL") {
			continue
		}
		if strings.Contains(line, "$SERVER_URL") && strings.Contains(line, "-f") {
			t.Errorf("pacer API call still discards the body with -f: %s", strings.TrimSpace(line))
		}
	}
}

// extractShellFunc pulls one function out of the rendered script so a
// test can run it for real. Brittle by nature, so it fails loudly
// rather than silently testing nothing.
func extractShellFunc(t *testing.T, script, name string) string {
	t.Helper()
	start := strings.Index(script, name+"() {")
	if start < 0 {
		t.Fatalf("function %s() not found in rendered user-data", name)
	}
	rest := script[start:]
	end := strings.Index(rest, "\n}\n")
	if end < 0 {
		t.Fatalf("no closing brace for %s()", name)
	}
	return rest[:end+3]
}

// TestUserData_APIPostSurfacesErrorBody runs the helper for real
// against a server that refuses the way pacer does: what matters is
// what ends up in the log, which a string-matching test cannot
// check.
func TestUserData_APIPostSurfacesErrorBody(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl not available")
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ok" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"job_id":"j-1"}`))
			return
		}
		// 424 is what a permanent GitHub refusal looks like, and it
		// is deliberately outside curl's retry set.
		w.WriteHeader(http.StatusFailedDependency)
		_, _ = w.Write([]byte(`{"error":"jitconfig: 403 Forbidden: Resource not accessible by integration"}`))
	}))
	defer srv.Close()

	script, err := renderUserData(&pool.Pool{}, srv.URL, "2.319.1", "tok")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	fn := extractShellFunc(t, script, "api_post")

	harness := "#!/usr/bin/env bash\nset -uo pipefail\nSERVER_URL=" + srv.URL + "\n" + fn + `
if body=$(api_post /ok '{}'); then
    echo "OK_BODY=$body"
else
    echo "unexpected failure on 2xx"
fi
if api_post /refused '{}'; then
    echo "unexpected success on 424"
else
    echo "RC=$?"
fi
`
	path := filepath.Join(t.TempDir(), "api_post_test.sh")
	if err := os.WriteFile(path, []byte(harness), 0o700); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("bash", path).CombinedOutput()
	if err != nil {
		t.Fatalf("run harness: %v\n%s", err, out)
	}
	got := string(out)

	// The success path returns the body and nothing else, so callers
	// can keep piping it into jq.
	if !strings.Contains(got, `OK_BODY={"job_id":"j-1"}`) {
		t.Errorf("2xx body not returned cleanly:\n%s", got)
	}
	// The failure path is the reason this exists: GitHub's sentence
	// has to reach the log.
	if !strings.Contains(got, "Resource not accessible by integration") {
		t.Errorf("error body was swallowed - the operator would see only a status code:\n%s", got)
	}
	if !strings.Contains(got, "HTTP 424") {
		t.Errorf("status code missing from the log line:\n%s", got)
	}
	if !strings.Contains(got, "RC=1") {
		t.Errorf("non-2xx must fail the caller so the ERR trap fires:\n%s", got)
	}
}
