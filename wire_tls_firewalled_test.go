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

//go:build portmasterFirewalled

package dml

import (
	"crypto/tls"
	"errors"
	"testing"
	"time"
)

// This test opens a real TCP socket on 127.0.0.1, so like the one in
// wire_firewalled_test.go it is compiled only with -tags portmasterFirewalled
// (see test.bat) into the same stable binary, which one permanent firewall
// rule already covers.

// TestFWNeededWireOverMutualTLSAuthorizesByCertificate runs the client/host
// flow over TLS on loopback TCP: a certificate the policy lists may use its
// resource, another certificate from the same CA is admitted but denied, and a
// certificate from an unknown CA never gets a session.
func TestFWNeededWireOverMutualTLSAuthorizesByCertificate(t *testing.T) {
	ctx := hostTestContext(t)

	pki := newTLSTestPKI(t)
	serverCert, _ := pki.issue(t, "server")
	aliceCert, aliceLeaf := pki.issue(t, "alice")
	bobCert, _ := pki.issue(t, "bob")
	foreignCert, _ := newTLSTestPKI(t).issue(t, "mallory")

	reported := &errorLog{}
	h := openTestHost(t, boltTestPath(t), func(cfg *HostConfig) { cfg.OnError = reported.add })
	registerTestResource(t, h, "allowed-resource", nil)
	registerTestResource(t, h, "other-resource", nil)

	// "other-resource" has no entry: default deny.
	policy := NewResourcePolicy(map[string][]Principal{
		"allowed-resource": {TLSCertificatePrincipal(aliceLeaf)},
	})

	server := NewWireServer(h, WithPeerIdentifier(TLSPeerPrincipal), WithAuthorizer(policy))

	listener, err := ListenTLS("127.0.0.1:0", pki.serverConfig(serverCert))
	if err != nil {
		t.Fatalf("ListenTLS(): %v", err)
	}

	serveListener(t, server, listener)

	address := listener.Addr().String()

	dial := func(cert tls.Certificate) *WireClient {
		client, dialErr := DialTLS(ctx, address, tlsClientConfig(cert, pki.pool))
		if dialErr != nil {
			t.Fatalf("DialTLS(): %v", dialErr)
		}

		t.Cleanup(func() {
			if closeErr := client.Close(); closeErr != nil {
				t.Errorf("client Close(): %v", closeErr)
			}
		})

		return client
	}

	alice := dial(aliceCert)
	bob := dial(bobCert)

	if acquireErr := AcquireAndWait(ctx, alice, "allowed-resource"); acquireErr != nil {
		t.Fatalf("alice: AcquireAndWait(allowed-resource): %v", acquireErr)
	}

	if _, acquireErr := alice.Acquire(ctx, "other-resource"); !errors.Is(acquireErr, ErrNotAuthorized) {
		t.Fatalf("alice: Acquire(other-resource) error = %v, want %v", acquireErr, ErrNotAuthorized)
	}

	if _, acquireErr := bob.Acquire(ctx, "allowed-resource"); !errors.Is(acquireErr, ErrNotAuthorized) {
		t.Fatalf("bob: Acquire(allowed-resource) error = %v, want %v", acquireErr, ErrNotAuthorized)
	}

	// A certificate from an unknown CA fails the TLS handshake on the server,
	// so the wire handshake never completes and no session is opened.
	stranger, strangerErr := DialTLS(ctx, address, tlsClientConfig(foreignCert, pki.pool))
	if strangerErr == nil {
		closeQuietly(stranger)
		t.Fatal("a client with a certificate from an unknown CA was admitted")
	}

	// The server must have said why it turned the stranger away.
	eventually(t, "the rejected handshake to be reported", func() bool { return reported.has(ErrTLSHandshake) })

	sessions, sessionsErr := h.Registries().Leases.Sessions(h.Graph())
	if sessionsErr != nil || len(sessions) != 2 {
		t.Fatalf("Sessions() = (%v,%v), want exactly alice's and bob's sessions", sessions, sessionsErr)
	}
}

// TestFWNeededReloadingServerTLSRotatesTheClientCAWithoutARestart serves a
// ReloadingServerTLS over loopback TCP and replaces its file of client CAs
// while it runs: a client of the old CA is admitted and one of the new CA is
// not, then, after the rewrite, the other way round, with no restart.
func TestFWNeededReloadingServerTLSRotatesTheClientCAWithoutARestart(t *testing.T) {
	ctx := hostTestContext(t)
	dir := t.TempDir()

	serverCA := mustNewCA(t, time.Hour)
	oldClientCA := mustNewCA(t, time.Hour)
	newClientCA := mustNewCA(t, time.Hour)

	server := mustIssue(t, serverCA, serverTestSpec())
	alice := mustIssue(t, oldClientCA, clientTestSpec())
	bob := mustIssue(t, newClientCA, clientTestSpec())

	certFile := writeGeneration(t, dir, "srv.pem", server.CertPEM, 0)
	keyFile := writeGeneration(t, dir, "srv-key.pem", server.KeyPEM, 0)
	clientCAFile := writeGeneration(t, dir, "clients.pem", oldClientCA.CertPEM(), 0)

	reported := &errorLog{}
	h := openTestHost(t, boltTestPath(t), nil)

	reloading, newErr := NewReloadingServerTLS(certFile, keyFile, clientCAFile, func(reloadErr error) { reported.add("reload", reloadErr) })
	if newErr != nil {
		t.Fatalf("NewReloadingServerTLS(): %v", newErr)
	}

	listener, listenErr := reloading.Listen("127.0.0.1:0")
	if listenErr != nil {
		t.Fatalf("Listen(): %v", listenErr)
	}

	serveListener(t, NewWireServer(h, WithPeerIdentifier(TLSPeerPrincipal)), listener)

	address := listener.Addr().String()

	dial := func(issued *IssuedCertificate) (*WireClient, error) {
		return DialTLS(ctx, address, tlsClientConfig(mustKeyPair(t, issued), serverCA.Pool()))
	}

	admitted := func(who string, issued *IssuedCertificate) {
		client, dialErr := dial(issued)
		if dialErr != nil {
			t.Fatalf("%s was refused: %v", who, dialErr)
		}

		t.Cleanup(func() {
			if closeErr := client.Close(); closeErr != nil {
				t.Errorf("%s: client Close(): %v", who, closeErr)
			}
		})
	}

	refused := func(who string, issued *IssuedCertificate) {
		client, dialErr := dial(issued)
		if dialErr == nil {
			closeQuietly(client)
			t.Fatalf("%s was admitted", who)
		}
	}

	admitted("alice, before the rotation", alice)
	refused("bob, before the rotation", bob)

	writeGeneration(t, dir, "clients.pem", newClientCA.CertPEM(), 1)

	admitted("bob, after the rotation", bob)
	refused("alice, after the rotation", alice)

	if got := reported.count(); got != 0 {
		t.Fatalf("%d reload problems reported, want none", got)
	}
}
