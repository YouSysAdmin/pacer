// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package authenticator

import "testing"

func TestPassword_HashVerify(t *testing.T) {
	h, err := HashPassword("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword(h, "correct horse") {
		t.Fatal("valid password rejected")
	}
	if VerifyPassword(h, "wrong") || VerifyPassword("", "x") || VerifyPassword("not-a-hash", "x") {
		t.Fatal("invalid input accepted")
	}
}

func TestPassword_DummyAlwaysFalse(t *testing.T) {
	if VerifyDummyPassword("anything") || VerifyDummyPassword("") {
		t.Fatal("dummy verify must never succeed")
	}
}

func TestPassword_GenerateUnique(t *testing.T) {
	a, err := GeneratePassword()
	if err != nil || len(a) < 12 {
		t.Fatalf("a=%q err=%v", a, err)
	}
	b, _ := GeneratePassword()
	if a == b {
		t.Fatal("two generated passwords must differ")
	}
}
