// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// Package callback owns the HMAC-signed token used for runner
// self-registration and completion callbacks.
//
// Token format:  <job_id>.<exp_unix>.<hex_sig>
//
//	sig = HMAC-SHA256(key, "<job_id>.<exp_unix>")
//
// The orchestrator mints a token at spawn time, embeds it in
// user-data, and stores its sha256 hash on the job row.
// The runner endpoint:
//
//  1. HMAC-verifies the token against the server-side key
//     (proves the token came from this server, not yet expired)
//  2. Hashes the presented token and compares to the stored hash
//     (binds the token to a single job, so replay across jobs is blocked)
//  3. Checks job status (rejects callbacks after job terminated)
//
// Layers (1) and (2) together mean a leaked token can only be used
// against the job it was minted for, only until expiry, and only
// while the job is still in an active state.
package callback

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Mint signs a token for jobID with the given key + TTL.
// Returns (token, sha256_hex_hash_of_token).
// Store the hash on the job row.
// The token itself goes to the instance via user-data.
func Mint(jobID string, key []byte, ttl time.Duration) (token, hash string) {
	exp := time.Now().Add(ttl).Unix()
	payload := fmt.Sprintf("%s.%d", jobID, exp)
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(payload))
	sig := hex.EncodeToString(mac.Sum(nil))
	token = payload + "." + sig

	h := sha256.Sum256([]byte(token))
	hash = hex.EncodeToString(h[:])
	return token, hash
}

// Verify HMAC-checks the token (signature + expiry).
// Returns the embedded jobID on success.
// Empty jobID means "invalid".
// Callers must additionally compare Hash(token) against the stored hash on
// the job row before trusting it.
//
// HMAC is computed before the expiry check so callers see the same
// response timing for "expired" and "wrong signature" failures.
func Verify(token string, key []byte) (jobID string, ok bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", false
	}
	expUnix, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return "", false
	}

	payload := parts[0] + "." + parts[1]
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(payload))
	want := mac.Sum(nil)

	got, decodeErr := hex.DecodeString(parts[2])
	sigOK := decodeErr == nil && hmac.Equal(want, got)
	notExpired := !time.Unix(expUnix, 0).Before(time.Now())
	if !sigOK || !notExpired {
		return "", false
	}
	return parts[0], true
}

// Hash returns the sha256 hex digest of token, matching what Mint
// returns as its second value.
func Hash(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}
