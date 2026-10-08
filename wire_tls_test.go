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
	"net"
	"testing"
	"time"
)

// The tests in this file need no sockets: TLS runs over net.Pipe. The one
// test that uses a real TCP socket is in wire_tls_firewalled_test.go, behind
// the portmasterFirewalled build tag.

// tlsTestPKI is a throwaway certificate authority (the same CertificateAuthority
// dmlcert uses) that issues leaf certificates usable both as TLS server and as
// TLS client certificates.
type tlsTestPKI struct {
	ca   *CertificateAuthority
	pool *x509.CertPool
}

func newTLSTestPKI(t *testing.T) *tlsTestPKI {
	t.Helper()

	// Longer than the one hour its leaves are valid for: a leaf may not
	// outlive its CA.
	authority, err := NewCertificateAuthority("dml test CA", 2*time.Hour)
	if err != nil {
		t.Fatalf("NewCertificateAuthority(): %v", err)
	}

	return &tlsTestPKI{ca: authority, pool: authority.Pool()}
}

// issue creates a leaf certificate named name, valid for 127.0.0.1 and
// localhost, signed by the CA. It goes through PEM and back, like the files
// dmlcert writes.
func (p *tlsTestPKI) issue(t *testing.T, name string) (tls.Certificate, *x509.Certificate) {
	t.Helper()

	issued, issueErr := p.ca.Issue(CertificateSpec{
		CommonName: name,
		Names:      []string{"localhost", "127.0.0.1"},
		Server:     true,
		Client:     true,
		Validity:   time.Hour,
	})
	if issueErr != nil {
		t.Fatalf("issuing the certificate of %q: %v", name, issueErr)
	}

	return mustKeyPair(t, issued), issued.Certificate
}

// serverConfig is a valid ListenTLS configuration trusting this CA's clients.
// Session tickets are off so nothing is written after the handshake, which
// keeps net.Pipe (unbuffered) from blocking a side that is not reading.
func (p *tlsTestPKI) serverConfig(cert tls.Certificate) *tls.Config {
	return &tls.Config{
		Certificates:           []tls.Certificate{cert},
		ClientCAs:              p.pool,
		ClientAuth:             tls.RequireAndVerifyClientCert,
		MinVersion:             tls.VersionTLS13,
		SessionTicketsDisabled: true,
	}
}

// tlsClientConfig is a client configuration presenting cert and verifying
// the server against roots.
func tlsClientConfig(cert tls.Certificate, roots *x509.CertPool) *tls.Config {
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      roots,
		ServerName:   "localhost",
		MinVersion:   tls.VersionTLS13,
	}
}

// tlsHandshakeBoth runs a TLS handshake between a server and a client over
// net.Pipe and returns both sides. Both pipe ends are closed directly when
// the test ends: closing a tls.Conn would try to write a close_notify that
// nobody reads. It only fits handshakes that succeed: over the unbuffered
// pipe a rejecting server would block writing its alert.
func tlsHandshakeBoth(t *testing.T, serverCfg, clientCfg *tls.Config) (serverConn, clientConn *tls.Conn) {
	t.Helper()

	clientPipe, serverPipe := net.Pipe()

	t.Cleanup(func() {
		closeQuietly(clientPipe)
		closeQuietly(serverPipe)
	})

	serverConn = tls.Server(serverPipe, serverCfg)
	clientConn = tls.Client(clientPipe, clientCfg)

	serverDone := make(chan error, 1)

	go func() { serverDone <- serverConn.Handshake() }()

	if clientErr := clientConn.Handshake(); clientErr != nil {
		t.Fatalf("client handshake: %v", clientErr)
	}

	if serverErr := <-serverDone; serverErr != nil {
		t.Fatalf("server handshake: %v", serverErr)
	}

	return serverConn, clientConn
}

// tlsHandshakePair is tlsHandshakeBoth returning only the server side.
func tlsHandshakePair(t *testing.T, serverCfg, clientCfg *tls.Config) *tls.Conn {
	t.Helper()

	serverConn, _ := tlsHandshakeBoth(t, serverCfg, clientCfg)

	return serverConn
}

