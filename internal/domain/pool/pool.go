// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// Package pool owns the pool domain - both the HTTP API and the
// SQLite-backed persistence. A pool is a named EC2 launch shape (AMI
// / instance types / subnets / SGs / IAM profile / etc.) inside a
// project. Each pool materializes one EC2 launch template; the
// project picks a pool per job by matching workflow_job.labels[]
// against the pool's generated label set.
package pool

import (
	"sort"
	"strings"

	poolmodel "github.com/yousysadmin/pacer/internal/models/pool"
)

// RunnerLabels builds the runner labels for a JIT spawn from this
// pool: [self-hosted, <project>, <pool>, <owner>-<repo>] + any
// operator-supplied extras.
// Each label is sanitized to GitHub's charset, matching what Match does on workflow labels.
// Duplicates (e.g. an extra that collides with the pool name) are collapsed.
func RunnerLabels(projectName, poolName, repoFullName string, extras []string) []string {
	out := []string{"self-hosted"}
	seen := map[string]bool{"self-hosted": true}
	add := func(s string) {
		if s = SanitizeLabel(s); s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	add(projectName)
	add(poolName)
	add(repoFullName)
	for _, e := range extras {
		add(e)
	}
	return out
}

// Match picks the pool whose runner labels best satisfy the workflow
// runs-on set:
//  1. enabled pools whose label set is a superset of workflow labels
//  2. if any match's name appears in workflow labels, lowest-priority such pool wins
//  3. else the project's default pool (if among matches)
//  4. else the lowest-priority match
//  5. no match -> nil; caller drops the job
//
// All comparisons are case-insensitive via SanitizeLabel.
func Match(pools []*poolmodel.Pool, workflowLabels []string, projectName, repoFullName string) *poolmodel.Pool {
	want := normalizeSet(workflowLabels)

	var matches []*poolmodel.Pool
	for _, p := range pools {
		if p.Disabled {
			continue
		}
		have := normalizeSet(RunnerLabels(projectName, p.Name, repoFullName, p.ExtraLabels))
		if isSuperset(have, want) {
			matches = append(matches, p)
		}
	}
	if len(matches) == 0 {
		return nil
	}

	var explicit []*poolmodel.Pool
	for _, p := range matches {
		if want[SanitizeLabel(p.Name)] {
			explicit = append(explicit, p)
		}
	}
	if len(explicit) > 0 {
		sort.SliceStable(explicit, func(i, j int) bool {
			return explicit[i].Priority < explicit[j].Priority
		})
		return explicit[0]
	}

	for _, p := range matches {
		if p.IsDefault {
			return p
		}
	}

	sort.SliceStable(matches, func(i, j int) bool {
		return matches[i].Priority < matches[j].Priority
	})
	return matches[0]
}

// SanitizeLabel lowercases and replaces every rune outside [a-z0-9_]
// with '-', collapsing runs and trimming trailing dashes.
// "octocat/hello.world" -> "octocat-hello-world".
func SanitizeLabel(s string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_':
			b.WriteRune(r)
			lastDash = false
		default:
			if b.Len() > 0 && !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.TrimRight(b.String(), "-")
}

func normalizeSet(labels []string) map[string]bool {
	out := make(map[string]bool, len(labels))
	for _, l := range labels {
		if s := SanitizeLabel(l); s != "" {
			out[s] = true
		}
	}
	return out
}

func isSuperset(have, want map[string]bool) bool {
	for k := range want {
		if !have[k] {
			return false
		}
	}
	return true
}
