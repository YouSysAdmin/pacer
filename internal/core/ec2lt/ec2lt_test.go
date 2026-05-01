// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package ec2lt

import (
	"testing"
)

func TestMergeTags(t *testing.T) {
	t.Run("empty input returns empty map", func(t *testing.T) {
		got := MergeTags()
		if len(got) != 0 {
			t.Fatalf("want empty, got %v", got)
		}
	})

	t.Run("single layer copied", func(t *testing.T) {
		got := MergeTags(map[string]string{"a": "1", "b": "2"})
		if got["a"] != "1" || got["b"] != "2" || len(got) != 2 {
			t.Fatalf("got %v", got)
		}
	})

	t.Run("later layer overrides earlier on key conflict", func(t *testing.T) {
		project := map[string]string{"cost_center": "alpha", "team": "core"}
		pool := map[string]string{"cost_center": "beta", "shape": "large"}
		got := MergeTags(project, pool)

		if got["cost_center"] != "beta" {
			t.Fatalf("pool tag should override project: got %q", got["cost_center"])
		}
		if got["team"] != "core" {
			t.Fatalf("project-only tag missing: got %v", got)
		}
		if got["shape"] != "large" {
			t.Fatalf("pool-only tag missing: got %v", got)
		}
	})

	t.Run("three-layer cascade project->pool->repo", func(t *testing.T) {
		project := map[string]string{"layer": "project", "p_only": "yes"}
		pool := map[string]string{"layer": "pool", "po_only": "yes"}
		repo := map[string]string{"layer": "repo", "r_only": "yes"}
		got := MergeTags(project, pool, repo)

		if got["layer"] != "repo" {
			t.Fatalf("repo (last) layer should win: got %q", got["layer"])
		}
		for _, k := range []string{"p_only", "po_only", "r_only"} {
			if got[k] != "yes" {
				t.Fatalf("non-conflicting key %q dropped: got %v", k, got)
			}
		}
	})

	t.Run("nil layer skipped without panic", func(t *testing.T) {
		got := MergeTags(nil, map[string]string{"a": "1"}, nil)
		if got["a"] != "1" || len(got) != 1 {
			t.Fatalf("got %v", got)
		}
	})

	t.Run("does not mutate inputs", func(t *testing.T) {
		project := map[string]string{"k": "project"}
		pool := map[string]string{"k": "pool"}
		_ = MergeTags(project, pool)

		if project["k"] != "project" {
			t.Fatalf("project map mutated: %v", project)
		}
		if pool["k"] != "pool" {
			t.Fatalf("pool map mutated: %v", pool)
		}
	})
}