func TestTLSCertificatePrincipalIsTheCertificateFingerprint(t *testing.T) {
	pki := newTLSTestPKI(t)
	_, first := pki.issue(t, "first")
	_, second := pki.issue(t, "second")

	principal := TLSCertificatePrincipal(first)

	if again := TLSCertificatePrincipal(first); principal != again {
		t.Fatalf("TLSCertificatePrincipal() = %q then %q, want the same Principal both times", principal, again)
	}

	if other := TLSCertificatePrincipal(second); principal == other {
		t.Fatalf("two different certificates share the Principal %q", principal)
	}

	const hexLength = 64 // SHA-256 in hex

	if len(principal) != len(tlsPrincipalPrefix)+hexLength || principal[:len(tlsPrincipalPrefix)] != tlsPrincipalPrefix {
		t.Fatalf("TLSCertificatePrincipal() = %q, want %q followed by %d hex digits", principal, tlsPrincipalPrefix, hexLength)
	}
}

func TestListenTLSRefusesAConfigThatDoesNotEnforceMutualAuthentication(t *testing.T) {
	pki := newTLSTestPKI(t)
	serverCert, _ := pki.issue(t, "server")
	good := pki.serverConfig(serverCert)

	noAuth := good.Clone()
	noAuth.ClientAuth = tls.NoClientCert

	optional := good.Clone()
	optional.ClientAuth = tls.VerifyClientCertIfGiven

	noCAs := good.Clone()
	noCAs.ClientCAs = nil

	dynamic := good.Clone()
	dynamic.GetConfigForClient = func(*tls.ClientHelloInfo) (*tls.Config, error) { return good, nil }

	cases := []struct {
		name string
		cfg  *tls.Config
	}{
		{name: "nil", cfg: nil},
		{name: "no client certificates", cfg: noAuth},
		{name: "client certificates optional", cfg: optional},
		{name: "no client CAs", cfg: noCAs},
		{name: "per-connection configuration", cfg: dynamic},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Validation comes before binding, so no socket is opened.
			if listener, err := ListenTLS("127.0.0.1:0", tc.cfg); !errors.Is(err, ErrTLSClientAuthRequired) {
				if err == nil {
					closeQuietly(listener)
				}

				t.Fatalf("ListenTLS() error = %v, want %v", err, ErrTLSClientAuthRequired)
			}
		})
	}
}

func TestTLSServerConfigRaisesTheMinimumVersionOnACopy(t *testing.T) {
	pki := newTLSTestPKI(t)
	serverCert, _ := pki.issue(t, "server")

	low := pki.serverConfig(serverCert)
	low.MinVersion = 0

	got, err := tlsServerConfig(low)
	if err != nil {
		t.Fatalf("tlsServerConfig(): %v", err)
	}

	if got == low {
		t.Fatal("tlsServerConfig() returned the caller's own configuration, want a copy")
	}
	if got.MinVersion != tls.VersionTLS12 {
		t.Fatalf("MinVersion = %#x, want TLS 1.2", got.MinVersion)
	}
	if low.MinVersion != 0 {
		t.Fatalf("the caller's MinVersion was changed to %#x", low.MinVersion)
	}

	strict := pki.serverConfig(serverCert)

	kept, err := tlsServerConfig(strict)
	if err != nil {
		t.Fatalf("tlsServerConfig(TLS 1.3): %v", err)
	}
	if kept.MinVersion != tls.VersionTLS13 {
		t.Fatalf("MinVersion = %#x, want TLS 1.3 kept", kept.MinVersion)
	}
}

func TestTLSPeerPrincipalIsTheVerifiedClientCertificate(t *testing.T) {
	pki := newTLSTestPKI(t)
	serverCert, _ := pki.issue(t, "server")
	clientCert, clientLeaf := pki.issue(t, "client")

	serverConn := tlsHandshakePair(t, pki.serverConfig(serverCert), tlsClientConfig(clientCert, pki.pool))

	got, err := TLSPeerPrincipal(serverConn)
	if err != nil {
		t.Fatalf("TLSPeerPrincipal(): %v", err)
	}

	if want := TLSCertificatePrincipal(clientLeaf); got != want {
		t.Fatalf("TLSPeerPrincipal() = %q, want %q", got, want)
	}
}

