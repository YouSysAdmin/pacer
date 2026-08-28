// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package pool

import (
	"testing"
	"time"
)

func TestEffectiveMaxRuntime(t *testing.T) {
	cap := time.Duration(MaxRuntimeMinutesCap) * time.Minute
	cases := []struct {
		name string
		p    *Pool
		want time.Duration
	}{
		{"nil pool", nil, cap},
		{"zero", &Pool{MaxRuntimeMinutes: 0}, cap},
		{"negative", &Pool{MaxRuntimeMinutes: -5}, cap},
		{"over cap", &Pool{MaxRuntimeMinutes: MaxRuntimeMinutesCap + 1}, cap},
		{"normal", &Pool{MaxRuntimeMinutes: 90}, 90 * time.Minute},
	}
	for _, c := range cases {
		if got := c.p.EffectiveMaxRuntime(); got != c.want {
			t.Errorf("%s: want %v, got %v", c.name, c.want, got)
		}
	}
}
