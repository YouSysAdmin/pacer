// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package auth

import (
	"testing"

	"github.com/yousysadmin/pacer/internal/core/env"
)

func TestSessionTTL_Fallbacks(t *testing.T) {
	rt := &env.Runtime{Config: &env.Config{}}
	if sessionTTL(rt).Hours() != 12 {
		t.Fatal("empty -> 12h")
	}
	rt.Config.Auth.SessionTTL = "garbage"
	if sessionTTL(rt).Hours() != 12 {
		t.Fatal("garbage -> 12h")
	}
	rt.Config.Auth.SessionTTL = "30m"
	if sessionTTL(rt).Minutes() != 30 {
		t.Fatal("30m honored")
	}
}

func TestTruncate(t *testing.T) {
	if truncate("abc", 5) != "abc" || truncate("abcdef", 3) != "abc..." {
		t.Fatal("truncate")
	}
}
