// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// Package health holds the in-memory health bus that background
// workers (reaper, preflight) write to and the /api/health endpoint
// reads from. It exists so a silent failure -- a panicked goroutine,
// a missing IAM permission, a DescribeInstances call that 401s --
// surfaces as a banner in the UI instead of buried in server logs.
package health

import (
	"sort"
	"sync"
	"time"
)

// Issue describes one ongoing problem from a named component.
// Time is when the issue was first set (or last re-set with a
// different message); a clean tick clears the entry.
type Issue struct {
	Component string    `json:"component"`
	Message   string    `json:"message"`
	Since     time.Time `json:"since"`
}

type Health struct {
	mu     sync.RWMutex
	issues map[string]Issue
}

func New() *Health {
	return &Health{issues: map[string]Issue{}}
}

// Set records that component is unhealthy with msg. Repeated Set with
// the same (component, msg) is a no-op on Since so a sustained
// failure keeps its original first-seen timestamp; changing the
// message resets Since.
func (h *Health) Set(component, msg string) {
	if component == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if prev, ok := h.issues[component]; ok && prev.Message == msg {
		return
	}
	h.issues[component] = Issue{
		Component: component,
		Message:   msg,
		Since:     time.Now().UTC(),
	}
}

// Clear drops the issue for component if any. Safe to call when no
// issue is present.
func (h *Health) Clear(component string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.issues, component)
}

// Snapshot returns the current issues in stable order (sorted by
// component name) for deterministic UI rendering.
func (h *Health) Snapshot() []Issue {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]Issue, 0, len(h.issues))
	for _, i := range h.issues {
		out = append(out, i)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Component < out[j].Component
	})
	return out
}

// Get returns the message for component and whether one is set. Used
// by callers (the reconcile endpoint) that want to surface only that
// component's verdict.
func (h *Health) Get(component string) (string, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	i, ok := h.issues[component]
	if !ok {
		return "", false
	}
	return i.Message, true
}
