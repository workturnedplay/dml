// Copyright 2026 workturnedplay
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// SPDX-License-Identifier: Apache-2.0

package dml

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The tests in this file need no sockets: the files live in a temporary
// directory and the handshake runs over net.Pipe.

func mustNewCA(t *testing.T, validity time.Duration) *CertificateAuthority {
	t.Helper()

	authority, err := NewCertificateAuthority("dml test CA", validity)
	if err != nil {
		t.Fatalf("NewCertificateAuthority(): %v", err)
	}

	return authority
}

func mustIssue(t *testing.T, authority *CertificateAuthority, spec CertificateSpec) *IssuedCertificate {
	t.Helper()

	issued, err := authority.Issue(spec)
	if err != nil {
		t.Fatalf("Issue(%+v): %v", spec, err)
	}

	return issued
}

func mustKeyPEM(t *testing.T, authority *CertificateAuthority) []byte {
	t.Helper()

	keyPEM, err := authority.KeyPEM()
	if err != nil {
		t.Fatalf("KeyPEM(): %v", err)
	}

	return keyPEM
}

func writeTestFile(t *testing.T, dir, name string, data []byte) string {
	t.Helper()

	path := filepath.Join(dir, name)

	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}

	return path
}

func serverTestSpec() CertificateSpec {
	return CertificateSpec{CommonName: "srv", Names: []string{"localhost", "127.0.0.1"}, Server: true, Validity: time.Hour}
}

func clientTestSpec() CertificateSpec {
	return CertificateSpec{CommonName: "cli", Client: true, Validity: time.Hour}
}

func TestCertificateAuthorityIssuesCertificatesWithTheRequestedUses(t *testing.T) {
	authority := mustNewCA(t, 24*time.Hour)
	server := mustIssue(t, authority, serverTestSpec())
	client := mustIssue(t, authority, clientTestSpec())

	verifies := func(leaf *x509.Certificate, host string, usage x509.ExtKeyUsage) bool {
		_, verifyErr := leaf.Verify(x509.VerifyOptions{
			Roots:     authority.Pool(),
			DNSName:   host,
			KeyUsages: []x509.ExtKeyUsage{usage},
		})

		return verifyErr == nil
	}

	checks := []struct {
		name string
		leaf *x509.Certificate
		host string
		use  x509.ExtKeyUsage
		want bool
	}{
		{name: "server for its DNS name", leaf: server.Certificate, host: "localhost", use: x509.ExtKeyUsageServerAuth, want: true},
		{name: "server for its IP address", leaf: server.Certificate, host: "127.0.0.1", use: x509.ExtKeyUsageServerAuth, want: true},
		{name: "server for another name", leaf: server.Certificate, host: "other.example", use: x509.ExtKeyUsageServerAuth, want: false},
		{name: "server certificate as a client", leaf: server.Certificate, host: "localhost", use: x509.ExtKeyUsageClientAuth, want: false},
		{name: "client as a client", leaf: client.Certificate, host: "", use: x509.ExtKeyUsageClientAuth, want: true},
		{name: "client certificate as a server", leaf: client.Certificate, host: "", use: x509.ExtKeyUsageServerAuth, want: false},
	}

	for _, check := range checks {
		if got := verifies(check.leaf, check.host, check.use); got != check.want {
			t.Fatalf("%s: verifies = %v, want %v", check.name, got, check.want)
		}
	}

	if got, want := client.Principal(), TLSCertificatePrincipal(client.Certificate); got != want {
		t.Fatalf("Principal() = %q, want %q", got, want)
	}
}

func TestCertificateAuthorityRejectsRequestsItCannotIssue(t *testing.T) {
	authority := mustNewCA(t, 24*time.Hour)

	specs := []struct {
		name string
		spec CertificateSpec
	}{
		{name: "no common name", spec: CertificateSpec{Names: []string{"localhost"}, Server: true, Validity: time.Hour}},
		{name: "no use", spec: CertificateSpec{CommonName: "x", Names: []string{"localhost"}, Validity: time.Hour}},
		{name: "server without names", spec: CertificateSpec{CommonName: "x", Server: true, Validity: time.Hour}},
		{name: "empty name", spec: CertificateSpec{CommonName: "x", Names: []string{""}, Server: true, Validity: time.Hour}},
		{name: "zero validity", spec: CertificateSpec{CommonName: "x", Client: true}},
		{name: "negative validity", spec: CertificateSpec{CommonName: "x", Client: true, Validity: -time.Hour}},
		{name: "outlives the CA", spec: CertificateSpec{CommonName: "x", Client: true, Validity: 48 * time.Hour}},
	}

	for _, tc := range specs {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := authority.Issue(tc.spec); !errors.Is(err, ErrCertificateSpec) {
				t.Fatalf("Issue() error = %v, want %v", err, ErrCertificateSpec)
			}
		})
	}

	if _, err := NewCertificateAuthority("", time.Hour); !errors.Is(err, ErrCertificateSpec) {
		t.Fatalf("NewCertificateAuthority(\"\") error = %v, want %v", err, ErrCertificateSpec)
	}

	if _, err := NewCertificateAuthority("x", 0); !errors.Is(err, ErrCertificateSpec) {
		t.Fatalf("NewCertificateAuthority(zero validity) error = %v, want %v", err, ErrCertificateSpec)
	}
}

