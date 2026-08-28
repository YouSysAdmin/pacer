// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// Package tlsutils builds the server's *tls.Config from operator
// config (none / manual / self-signed / ACME).
// Build is the single entry point. Per-mode field validation lives in builder.go so
// non-listening commands skip the cert path checks.
package tlsutils

import (
	"cmp"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"slices"
	"strings"
	"time"

	"golang.org/x/crypto/acme/autocert"
)

var (
	certificateCommonName   = "Self-Signed AWS Github Runner"
	certificateOrganization = []string{"pacer"}
)

type ACME struct {
	Enable   bool
	Email    string
	CacheDir string
	HTTPAddr string
	Hosts    []string
}

type ManualTLS struct {
	CertFile string
	KeyFile  string
}

// AutoTLS start HTTP server for get TLS cert via LetsEncrypt
func AutoTLS(ac ACME) *tls.Config {
	if !ac.Enable {
		return nil
	}
	cache := ac.CacheDir
	cache = cmp.Or(cache, "certs")
	addr := ac.HTTPAddr
	addr = cmp.Or(addr, ":80")
	m := &autocert.Manager{
		Prompt: autocert.AcceptTOS,
		Cache:  autocert.DirCache(cache),
		Email:  ac.Email,
	}
	if len(ac.Hosts) > 0 {
		m.HostPolicy = autocert.HostWhitelist(ac.Hosts...)
	}
	go func() {
		// HTTPHandler(fallback) serves /.well-known/acme-challenge/*
		// from the autocert cache and forwards everything else to
		// fallback. Default behavior (nil) only redirects GET/HEAD.
		// Supplying our own handler 308-redirects every method to
		// HTTPS so a misdirected POST doesn't get a confusing 405.
		// 308 (vs 301) preserves the request method on retry.
		srv := &http.Server{
			Addr:              addr,
			Handler:           m.HTTPHandler(redirectToHTTPS(ac.Hosts)),
			ReadHeaderTimeout: 10 * time.Second,
		}
		slog.Info("ACME HTTP challenge listener", "addr", addr, "hosts", ac.Hosts)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			// Don't kill the whole process -- the operator notices the
			// missing challenge handler when cert issuance fails, and
			// the orchestrator + reaper stay up in the meantime.
			slog.Error("ACME HTTP server failed; cert issuance will not work", "err", err, "addr", addr)
		}
	}()
	return &tls.Config{GetCertificate: m.GetCertificate, MinVersion: tls.VersionTLS12}
}

// redirectToHTTPS returns the fallback handler behind autocert's
// HTTP-01 listener. HostPolicy only gates certificate issuance, not
// this handler, so r.Host is attacker-controlled. A request for a
// host outside the allowlist is redirected to the first configured
// host rather than echoing the header back as an open redirect.
func redirectToHTTPS(hosts []string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		if len(hosts) > 0 && !slices.Contains(hosts, host) {
			host = hosts[0]
		}
		target := "https://" + host + r.URL.RequestURI()
		http.Redirect(w, r, target, http.StatusPermanentRedirect)
	})
}

// LoadManualTLS load user provided TLS certs
func LoadManualTLS(m ManualTLS) (*tls.Config, error) {
	if m.CertFile == "" || m.KeyFile == "" {
		return nil, nil
	}
	pair, err := tls.LoadX509KeyPair(m.CertFile, m.KeyFile)
	if err != nil {
		return nil, err
	}
	return &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12}, nil
}

// SelfSignedTLS generate self-signed TLS certs
func SelfSignedTLS(fqdn, alg string) (*tls.Config, error) {
	switch strings.ToLower(strings.TrimSpace(alg)) {
	case "ed25519":
		return selfSignedEd25519(fqdn)
	default:
		return selfSignedRSA(fqdn)
	}
}

// selfSignedRSA RSA
func selfSignedRSA(fqdn string) (*tls.Config, error) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 62))
	if err != nil {
		return nil, err
	}
	now := time.Now()
	tpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: certificateCommonName, Organization: certificateOrganization},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(180 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		SignatureAlgorithm:    x509.SHA256WithRSA,
		DNSNames:              []string{fqdn, "localhost"},
	}
	certDER, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &priv.PublicKey, priv)
	if err != nil {
		return nil, err
	}
	keyBytes := x509.MarshalPKCS1PrivateKey(priv)
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: keyBytes})
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, err
	}
	return &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12}, nil
}

// selfSignedEd25519 ED25519
func selfSignedEd25519(fqdn string) (*tls.Config, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 62))
	if err != nil {
		return nil, err
	}
	now := time.Now()
	tpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: certificateCommonName, Organization: certificateOrganization},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(180 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{fqdn, "localhost"},
	}
	certDER, err := x509.CreateCertificate(rand.Reader, tpl, tpl, priv.Public(), priv)
	if err != nil {
		return nil, err
	}
	keyBytes, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyBytes})
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, err
	}
	return &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12}, nil
}
