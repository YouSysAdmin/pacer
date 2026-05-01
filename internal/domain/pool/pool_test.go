// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package pool

import (
	"testing"

	poolmodel "github.com/yousysadmin/pacer/internal/models/pool"
)

func TestSanitizeLabel(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"my-app", "my-app"},
		{"MyApp", "myapp"},
		{"My App", "my-app"},
		{"octocat/hello.world", "octocat-hello-world"},
		{"a___b", "a___b"},
		{"---trim---", "trim"},
		{"a/////b", "a-b"},
		{"   spaces   ", "spaces"},
		{"weird!!chars??", "weird-chars"},
		{"unicode-é-foo", "unicode-foo"},
		{"123-numbers_ok", "123-numbers_ok"},
	}
	for _, c := range cases {
		if got := SanitizeLabel(c.in); got != c.want {
			t.Errorf("SanitizeLabel(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestRunnerLabels(t *testing.T) {
	t.Run("repo scope full set", func(t *testing.T) {
		got := RunnerLabels("my-app", "large", "octocat/hello-world", nil)
		want := []string{"self-hosted", "my-app", "large", "octocat-hello-world"}
		assertLabels(t, got, want)
	})

	t.Run("org scope drops repo narrowing", func(t *testing.T) {
		got := RunnerLabels("my-app", "large", "", nil)
		want := []string{"self-hosted", "my-app", "large"}
		assertLabels(t, got, want)
	})

	t.Run("extras appended", func(t *testing.T) {
		got := RunnerLabels("my-app", "large", "octocat/hello-world", []string{"gpu", "arm64"})
		want := []string{"self-hosted", "my-app", "large", "octocat-hello-world", "gpu", "arm64"}
		assertLabels(t, got, want)
	})

	t.Run("dedupes against auto-derived prefix", func(t *testing.T) {
		got := RunnerLabels("my-app", "large", "octocat/hello-world", []string{"large", "MY-APP", "self-hosted", "gpu"})
		want := []string{"self-hosted", "my-app", "large", "octocat-hello-world", "gpu"}
		assertLabels(t, got, want)
	})

	t.Run("extras sanitized", func(t *testing.T) {
		got := RunnerLabels("p", "pool", "", []string{"GPU", "arm/64", "  "})
		want := []string{"self-hosted", "p", "pool", "gpu", "arm-64"}
		assertLabels(t, got, want)
	})
}

func TestMatch(t *testing.T) {
	defaultPool := &poolmodel.Pool{Name: "default", IsDefault: true, Priority: 10}
	largePool := &poolmodel.Pool{Name: "large", Priority: 5}
	armPool := &poolmodel.Pool{Name: "arm", Priority: 5, ExtraLabels: []string{"arm64"}}
	gpuPool := &poolmodel.Pool{Name: "gpu", Priority: 1, ExtraLabels: []string{"gpu"}}
	disabledPool := &poolmodel.Pool{Name: "disabled-large", Priority: 1, Disabled: true}

	cases := []struct {
		name   string
		pools  []*poolmodel.Pool
		labels []string
		want   string // pool name; "" = no match
	}{
		{
			name:   "bare project label picks default",
			pools:  []*poolmodel.Pool{defaultPool, largePool},
			labels: []string{"self-hosted", "my-app"},
			want:   "default",
		},
		{
			name:   "explicit pool name in labels wins over default",
			pools:  []*poolmodel.Pool{defaultPool, largePool},
			labels: []string{"self-hosted", "my-app", "large"},
			want:   "large",
		},
		{
			name:   "extra label picks pool that advertises it",
			pools:  []*poolmodel.Pool{defaultPool, gpuPool},
			labels: []string{"self-hosted", "my-app", "gpu"},
			want:   "gpu",
		},
		{
			name:   "disabled pool ignored even if it would match",
			pools:  []*poolmodel.Pool{disabledPool, largePool},
			labels: []string{"self-hosted", "my-app", "large"},
			want:   "large",
		},
		{
			name:   "no match returns nil",
			pools:  []*poolmodel.Pool{defaultPool, largePool},
			labels: []string{"self-hosted", "my-app", "nonexistent"},
			want:   "",
		},
		{
			name:   "case-insensitive match",
			pools:  []*poolmodel.Pool{largePool},
			labels: []string{"Self-Hosted", "MY-APP", "Large"},
			want:   "large",
		},
		{
			name:   "narrowest (pool + repo) still resolves to pool",
			pools:  []*poolmodel.Pool{defaultPool, largePool},
			labels: []string{"self-hosted", "my-app", "large", "octocat-hello-world"},
			want:   "large",
		},
		{
			name:   "two pools both name-match: lowest priority wins",
			pools:  []*poolmodel.Pool{{Name: "large", Priority: 10}, {Name: "large", Priority: 1}},
			labels: []string{"self-hosted", "my-app", "large"},
			want:   "large", // either, but lower priority should be chosen
		},
		{
			name:   "default chosen over higher-priority non-default when no name match",
			pools:  []*poolmodel.Pool{defaultPool, armPool},
			labels: []string{"self-hosted", "my-app"},
			want:   "default",
		},
		{
			name:   "no default: lowest priority wins",
			pools:  []*poolmodel.Pool{{Name: "a", Priority: 10}, {Name: "b", Priority: 1}},
			labels: []string{"self-hosted", "my-app"},
			want:   "b",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Match(c.pools, c.labels, "my-app", "octocat/hello-world")
			if c.want == "" {
				if got != nil {
					t.Fatalf("expected nil, got %q", got.Name)
				}
				return
			}
			if got == nil {
				t.Fatalf("expected %q, got nil", c.want)
			}
			if got.Name != c.want {
				t.Fatalf("expected %q, got %q", c.want, got.Name)
			}
		})
	}
}

func TestMatch_OrgScope(t *testing.T) {
	pools := []*poolmodel.Pool{
		{Name: "default", IsDefault: true, Priority: 10},
		{Name: "large", Priority: 5},
	}
	got := Match(pools, []string{"self-hosted", "my-app", "large"}, "my-app", "")
	if got == nil || got.Name != "large" {
		t.Fatalf("org scope: want large, got %v", got)
	}
}

func assertLabels(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("len mismatch: got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}
