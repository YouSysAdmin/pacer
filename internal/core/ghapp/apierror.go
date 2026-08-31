// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package ghapp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// bodyExcerptMax caps how much of GitHub's response body travels with
// the error. GitHub's messages are one or two sentences; anything
// longer is an HTML error page from a proxy, and the whole point is
// that this string ends up in a job's failure_log where the operator
// reads it.
const bodyExcerptMax = 512

// APIError is a non-2xx answer from the GitHub API.
//
// Typed rather than fmt.Errorf, because the caller has to make a
// DECISION with it and a string cannot be asked questions. A runner
// bootstrap that hits "GitHub is having a bad minute" should retry;
// one that hits "this App no longer has access to the repo" should
// fail immediately and say so, rather than burning 72 seconds of
// billed instance time re-asking a question already answered.
type APIError struct {
	// Op is the call that failed, e.g. "jitconfig" - what an
	// operator needs to know before the status code means anything.
	Op         string
	StatusCode int
	Status     string
	// Body is GitHub's own explanation, truncated. This is the part
	// worth carrying: "Bad credentials" and "Resource not accessible
	// by integration" are different problems with the same 403.
	Body string
}

func (e *APIError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("%s: %s", e.Op, e.Status)
	}
	return fmt.Sprintf("%s: %s: %s", e.Op, e.Status, e.Body)
}

// Temporary reports whether retrying the same call could plausibly
// succeed without anybody changing anything.
//
// 5xx is GitHub failing, 429 is us going too fast, and 408 is a
// timeout - all worth another attempt. Everything else (401, 403,
// 404, 422) describes a configuration or permission state that will
// answer identically until a human fixes it.
func (e *APIError) Temporary() bool {
	return e.StatusCode == http.StatusTooManyRequests ||
		e.StatusCode == http.StatusRequestTimeout ||
		e.StatusCode >= 500
}

// newAPIError builds an APIError from a response body already read.
func newAPIError(op string, statusCode int, status string, body []byte) *APIError {
	return &APIError{
		Op:         op,
		StatusCode: statusCode,
		Status:     status,
		Body:       excerpt(body),
	}
}

// excerpt reduces a body to the one readable line worth logging.
//
// GitHub's error schema is {"message": ..., "documentation_url": ...,
// "errors": [...]} on every endpoint, and it is the "message" that
// says what went wrong. Dumping the raw JSON instead means the
// operator reads their own envelope's escaping - the useful sentence
// arrives as \"Resource not accessible by integration\" wrapped in a
// documentation_url nobody needs mid-incident.
//
// Anything that is not that shape (an HTML page from a proxy, a plain
// string) falls through to whitespace-normalized text, so a body is
// never dropped just because it surprised us.
func excerpt(body []byte) string {
	if msg := githubMessage(body); msg != "" {
		return truncate(msg)
	}
	s := strings.Join(strings.Fields(string(body)), " ")
	return truncate(s)
}

// githubMessage pulls the human sentence out of GitHub's error
// envelope, appending the per-field details it sometimes carries
// (a 422 explains itself in "errors", not in "message").
func githubMessage(body []byte) string {
	var env struct {
		Message string `json:"message"`
		Errors  []struct {
			Resource string `json:"resource"`
			Field    string `json:"field"`
			Code     string `json:"code"`
			Message  string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(body, &env); err != nil || env.Message == "" {
		return ""
	}
	parts := []string{env.Message}
	for _, e := range env.Errors {
		switch {
		case e.Message != "":
			parts = append(parts, e.Message)
		case e.Field != "":
			parts = append(parts, strings.TrimSpace(e.Resource+"."+e.Field+" "+e.Code))
		}
	}
	return strings.Join(parts, "; ")
}

func truncate(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > bodyExcerptMax {
		return s[:bodyExcerptMax] + "..."
	}
	return s
}
