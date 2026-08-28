// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package authenticator

import (
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v4"
)

const secret = "0123456789abcdef0123456789abcdef"

func TestToken_RoundTrip(t *testing.T) {
	raw, err := CreateToken(secret, "u1", "a@example.com", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	c, err := ParseToken(secret, raw)
	if err != nil {
		t.Fatal(err)
	}
	if c.UserID != "u1" || c.Email != "a@example.com" || time.Until(c.Expiry) < 50*time.Minute {
		t.Fatalf("claims %+v", c)
	}
}

func forge(t *testing.T, method jwt.SigningMethod, key string, claims jwt.MapClaims) string {
	t.Helper()
	s, err := jwt.NewWithClaims(method, claims).SignedString([]byte(key))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestToken_Rejections(t *testing.T) {
	base := func() jwt.MapClaims {
		now := time.Now()
		return jwt.MapClaims{"user_id": "u1", "email": "a@x", "iss": jwtIssuer, "aud": jwtAudience,
			"iat": now.Unix(), "nbf": now.Unix(), "exp": now.Add(time.Hour).Unix()}
	}
	good, _ := CreateToken(secret, "u1", "a@x", time.Hour)
	cases := map[string]string{
		"empty":         "",
		"garbage":       "not.a.jwt",
		"wrong secret":  forge(t, jwt.SigningMethodHS256, "other-secret-other-secret-other-!", base()),
		"hs512 variant": forge(t, jwt.SigningMethodHS512, secret, base()),
		"tampered":      good[:len(good)-2] + "AA",
	}
	c := base()
	c["exp"] = time.Now().Add(-time.Minute).Unix()
	cases["expired"] = forge(t, jwt.SigningMethodHS256, secret, c)
	c = base()
	c["iss"] = "other"
	cases["wrong issuer"] = forge(t, jwt.SigningMethodHS256, secret, c)
	c = base()
	c["aud"] = "other"
	cases["wrong audience"] = forge(t, jwt.SigningMethodHS256, secret, c)
	c = base()
	delete(c, "user_id")
	cases["no user_id"] = forge(t, jwt.SigningMethodHS256, secret, c)

	for name, raw := range cases {
		if _, err := ParseToken(secret, raw); err == nil {
			t.Errorf("%s: expected rejection", name)
		}
	}
	if _, err := ParseToken("", good); err == nil {
		t.Error("empty secret must be rejected")
	}
	if _, err := CreateToken("", "u", "e", time.Hour); err == nil {
		t.Error("CreateToken with empty secret must fail")
	}
}

func TestToken_AlgNoneRejected(t *testing.T) {
	// header {"alg":"none"} with a valid-looking payload and empty sig.
	none := "eyJhbGciOiJub25lIiwidHlwIjoiSldUIn0.eyJ1c2VyX2lkIjoidTEiLCJpc3MiOiJwYWNlciIsImF1ZCI6InBhY2VyLXNlc3Npb24iLCJleHAiOjk5OTk5OTk5OTl9."
	if _, err := ParseToken(secret, none); err == nil || !strings.Contains(err.Error(), "signing method") && !strings.Contains(err.Error(), "none") {
		t.Fatalf("alg=none must be rejected, got %v", err)
	}
}
