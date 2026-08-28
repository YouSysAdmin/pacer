// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package oidc

import (
	"encoding/json"
	"testing"
)

func TestFlexBool_Decode(t *testing.T) {
	for in, want := range map[string]bool{
		`{"email_verified":true}`:    true,
		`{"email_verified":"true"}`:  true,
		`{"email_verified":false}`:   false,
		`{"email_verified":"false"}`: false,
		`{"email_verified":null}`:    false,
		`{}`:                         false,
	} {
		var c Claims
		if err := json.Unmarshal([]byte(in), &c); err != nil {
			t.Errorf("%s: %v", in, err)
			continue
		}
		if bool(c.EmailVerified) != want {
			t.Errorf("%s: want %v", in, want)
		}
	}
	var c Claims
	if err := json.Unmarshal([]byte(`{"email_verified":"yes"}`), &c); err == nil {
		t.Error("garbage string must fail")
	}
}
