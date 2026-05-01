// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package callback

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"
)

var testKey = []byte("test-callback-secret-32-bytes-min!!")

func TestMintVerifyRoundTrip(t *testing.T) {
	tok, hash := Mint("job-123", testKey, time.Hour)

	if tok == "" || hash == "" {
		t.Fatalf("Mint returned empty: tok=%q hash=%q", tok, hash)
	}
	if Hash(tok) != hash {
		t.Fatalf("Hash(token) != hash returned by Mint")
	}
	if strings.Count(tok, ".") != 2 {
		t.Fatalf("token format wrong (want 2 dots): %q", tok)
	}

	jobID, ok := Verify(tok, testKey)
	if !ok {
		t.Fatalf("Verify failed on freshly minted token")
	}
	if jobID != "job-123" {
		t.Fatalf("jobID = %q, want job-123", jobID)
	}
}

func TestVerify_RejectsExpired(t *testing.T) {
	exp := time.Now().Add(-1 * time.Minute).Unix()
	payload := fmt.Sprintf("job-1.%d", exp)
	tok := payload + "." + signPayload(payload, testKey)

	if _, ok := Verify(tok, testKey); ok {
		t.Fatalf("expired token must not verify")
	}
}

func TestVerify_RejectsBadSignature(t *testing.T) {
	tok, _ := Mint("job-1", testKey, time.Hour)
	if _, ok := Verify(tok, []byte("different-key")); ok {
		t.Fatalf("token must not verify under a different key")
	}
}

func TestVerify_RejectsMalformed(t *testing.T) {
	future := strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10)
	cases := []string{
		"",
		"only-one-segment",
		"two.segments",
		"four.segments.here.bad",
		"job.notanumber.deadbeef",
		"job." + future + ".not-hex-z",
	}
	for _, tok := range cases {
		if _, ok := Verify(tok, testKey); ok {
			t.Errorf("malformed token verified: %q", tok)
		}
	}
}

func TestVerify_RejectsTamperedJobID(t *testing.T) {
	tok, _ := Mint("job-1", testKey, time.Hour)
	parts := strings.Split(tok, ".")
	tampered := "job-2." + parts[1] + "." + parts[2]

	if _, ok := Verify(tampered, testKey); ok {
		t.Fatalf("tampered jobID must not verify")
	}
}

func TestHash_DeterministicAndDistinct(t *testing.T) {
	tok1, _ := Mint("job-1", testKey, time.Hour)
	tok2, _ := Mint("job-2", testKey, time.Hour)

	if Hash(tok1) != Hash(tok1) {
		t.Fatalf("Hash should be deterministic")
	}
	if Hash(tok1) == Hash(tok2) {
		t.Fatalf("different tokens must hash differently")
	}
}

func signPayload(payload string, key []byte) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}
