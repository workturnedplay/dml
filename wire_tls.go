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
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
)

// Mutual-TLS transport for the wire protocol (theorystate.md section 113).
// Who may connect at all is decided by the TLS handshake (the client's
// certificate must verify against the server's ClientCAs, the counterpart of
// the named pipe's DACL); which resource a connection may use is still the
// server's Authorizer, and the Principal it sees is the certificate's
// fingerprint (TLSPeerPrincipal).

// ErrTLSClientAuthRequired is returned by ListenTLS for a configuration that
// would let a client connect without a verified certificate.
var ErrTLSClientAuthRequired = errors.New("the TLS server configuration must require verified client certificates (ClientAuth RequireAndVerifyClientCert, ClientCAs set, no GetConfigForClient)")

// ErrTLSHandshake wraps the error of a connection the server dropped because
// its TLS handshake failed: the client's certificate did not verify, it
// presented none, or the peer was not speaking TLS at all. The server reports
// it (HostConfig.OnError) with the peer's address; the client may see only a
// TLS alert.
var ErrTLSHandshake = errors.New("TLS handshake failed")

// tlsPrincipalPrefix starts every Principal TLSCertificatePrincipal makes, so
// a certificate Principal can never be mistaken for another kind (a Windows
// SID, say).
const tlsPrincipalPrefix = "tls-sha256:"

// TLSCertificatePrincipal returns the Principal for cert: the SHA-256
// fingerprint of its DER encoding. Use it to build a ResourcePolicy from the
// certificates that may use each resource.
func TLSCertificatePrincipal(cert *x509.Certificate) Principal {
	sum := sha256.Sum256(cert.Raw)

	return Principal(tlsPrincipalPrefix + hex.EncodeToString(sum[:]))
}

// TLSPeerPrincipal is a PeerIdentifier for connections accepted from
// ListenTLS: the Principal is TLSCertificatePrincipal of the client's leaf
// certificate. A connection that is not a completed TLS connection, or whose
// client has no verified certificate, is ErrPeerUnidentified (the server
// turns that into the empty Principal, which a ResourcePolicy denies).
func TLSPeerPrincipal(nc net.Conn) (Principal, error) {
	tc, ok := nc.(*tls.Conn)
	if !ok {
		return "", fmt.Errorf("%w: %T is not a TLS connection", ErrPeerUnidentified, nc)
	}

	state := tc.ConnectionState()
	if !state.HandshakeComplete || len(state.VerifiedChains) == 0 || len(state.PeerCertificates) == 0 {
		return "", fmt.Errorf("%w: the TLS peer presented no verified certificate", ErrPeerUnidentified)
	}

	return TLSCertificatePrincipal(state.PeerCertificates[0]), nil
}

// isPlainTCP reports whether nc is an unencrypted TCP connection: its remote
// address is a TCP address and it is not a *tls.Conn. A TLS connection is not
// plain even though its address is TCP. Anything else (a named pipe, an
// in-memory pipe, a unix socket) is not TCP at all. A custom wrapper around a
// *tls.Conn is not recognized as TLS, so it counts as plain: the check fails
// closed, and WithInsecurePlainTCP is the way out for such a setup.
func isPlainTCP(nc net.Conn) bool {
	if _, isTLS := nc.(*tls.Conn); isTLS {
		return false
	}

	_, isTCP := nc.RemoteAddr().(*net.TCPAddr)

	return isTCP
}

// completeTLS performs the TLS handshake of nc, if it is a TLS connection,
// within wireHelloTimeout, so that a failure is known as a TLS failure and
// carries the peer's address instead of surfacing later as an anonymous read
// error. Any other connection needs no handshake and is left alone.
func completeTLS(nc net.Conn) error {
	tc, isTLS := nc.(*tls.Conn)
	if !isTLS {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), wireHelloTimeout)
	defer cancel()

	if handshakeErr := tc.HandshakeContext(ctx); handshakeErr != nil {
		return fmt.Errorf("%w from %v: %w", ErrTLSHandshake, nc.RemoteAddr(), handshakeErr)
	}

	return nil
}

// reportTLSFailure reports a failed TLS handshake (see completeTLS) through
// the host's error hook. It is how an operator learns that a client with an
// unknown or missing certificate is being turned away; nothing else records
// it. Two cases are routine and stay silent: a peer that connected and left
// without sending a byte (io.EOF), such as a TCP health probe, and a handshake
// cut short because the server is closing.
func (s *WireServer) reportTLSFailure(err error) {
	if errors.Is(err, io.EOF) || s.isClosed() {
		return
	}

	s.host.report("wire: rejecting a TLS connection", err)
}

// tlsServerConfig validates cfg and returns a private copy of it for a
// listener: mutual authentication must be enforced by cfg itself, and the
// minimum protocol version is raised to TLS 1.2 if it was lower. cfg is not
// modified.
func tlsServerConfig(cfg *tls.Config) (*tls.Config, error) {
	if cfg == nil ||
		cfg.ClientAuth != tls.RequireAndVerifyClientCert ||
		cfg.ClientCAs == nil ||
		cfg.GetConfigForClient != nil {
		return nil, ErrTLSClientAuthRequired
	}

	server := cfg.Clone()
	server.MinVersion = max(server.MinVersion, tls.VersionTLS12)

	return server, nil
}

// ListenTLS listens on the TCP address and wraps the listener in TLS with
// cfg, which must require and verify client certificates (see
// ErrTLSClientAuthRequired). Pass the result to WireServer.Serve together with
// WithPeerIdentifier(TLSPeerPrincipal) and an Authorizer. The server completes
// the TLS handshake of each connection itself, within its hello timeout, and
// a client that fails it never gets a session: the failure is reported to the
// host's error hook (ErrTLSHandshake).
func ListenTLS(address string, cfg *tls.Config) (net.Listener, error) {
	serverCfg, cfgErr := tlsServerConfig(cfg)
	if cfgErr != nil {
		return nil, cfgErr
	}

	return listenTLS(address, serverCfg)
}

// listenTLS listens on the TCP address and wraps the listener in TLS with cfg,
// which the caller has already validated. It is shared by ListenTLS (a fixed,
// validated configuration) and ReloadingServerTLS.Listen (a configuration
// that validates each per-connection configuration it hands out).
func listenTLS(address string, cfg *tls.Config) (net.Listener, error) {
	var lc net.ListenConfig

	listener, listenErr := lc.Listen(context.Background(), "tcp", address)
	if listenErr != nil {
		return nil, fmt.Errorf("tls: listening on %s: %w", address, listenErr)
	}

	return tls.NewListener(listener, cfg), nil
}

// DialTLS connects to a ListenTLS server at address, with cfg (which carries
// the client certificate and the roots that verify the server), and performs
// the wire handshake. ctx bounds the dial, the TLS handshake and the wire
// handshake only.
func DialTLS(ctx context.Context, address string, cfg *tls.Config) (*WireClient, error) {
	dialer := tls.Dialer{Config: cfg}

	nc, dialErr := dialer.DialContext(ctx, "tcp", address)
	if dialErr != nil {
		return nil, fmt.Errorf("tls: dialing %s: %w", address, dialErr)
	}

	return NewWireClient(ctx, nc)
}
