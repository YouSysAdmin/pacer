// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package tlsutils

import (
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestBuild_NoneReturnsNil(t *testing.T) {
	for _, mode := range []string{"", ModeNone} {
		cfg, err := Build(Config{Mode: mode})
		if cfg != nil || err != nil {
			t.Fatalf("mode %q: %v %v", mode, cfg, err)
		}
		if (Config{Mode: mode}).Enabled() {
			t.Fatalf("mode %q must not be enabled", mode)
		}
	}
}

func TestBuild_Rejections(t *testing.T) {
	cases := map[string]Config{
		"unknown mode":        {Mode: "letsencrypt"},
		"manual without key":  {Mode: ModeManual, Cert: "/x.pem"},
		"manual without cert": {Mode: ModeManual, Key: "/x.pem"},
		"manual missing file": {Mode: ModeManual, Cert: "/nope/c.pem", Key: "/nope/k.pem"},
		"self without fqdn":   {Mode: ModeSelf},
		"acme without hosts":  {Mode: ModeACME},
	}
	for name, c := range cases {
		if _, err := Build(c); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	_, err := Build(Config{Mode: "bogus"})
	if err == nil || !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("unknown mode error should name the mode: %v", err)
	}
}

func TestBuild_SelfSigned(t *testing.T) {
	for _, alg := range []string{"", "rsa", "ED25519"} {
		cfg, err := Build(Config{Mode: ModeSelf, FQDN: "pacer.example.com", Alg: alg})
		if err != nil || cfg == nil || len(cfg.Certificates) != 1 {
			t.Fatalf("alg %q: %v %v", alg, cfg, err)
		}
		if cfg.MinVersion < tls.VersionTLS12 {
			t.Fatalf("alg %q: weak MinVersion", alg)
		}
		leaf, err := x509.ParseCertificate(cfg.Certificates[0].Certificate[0])
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(leaf.DNSNames, "pacer.example.com") {
			t.Fatalf("alg %q: fqdn missing from SANs %v", alg, leaf.DNSNames)
		}
	}
}

func pemCert(t *testing.T, cfg *tls.Config) []byte {
	t.Helper()
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cfg.Certificates[0].Certificate[0]})
}

func pemKey(t *testing.T, cfg *tls.Config) []byte {
	t.Helper()
	priv, ok := cfg.Certificates[0].PrivateKey.(*rsa.PrivateKey)
	if !ok {
		t.Fatal("expected rsa key")
	}
	return pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)})
}

func TestBuild_ManualLoadsGeneratedPair(t *testing.T) {
	// Reuse the self-signed generator to produce a real pair on disk.
	self, err := selfSignedRSA("manual.example.com")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certPath, keyPath := filepath.Join(dir, "c.pem"), filepath.Join(dir, "k.pem")
	if err := os.WriteFile(certPath, pemCert(t, self), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pemKey(t, self), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Build(Config{Mode: ModeManual, Cert: certPath, Key: keyPath})
	if err != nil || cfg == nil || len(cfg.Certificates) != 1 {
		t.Fatalf("%v %v", cfg, err)
	}
}
