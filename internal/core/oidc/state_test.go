// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package oidc

import (
	"strings"
	"testing"
	"time"
)

var key = []byte("0123456789abcdef0123456789abcdef")

func TestState_RoundTrip(t *testing.T) {
	in := StatePayload{State: "s", Nonce: "n", CodeVerifier: "v"}
	cookie := signState(in, key, time.Now().Add(time.Minute))
	out, err := verifyState(cookie, key)
	if err != nil {
		t.Fatal(err)
	}
	if out != in {
		t.Fatalf("got %+v", out)
	}
}

func TestState_Rejections(t *testing.T) {
	in := StatePayload{State: "s", Nonce: "n", CodeVerifier: "v"}
	good := signState(in, key, time.Now().Add(time.Minute))
	parts := strings.Split(good, ".")
	cases := map[string]string{
		"malformed":     "a.b.c",
		"tampered body": "x." + strings.Join(parts[1:], "."),
		"bad hex sig":   strings.Join(parts[:4], ".") + ".zz",
		"wrong key":     signState(in, []byte("other-key"), time.Now().Add(time.Minute)),
		"expired":       signState(in, key, time.Now().Add(-time.Second)),
	}
	for name, c := range cases {
		if _, err := verifyState(c, key); err == nil {
			t.Errorf("%s: expected rejection", name)
		}
	}
}

func TestPKCEChallenge_IsS256(t *testing.T) {
	// RFC 7636 appendix B test vector.
	got := pkceChallenge("dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk")
	if got != "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM" {
		t.Fatalf("got %s", got)
	}
}

func admitProvider(cfg Config) *Provider { return &Provider{cfg: cfg} }

func TestAdmit(t *testing.T) {
	claims := func(email string, verified bool, groups any) *Claims {
		raw := map[string]any{}
		if groups != nil {
			raw["groups"] = groups
		}
		return &Claims{Subject: "sub", Email: email, EmailVerified: FlexBool(verified), Raw: raw}
	}
	cases := []struct {
		name string
		cfg  Config
		c    *Claims
		ok   bool
	}{
		{"empty allowlists admit", Config{}, claims("a@x.io", false, nil), true},
		{"unverified rejected", Config{RequireEmailVerified: true}, claims("a@x.io", false, nil), false},
		{"verified ok", Config{RequireEmailVerified: true}, claims("a@x.io", true, nil), true},
		{"email listed", Config{AllowedEmails: []string{"a@x.io"}}, claims("A@X.io", true, nil), true},
		{"email not listed", Config{AllowedEmails: []string{"b@x.io"}}, claims("a@x.io", true, nil), false},
		{"email list without claim", Config{AllowedEmails: []string{"b@x.io"}}, claims("", true, nil), false},
		{"email wins over domain", Config{AllowedEmails: []string{"a@x.io"}, AllowedDomains: []string{"other.io"}}, claims("a@x.io", true, nil), true},
		{"domain ok", Config{AllowedDomains: []string{"x.io"}}, claims("a@x.io", true, nil), true},
		{"domain wrong", Config{AllowedDomains: []string{"x.io"}}, claims("a@y.io", true, nil), false},
		{"domain malformed email", Config{AllowedDomains: []string{"x.io"}}, claims("nope", true, nil), false},
		{"group ok case-insensitive", Config{GroupsClaim: "groups", AllowedGroups: []string{"admins"}}, claims("a@x.io", true, []any{"ADMINS"}), true},
		{"group string shape", Config{GroupsClaim: "groups", AllowedGroups: []string{"admins"}}, claims("a@x.io", true, "admins"), true},
		{"group missing", Config{GroupsClaim: "groups", AllowedGroups: []string{"admins"}}, claims("a@x.io", true, nil), false},
		{"group no overlap", Config{GroupsClaim: "groups", AllowedGroups: []string{"admins"}}, claims("a@x.io", true, []string{"devs"}), false},
		{"domain and group both", Config{AllowedDomains: []string{"x.io"}, GroupsClaim: "groups", AllowedGroups: []string{"a"}}, claims("a@x.io", true, []any{"a"}), true},
	}
	for _, tc := range cases {
		err := admitProvider(tc.cfg).Admit(tc.c)
		if (err == nil) != tc.ok {
			t.Errorf("%s: ok=%v err=%v", tc.name, tc.ok, err)
		}
	}
}

func TestExtractGroups_Shapes(t *testing.T) {
	if g := extractGroups(map[string]any{"g": []any{"a", 1, "b"}}, "g"); len(g) != 2 {
		t.Fatalf("mixed slice: %v", g)
	}
	if g := extractGroups(map[string]any{"g": 42}, "g"); g != nil {
		t.Fatalf("unsupported shape: %v", g)
	}
	if g := extractGroups(map[string]any{"g": "x"}, ""); g != nil {
		t.Fatalf("empty claim name: %v", g)
	}
}

func TestIssuerHost(t *testing.T) {
	if h := admitProvider(Config{Issuer: "https://idp.example.com/realms/x"}).IssuerHost(); h != "idp.example.com" {
		t.Fatal(h)
	}
	if h := admitProvider(Config{Issuer: "garbage"}).IssuerHost(); h != "garbage" {
		t.Fatal(h)
	}
}
