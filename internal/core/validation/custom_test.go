// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package validation

import (
	"encoding/json"
	"testing"
)

type repoDTO struct {
	FullName string            `json:"full_name" validate:"repo_full_name"`
	Tags     map[string]string `json:"tags" validate:"dive,keys,gha_safe,endkeys"`
}

func TestCustomValidators(t *testing.T) {
	v := V()
	ok := []repoDTO{
		{FullName: "octocat/hello-world"},
		{FullName: "o/r", Tags: map[string]string{"team": "a"}},
	}
	for _, d := range ok {
		if err := v.Struct(d); err != nil {
			t.Errorf("%+v: unexpected error %v", d, err)
		}
	}
	bad := []repoDTO{
		{FullName: "octocat"},
		{FullName: "octocat/"},
		{FullName: "/hello"},
		{FullName: "a/b/c"},
		{FullName: "octocat/ hello"},
		{FullName: "o/r", Tags: map[string]string{"gha:job_id": "x"}},
		{FullName: "o/r", Tags: map[string]string{" gha:job_id": "x"}},
		{FullName: "o/r", Tags: map[string]string{"GHA:x": "x"}},
	}
	for _, d := range bad {
		if err := v.Struct(d); err == nil {
			t.Errorf("%+v: expected validation error", d)
		}
	}
}

func TestHumanize_DecodeErrors(t *testing.T) {
	type in struct {
		Max int `json:"max_concurrent_runners"`
	}
	var x in
	err := json.Unmarshal([]byte(`{"max_concurrent_runners":"five"}`), &x)
	fes := Humanize(err)
	if len(fes) != 1 || fes[0].Field != "max_concurrent_runners" || fes[0].Rule != "type" {
		t.Fatalf("unexpected %+v", fes)
	}
	if fes[0].Message != "Max concurrent runners has the wrong type" {
		t.Errorf("message leaks internals: %q", fes[0].Message)
	}
	fes = Humanize(json.Unmarshal([]byte(`{`), &x))
	if len(fes) != 1 || fes[0].Rule != "syntax" {
		t.Fatalf("unexpected %+v", fes)
	}
}