func TestTLSPeerPrincipalRefusesAPeerWithoutAVerifiedCertificate(t *testing.T) {
	pki := newTLSTestPKI(t)
	serverCert, _ := pki.issue(t, "server")

	// A server that does not ask for client certificates, and a client that
	// has none: the handshake succeeds, but there is no identity.
	serverCfg := pki.serverConfig(serverCert)
	serverCfg.ClientAuth = tls.NoClientCert

	clientCfg := &tls.Config{RootCAs: pki.pool, ServerName: "localhost", MinVersion: tls.VersionTLS13}

	serverConn := tlsHandshakePair(t, serverCfg, clientCfg)

	if _, err := TLSPeerPrincipal(serverConn); !errors.Is(err, ErrPeerUnidentified) {
		t.Fatalf("TLSPeerPrincipal() without a client certificate error = %v, want %v", err, ErrPeerUnidentified)
	}
}

func TestTLSPeerPrincipalRefusesConnectionsThatAreNotCompletedTLS(t *testing.T) {
	pki := newTLSTestPKI(t)
	serverCert, _ := pki.issue(t, "server")

	clientPipe, serverPipe := net.Pipe()

	t.Cleanup(func() {
		closeQuietly(clientPipe)
		closeQuietly(serverPipe)
	})

	if _, err := TLSPeerPrincipal(serverPipe); !errors.Is(err, ErrPeerUnidentified) {
		t.Fatalf("TLSPeerPrincipal(net.Pipe) error = %v, want %v", err, ErrPeerUnidentified)
	}

	// A TLS connection whose handshake has not happened yet.
	notYet := tls.Server(serverPipe, pki.serverConfig(serverCert))

	if _, err := TLSPeerPrincipal(notYet); !errors.Is(err, ErrPeerUnidentified) {
		t.Fatalf("TLSPeerPrincipal(before the handshake) error = %v, want %v", err, ErrPeerUnidentified)
	}
}

// serveInMemoryTLS is serveInMemory with a TLS server on the server side of
// the pipe, so the server runs its own TLS handshake on whatever the peer
// sends. It returns the raw client side, which is closed (and the handler
// awaited) when the test ends.
func serveInMemoryTLS(t *testing.T, server *WireServer, cfg *tls.Config) net.Conn {
	t.Helper()

	clientSide, serverSide := net.Pipe()
	done := make(chan struct{})

	go func() {
		defer close(done)
		server.ServeConn(tls.Server(serverSide, cfg))
	}()

	t.Cleanup(func() {
		closeQuietly(clientSide)
		<-done
	})

	return clientSide
}

// TestWireServerReportsAPeerThatDoesNotSpeakTLS: an HTTP request sent to the
// TLS port is a mistake that really happens. It fails the handshake, and the
// server must say so.
func TestWireServerReportsAPeerThatDoesNotSpeakTLS(t *testing.T) {
	pki := newTLSTestPKI(t)
	serverCert, _ := pki.issue(t, "server")

	reported := &errorLog{}
	rig := newWireRig(t, func(cfg *HostConfig) { cfg.OnError = reported.add })

	peer := serveInMemoryTLS(t, rig.server, pki.serverConfig(serverCert))

	if _, writeErr := peer.Write([]byte("GET / HTTP/1.1\r\n\r\n")); writeErr != nil {
		t.Fatalf("writing to the server: %v", writeErr)
	}

	eventually(t, "the failed TLS handshake to be reported", func() bool { return reported.has(ErrTLSHandshake) })

	sessions, sessionsErr := rig.host.Registries().Leases.Sessions(rig.host.Graph())
	if sessionsErr != nil || len(sessions) != 0 {
		t.Fatalf("Sessions() = (%v,%v), want none for a failed handshake", sessions, sessionsErr)
	}
}

// TestWireServerDoesNotReportAPeerThatLeavesBeforeTheTLSHandshake: a probe
// that connects and closes without a byte is routine, not a failure to report.
func TestWireServerDoesNotReportAPeerThatLeavesBeforeTheTLSHandshake(t *testing.T) {
	pki := newTLSTestPKI(t)
	serverCert, _ := pki.issue(t, "server")

	reported := &errorLog{}
	rig := newWireRig(t, func(cfg *HostConfig) { cfg.OnError = reported.add })

	clientSide, serverSide := net.Pipe()
	closeQuietly(clientSide)

	// Returns once the handshake has failed: the peer is already gone.
	rig.server.ServeConn(tls.Server(serverSide, pki.serverConfig(serverCert)))

	if reported.has(ErrTLSHandshake) {
		t.Fatal("a peer that left without sending anything was reported")
	}
}
