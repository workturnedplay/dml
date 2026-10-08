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

// leafTestValidity is the validity of the certificates these tests issue. It
// must be clearly shorter than the validity of the CA they use (an hour here):
// a leaf may not outlive its CA, and a leaf issued a moment after the CA
// already would if the two validities were equal.
const leafTestValidity = 30 * time.Minute

func serverTestSpec() CertificateSpec {
	return CertificateSpec{CommonName: "srv", Names: []string{"localhost", "127.0.0.1"}, Server: true, Validity: leafTestValidity}
}

func clientTestSpec() CertificateSpec {
	return CertificateSpec{CommonName: "cli", Client: true, Validity: leafTestValidity}
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

// TestCertificateAuthorityRefusesALeafThatWouldOutliveIt pins down the rule
// the first version of these tests tripped over: the CA's validity counts from
// when the CA was made, so a leaf issued later with the same validity ends
// after the CA does and is refused, while a shorter one is fine.
func TestCertificateAuthorityRefusesALeafThatWouldOutliveIt(t *testing.T) {
	authority := mustNewCA(t, time.Hour)

	equal := clientTestSpec()
	equal.Validity = time.Hour

	if _, err := authority.Issue(equal); !errors.Is(err, ErrCertificateSpec) {
		t.Fatalf("Issue() with the CA's own validity error = %v, want %v", err, ErrCertificateSpec)
	}

	shorter := clientTestSpec()
	shorter.Validity = 59 * time.Minute

	issued := mustIssue(t, authority, shorter)

	if issued.Certificate.NotAfter.After(authority.Certificate().NotAfter) {
		t.Fatalf("the leaf expires at %v, after its CA at %v", issued.Certificate.NotAfter, authority.Certificate().NotAfter)
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

// mustKeyPair loads an issued certificate and its key as a tls.Certificate.
func mustKeyPair(t *testing.T, issued *IssuedCertificate) tls.Certificate {
	t.Helper()

	cert, err := tls.X509KeyPair(issued.CertPEM, issued.KeyPEM)
	if err != nil {
		t.Fatalf("loading the certificate: %v", err)
	}

	return cert
}

// writeGeneration writes data to the file name in dir and stamps it with a
// modification time that is a distinct function of generation, so that a
// rewrite is always seen as a change by the reloader, whatever the clock's
// granularity or the size of the data.
func writeGeneration(t *testing.T, dir, name string, data []byte, generation int) string {
	t.Helper()

	path := writeTestFile(t, dir, name, data)
	stamp := time.Unix(1_700_000_000, 0).Add(time.Duration(generation) * time.Minute)

	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatalf("setting the modification time of %s: %v", path, err)
	}

	return path
}

// reloadRig is a ReloadingServerTLS over files in a temporary directory, with
// one CA that signs the server's certificate and (unless a test says
// otherwise) the clients'.
type reloadRig struct {
	authority  *CertificateAuthority
	dir        string
	certFile   string
	keyFile    string
	caFile     string
	serverCert *IssuedCertificate
	reported   *errorLog
	server     *ReloadingServerTLS
}

func newReloadRig(t *testing.T) *reloadRig {
	t.Helper()

	rig := &reloadRig{
		authority: mustNewCA(t, time.Hour),
		dir:       t.TempDir(),
		reported:  &errorLog{},
	}

	rig.caFile = writeGeneration(t, rig.dir, "ca.pem", rig.authority.CertPEM(), 0)
	rig.writeServerCertificate(t, mustIssue(t, rig.authority, serverTestSpec()), 0)

	server, err := NewReloadingServerTLS(rig.certFile, rig.keyFile, rig.caFile, func(reloadErr error) { rig.reported.add("reload", reloadErr) })
	if err != nil {
		t.Fatalf("NewReloadingServerTLS(): %v", err)
	}

	rig.server = server

	return rig
}

// writeServerCertificate makes issued the server's certificate on disk.
func (rig *reloadRig) writeServerCertificate(t *testing.T, issued *IssuedCertificate, generation int) {
	t.Helper()

	rig.serverCert = issued
	rig.certFile = writeGeneration(t, rig.dir, "srv.pem", issued.CertPEM, generation)
	rig.keyFile = writeGeneration(t, rig.dir, "srv-key.pem", issued.KeyPEM, generation)
}

// handshake runs a handshake between the reloading server and a client
// presenting client, and returns both sides.
func (rig *reloadRig) handshake(t *testing.T, client *IssuedCertificate) (serverConn, clientConn *tls.Conn) {
	t.Helper()

	return tlsHandshakeBoth(t, rig.server.baseConfig(), tlsClientConfig(mustKeyPair(t, client), rig.authority.Pool()))
}

// presented returns the server certificate a client saw in a handshake.
func presented(clientConn *tls.Conn) *x509.Certificate {
	return clientConn.ConnectionState().PeerCertificates[0]
}

func TestReloadingServerTLSServesTheFilesAndAuthenticatesClients(t *testing.T) {
	rig := newReloadRig(t)
	alice := mustIssue(t, rig.authority, clientTestSpec())

	cfg, err := rig.server.getConfigForClient(nil)
	if err != nil {
		t.Fatalf("getConfigForClient(): %v", err)
	}

	if cfg.ClientAuth != tls.RequireAndVerifyClientCert || cfg.ClientCAs == nil || cfg.MinVersion != tls.VersionTLS13 ||
		!cfg.SessionTicketsDisabled || len(cfg.Certificates) != 1 {
		t.Fatalf("per-connection configuration = %+v, want mutual TLS 1.3, no session tickets, one certificate", cfg)
	}

	// The same validation ListenTLS applies to a fixed configuration.
	if _, validateErr := tlsServerConfig(cfg); validateErr != nil {
		t.Fatalf("tlsServerConfig(per-connection configuration): %v", validateErr)
	}

	// The base configuration cannot serve a handshake by itself.
	base := rig.server.baseConfig()
	if base.ClientAuth != tls.RequireAndVerifyClientCert || base.GetConfigForClient == nil || len(base.Certificates) != 0 || base.ClientCAs != nil {
		t.Fatalf("base configuration = %+v, want one that requires client certificates and defers everything else", base)
	}

	serverConn, clientConn := rig.handshake(t, alice)

	got, principalErr := TLSPeerPrincipal(serverConn)
	if principalErr != nil {
		t.Fatalf("TLSPeerPrincipal(): %v", principalErr)
	}

	if want := alice.Principal(); got != want {
		t.Fatalf("TLSPeerPrincipal() = %q, want %q", got, want)
	}

	if !presented(clientConn).Equal(rig.serverCert.Certificate) {
		t.Fatal("the server presented a certificate other than the one in its file")
	}
}

func TestReloadingServerTLSPicksUpARotatedServerCertificate(t *testing.T) {
	rig := newReloadRig(t)
	alice := mustIssue(t, rig.authority, clientTestSpec())

	_, before := rig.handshake(t, alice)
	first := presented(before)

	rotated := mustIssue(t, rig.authority, serverTestSpec())
	rig.writeServerCertificate(t, rotated, 1)

	_, after := rig.handshake(t, alice)
	second := presented(after)

	if second.Equal(first) || !second.Equal(rotated.Certificate) {
		t.Fatal("the server did not present the rotated certificate after its files changed")
	}

	if got := rig.reported.count(); got != 0 {
		t.Fatalf("%d problems reported for a clean rotation, want none", got)
	}
}

// TestReloadingServerTLSPicksUpARotatedClientCAPool checks which pool is
// served without a handshake: a rejecting handshake over net.Pipe would block
// the server writing its alert. The real rejection is covered over loopback
// TCP (TestFWNeededReloadingServerTLSRotatesTheClientCAWithoutARestart).
func TestReloadingServerTLSPicksUpARotatedClientCAPool(t *testing.T) {
	rig := newReloadRig(t)
	alice := mustIssue(t, rig.authority, clientTestSpec())

	otherAuthority := mustNewCA(t, time.Hour)
	bob := mustIssue(t, otherAuthority, clientTestSpec())

	trusts := func(client *IssuedCertificate) bool {
		cfg, err := rig.server.getConfigForClient(nil)
		if err != nil {
			t.Fatalf("getConfigForClient(): %v", err)
		}

		_, verifyErr := client.Certificate.Verify(x509.VerifyOptions{
			Roots:     cfg.ClientCAs,
			KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		})

		return verifyErr == nil
	}

	if !trusts(alice) || trusts(bob) {
		t.Fatal("before the rotation the server must trust alice's CA and not bob's")
	}

	writeGeneration(t, rig.dir, "ca.pem", otherAuthority.CertPEM(), 1)

	if trusts(alice) || !trusts(bob) {
		t.Fatal("after the rotation the server must trust bob's CA and no longer alice's")
	}
}

func TestReloadingServerTLSKeepsServingTheOldFilesWhenTheNewOnesAreBad(t *testing.T) {
	rig := newReloadRig(t)
	alice := mustIssue(t, rig.authority, clientTestSpec())

	_, first := rig.handshake(t, alice)
	original := presented(first)

	// A certificate file that is garbage while its key is still the old one.
	writeGeneration(t, rig.dir, "srv.pem", []byte("not a certificate"), 1)

	for i := range 2 {
		_, during := rig.handshake(t, alice)
		if !presented(during).Equal(original) {
			t.Fatalf("handshake %d: the server stopped presenting its working certificate", i)
		}
	}

	if got := rig.reported.count(); got != 1 {
		t.Fatalf("%d problems reported for one bad rewrite across two handshakes, want exactly 1", got)
	}

	if reloadErr := rig.server.Reload(); reloadErr == nil {
		t.Fatal("Reload() accepted a garbage certificate file")
	}

	rotated := mustIssue(t, rig.authority, serverTestSpec())
	rig.writeServerCertificate(t, rotated, 2)

	_, after := rig.handshake(t, alice)
	if !presented(after).Equal(rotated.Certificate) {
		t.Fatal("the server did not recover once the files were repaired")
	}

	if got := rig.reported.count(); got != 1 {
		t.Fatalf("%d problems reported in total, want still 1", got)
	}
}

func TestReloadingServerTLSReportsMissingFilesOnce(t *testing.T) {
	rig := newReloadRig(t)
	alice := mustIssue(t, rig.authority, clientTestSpec())

	if removeErr := os.Remove(rig.certFile); removeErr != nil {
		t.Fatalf("removing the certificate file: %v", removeErr)
	}

	for i := range 3 {
		_, during := rig.handshake(t, alice)
		if !presented(during).Equal(rig.serverCert.Certificate) {
			t.Fatalf("handshake %d: the server stopped presenting its working certificate", i)
		}
	}

	if got := rig.reported.count(); got != 1 {
		t.Fatalf("%d problems reported for a missing file across three handshakes, want exactly 1", got)
	}

	rig.writeServerCertificate(t, rig.serverCert, 1)

	_, after := rig.handshake(t, alice)
	if !presented(after).Equal(rig.serverCert.Certificate) {
		t.Fatal("the server did not carry on once the file was restored")
	}
}

func TestNewReloadingServerTLSFailsLoudlyOnBadFiles(t *testing.T) {
	rig := newReloadRig(t)

	missing := filepath.Join(rig.dir, "missing.pem")
	noCertificates := writeGeneration(t, rig.dir, "empty.pem", []byte("nothing here"), 0)

	cases := []struct {
		name string
		cert string
		key  string
		ca   string
		want error
	}{
		{name: "missing certificate", cert: missing, key: rig.keyFile, ca: rig.caFile, want: os.ErrNotExist},
		{name: "missing key", cert: rig.certFile, key: missing, ca: rig.caFile, want: os.ErrNotExist},
		{name: "missing CA file", cert: rig.certFile, key: rig.keyFile, ca: missing, want: os.ErrNotExist},
		{name: "CA file without certificates", cert: rig.certFile, key: rig.keyFile, ca: noCertificates, want: ErrNoCertificates},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewReloadingServerTLS(tc.cert, tc.key, tc.ca, nil); !errors.Is(err, tc.want) {
				t.Fatalf("NewReloadingServerTLS() error = %v, want %v", err, tc.want)
			}
		})
	}
}