func TestParseCertificateAuthorityRoundTripsAndRejectsWrongInput(t *testing.T) {
	authority := mustNewCA(t, time.Hour)
	keyPEM := mustKeyPEM(t, authority)

	restored, parseErr := ParseCertificateAuthority(authority.CertPEM(), keyPEM)
	if parseErr != nil {
		t.Fatalf("ParseCertificateAuthority(): %v", parseErr)
	}

	// A certificate the restored CA issues is trusted by the original one.
	issued := mustIssue(t, restored, clientTestSpec())

	_, verifyErr := issued.Certificate.Verify(x509.VerifyOptions{
		Roots:     authority.Pool(),
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	})
	if verifyErr != nil {
		t.Fatalf("a certificate issued by the restored CA does not verify against the original: %v", verifyErr)
	}

	other := mustNewCA(t, time.Hour)

	cases := []struct {
		name    string
		certPEM []byte
		keyPEM  []byte
		want    error
	}{
		{name: "key of another CA", certPEM: authority.CertPEM(), keyPEM: mustKeyPEM(t, other), want: ErrCAKeyMismatch},
		{name: "a leaf certificate", certPEM: issued.CertPEM, keyPEM: issued.KeyPEM, want: ErrNotCA},
		{name: "not PEM", certPEM: []byte("not pem"), keyPEM: keyPEM, want: ErrInvalidPEM},
		{name: "key given as the certificate", certPEM: keyPEM, keyPEM: keyPEM, want: ErrInvalidPEM},
		{name: "certificate given as the key", certPEM: authority.CertPEM(), keyPEM: authority.CertPEM(), want: ErrInvalidPEM},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseCertificateAuthority(tc.certPEM, tc.keyPEM); !errors.Is(err, tc.want) {
				t.Fatalf("ParseCertificateAuthority() error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestLoadCertificateAuthorityReadsFilesAndNamesMissingOnes(t *testing.T) {
	dir := t.TempDir()
	authority := mustNewCA(t, time.Hour)

	certFile := writeTestFile(t, dir, "ca.pem", authority.CertPEM())
	keyFile := writeTestFile(t, dir, "ca-key.pem", mustKeyPEM(t, authority))

	if _, err := LoadCertificateAuthority(certFile, keyFile); err != nil {
		t.Fatalf("LoadCertificateAuthority(): %v", err)
	}

	missing := filepath.Join(dir, "missing.pem")

	if _, err := LoadCertificateAuthority(missing, keyFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("LoadCertificateAuthority(missing certificate) error = %v, want %v", err, os.ErrNotExist)
	}

	if _, err := LoadCertificateAuthority(certFile, missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("LoadCertificateAuthority(missing key) error = %v, want %v", err, os.ErrNotExist)
	}
}

func TestLoadTLSConfigsEnforceMutualAuthenticationAndHandshake(t *testing.T) {
	dir := t.TempDir()
	authority := mustNewCA(t, time.Hour)
	server := mustIssue(t, authority, serverTestSpec())
	client := mustIssue(t, authority, clientTestSpec())

	caFile := writeTestFile(t, dir, "ca.pem", authority.CertPEM())
	serverCertFile := writeTestFile(t, dir, "srv.pem", server.CertPEM)
	serverKeyFile := writeTestFile(t, dir, "srv-key.pem", server.KeyPEM)
	clientCertFile := writeTestFile(t, dir, "cli.pem", client.CertPEM)
	clientKeyFile := writeTestFile(t, dir, "cli-key.pem", client.KeyPEM)

	serverCfg, serverErr := LoadServerTLS(serverCertFile, serverKeyFile, caFile)
	if serverErr != nil {
		t.Fatalf("LoadServerTLS(): %v", serverErr)
	}

	if serverCfg.ClientAuth != tls.RequireAndVerifyClientCert || serverCfg.ClientCAs == nil ||
		serverCfg.MinVersion != tls.VersionTLS13 || len(serverCfg.Certificates) != 1 {
		t.Fatalf("LoadServerTLS() = %+v, want mutual TLS 1.3 with one certificate", serverCfg)
	}

	// What ListenTLS requires of a configuration.
	if _, validateErr := tlsServerConfig(serverCfg); validateErr != nil {
		t.Fatalf("tlsServerConfig(LoadServerTLS()): %v", validateErr)
	}

	clientCfg, clientErr := LoadClientTLS(clientCertFile, clientKeyFile, caFile, "localhost")
	if clientErr != nil {
		t.Fatalf("LoadClientTLS(): %v", clientErr)
	}

	if clientCfg.ServerName != "localhost" || clientCfg.RootCAs == nil ||
		clientCfg.MinVersion != tls.VersionTLS13 || len(clientCfg.Certificates) != 1 {
		t.Fatalf("LoadClientTLS() = %+v, want TLS 1.3 verifying localhost with one certificate", clientCfg)
	}

	// Session tickets would be written after the handshake and block the
	// unbuffered net.Pipe (see tlsTestPKI.serverConfig).
	serverCfg.SessionTicketsDisabled = true

	serverConn := tlsHandshakePair(t, serverCfg, clientCfg)

	got, principalErr := TLSPeerPrincipal(serverConn)
	if principalErr != nil {
		t.Fatalf("TLSPeerPrincipal(): %v", principalErr)
	}

	want, fileErr := CertificateFilePrincipal(clientCertFile)
	if fileErr != nil {
		t.Fatalf("CertificateFilePrincipal(): %v", fileErr)
	}

	if got != want {
		t.Fatalf("TLSPeerPrincipal() = %q, want the principal of the client certificate file, %q", got, want)
	}
}

func TestLoadTLSFailsLoudlyOnBadFiles(t *testing.T) {
	dir := t.TempDir()
	authority := mustNewCA(t, time.Hour)
	server := mustIssue(t, authority, serverTestSpec())
	other := mustIssue(t, authority, serverTestSpec())

	caFile := writeTestFile(t, dir, "ca.pem", authority.CertPEM())
	certFile := writeTestFile(t, dir, "srv.pem", server.CertPEM)
	keyFile := writeTestFile(t, dir, "srv-key.pem", server.KeyPEM)
	otherKeyFile := writeTestFile(t, dir, "other-key.pem", other.KeyPEM)
	notACAFile := writeTestFile(t, dir, "empty.pem", []byte("nothing here"))
	missing := filepath.Join(dir, "missing.pem")

	if _, loadErr := LoadServerTLS(missing, keyFile, caFile); !errors.Is(loadErr, os.ErrNotExist) {
		t.Fatalf("LoadServerTLS(missing certificate) error = %v, want %v", loadErr, os.ErrNotExist)
	}

	if _, loadErr := LoadServerTLS(certFile, otherKeyFile, caFile); loadErr == nil {
		t.Fatal("LoadServerTLS() accepted a key that does not belong to the certificate")
	}

	if _, loadErr := LoadServerTLS(certFile, keyFile, notACAFile); !errors.Is(loadErr, ErrNoCertificates) {
		t.Fatalf("LoadServerTLS(CA file without certificates) error = %v, want %v", loadErr, ErrNoCertificates)
	}

	if _, loadErr := LoadServerTLS(certFile, keyFile, missing); !errors.Is(loadErr, os.ErrNotExist) {
		t.Fatalf("LoadServerTLS(missing CA file) error = %v, want %v", loadErr, os.ErrNotExist)
	}

	if _, loadErr := LoadClientTLS(certFile, keyFile, missing, ""); !errors.Is(loadErr, os.ErrNotExist) {
		t.Fatalf("LoadClientTLS(missing CA file) error = %v, want %v", loadErr, os.ErrNotExist)
	}

	if _, loadErr := LoadClientTLS(certFile, otherKeyFile, caFile, ""); loadErr == nil {
		t.Fatal("LoadClientTLS() accepted a key that does not belong to the certificate")
	}
}

func TestCertificateFilePrincipal(t *testing.T) {
	dir := t.TempDir()
	authority := mustNewCA(t, time.Hour)
	client := mustIssue(t, authority, clientTestSpec())

	certFile := writeTestFile(t, dir, "cli.pem", client.CertPEM)

	got, err := CertificateFilePrincipal(certFile)
	if err != nil {
		t.Fatalf("CertificateFilePrincipal(): %v", err)
	}

	if want := TLSCertificatePrincipal(client.Certificate); got != want {
		t.Fatalf("CertificateFilePrincipal() = %q, want %q", got, want)
	}

	garbage := writeTestFile(t, dir, "garbage.pem", []byte("not pem"))

	if _, principalErr := CertificateFilePrincipal(garbage); !errors.Is(principalErr, ErrInvalidPEM) {
		t.Fatalf("CertificateFilePrincipal(garbage) error = %v, want %v", principalErr, ErrInvalidPEM)
	}

	if _, principalErr := CertificateFilePrincipal(filepath.Join(dir, "missing.pem")); !errors.Is(principalErr, os.ErrNotExist) {
		t.Fatalf("CertificateFilePrincipal(missing) error = %v, want %v", principalErr, os.ErrNotExist)
	}
}