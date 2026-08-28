// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package oidc

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	gooidc "github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// StatePayload carries the state-cookie fields between Authorize +
// Exchange. Random per-request: state guards against CSRF on the
// callback, nonce binds the ID token to this exact request,
// codeVerifier is the PKCE secret the IdP hashes against the
// challenge it received earlier.
type StatePayload struct {
	State        string
	Nonce        string
	CodeVerifier string
}

// Authorize mints fresh state/nonce/verifier and returns the IdP
// redirect URL plus a signed cookie value the caller stores so the
// callback can recover the same payload.
func (p *Provider) Authorize(jwtSecret []byte) (redirectURL, cookieValue string, err error) {
	pay := StatePayload{
		State:        randHex(16),
		Nonce:        randHex(16),
		CodeVerifier: randURLB64(32),
	}
	cookieValue = signState(pay, jwtSecret, time.Now().Add(StateCookieTTL))
	codeChallenge := pkceChallenge(pay.CodeVerifier)
	redirectURL = p.oauth2.AuthCodeURL(pay.State,
		oauth2.SetAuthURLParam("code_challenge", codeChallenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
		gooidc.Nonce(pay.Nonce),
	)
	return redirectURL, cookieValue, nil
}

// Claims is the subset of ID-token + UserInfo claims the callback
// needs to make an admit/deny decision.
type Claims struct {
	Subject       string         `json:"sub"`
	Email         string         `json:"email"`
	EmailVerified FlexBool       `json:"email_verified"`
	Name          string         `json:"name"`
	Raw           map[string]any `json:"-"` // for groups_claim lookup
}

// FlexBool decodes a JSON bool or the strings "true"/"false". Some
// IdPs emit email_verified as a string, which would otherwise fail
// the whole claims decode and lock every user out.
type FlexBool bool

func (b *FlexBool) UnmarshalJSON(data []byte) error {
	switch string(data) {
	case "true", `"true"`:
		*b = true
	case "false", `"false"`, "null":
		*b = false
	default:
		return fmt.Errorf("email_verified: cannot decode %s as bool", data)
	}
	return nil
}

// Exchange verifies the callback URL parameters against the
// previously-set state cookie, exchanges the code, validates the ID
// token (signature + audience + nonce + expiry), and returns the
// resolved claims.
func (p *Provider) Exchange(ctx context.Context, jwtSecret []byte, returnedState, code, cookieValue string) (*Claims, error) {
	pay, err := verifyState(cookieValue, jwtSecret)
	if err != nil {
		return nil, fmt.Errorf("state cookie: %w", err)
	}
	if pay.State == "" || pay.State != returnedState {
		return nil, fmt.Errorf("state mismatch (CSRF guard)")
	}

	tok, err := p.oauth2.Exchange(ctx, code,
		oauth2.SetAuthURLParam("code_verifier", pay.CodeVerifier),
	)
	if err != nil {
		return nil, fmt.Errorf("token exchange: %w", err)
	}
	rawID, ok := tok.Extra("id_token").(string)
	if !ok || rawID == "" {
		return nil, fmt.Errorf("id_token missing from token response")
	}
	idTok, err := p.verifier.Verify(ctx, rawID)
	if err != nil {
		return nil, fmt.Errorf("id_token verify: %w", err)
	}
	if idTok.Nonce != pay.Nonce {
		return nil, fmt.Errorf("nonce mismatch")
	}

	var c Claims
	if err := idTok.Claims(&c); err != nil {
		return nil, fmt.Errorf("decode id_token claims: %w", err)
	}
	if err := idTok.Claims(&c.Raw); err != nil {
		return nil, fmt.Errorf("decode raw claims: %w", err)
	}
	if c.Subject == "" {
		return nil, fmt.Errorf("id_token has no sub claim")
	}
	return &c, nil
}

// signState returns "<state>.<nonce>.<code_verifier>.<exp>.<sig>",
// HMAC-SHA256 over the first four fields with the supplied secret.
func signState(p StatePayload, secret []byte, exp time.Time) string {
	body := strings.Join([]string{
		p.State, p.Nonce, p.CodeVerifier, strconv.FormatInt(exp.Unix(), 10),
	}, ".")
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(body))
	return body + "." + hex.EncodeToString(mac.Sum(nil))
}

func verifyState(cookie string, secret []byte) (StatePayload, error) {
	parts := strings.Split(cookie, ".")
	if len(parts) != 5 {
		return StatePayload{}, fmt.Errorf("malformed cookie")
	}
	body := strings.Join(parts[:4], ".")
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(body))
	want := mac.Sum(nil)
	got, err := hex.DecodeString(parts[4])
	if err != nil {
		slog.Debug("oidc: state cookie hex decode failed", "err", err)
		return StatePayload{}, fmt.Errorf("bad signature")
	}
	if !hmac.Equal(want, got) {
		slog.Debug("oidc: state cookie hmac mismatch")
		return StatePayload{}, fmt.Errorf("bad signature")
	}
	exp, err := strconv.ParseInt(parts[3], 10, 64)
	if err != nil {
		return StatePayload{}, fmt.Errorf("bad expiry")
	}
	if time.Now().Unix() > exp {
		return StatePayload{}, fmt.Errorf("expired (sit too long on IdP page?)")
	}
	return StatePayload{
		State:        parts[0],
		Nonce:        parts[1],
		CodeVerifier: parts[2],
	}, nil
}

func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("oidc: crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(b)
}

func randURLB64(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("oidc: crypto/rand failed: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
