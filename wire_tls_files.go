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
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"os"
	"slices"
	"sync"
)

// Loading the certificates and keys the TLS transport needs from PEM files
// (theorystate.md section 113). dmlcert (cmd/dmlcert) makes such files.

var (
	// ErrInvalidPEM is returned for PEM data that is not what was expected: no
	// PEM block, a block of the wrong type, or a block that does not parse.
	ErrInvalidPEM = errors.New("invalid PEM data")

	// ErrNoCertificates is returned for a CA file that holds no certificate.
	ErrNoCertificates = errors.New("no certificates found")
)

const (
	pemTypeCertificate = "CERTIFICATE"
	pemTypePrivateKey  = "PRIVATE KEY"
)

// readTLSFile reads a certificate or key file, naming it in the error.
func readTLSFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("tls: reading %s: %w", path, err)
	}

	return data, nil
}

// decodePEM returns the bytes of the first PEM block in data, which must be of
// type wantType.
func decodePEM(data []byte, wantType string) ([]byte, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("%w: no PEM block found", ErrInvalidPEM)
	}

	if block.Type != wantType {
		return nil, fmt.Errorf("%w: found a %q block, want %q", ErrInvalidPEM, block.Type, wantType)
	}

	return block.Bytes, nil
}

// encodePEM returns der as one PEM block of type blockType.
func encodePEM(blockType string, der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: der})
}

// parseCertificatePEM parses the first certificate in data.
func parseCertificatePEM(data []byte) (*x509.Certificate, error) {
	der, decodeErr := decodePEM(data, pemTypeCertificate)
	if decodeErr != nil {
		return nil, decodeErr
	}

	cert, parseErr := x509.ParseCertificate(der)
	if parseErr != nil {
		return nil, fmt.Errorf("%w: parsing the certificate: %w", ErrInvalidPEM, parseErr)
	}

	return cert, nil
}

// readCertPool reads every certificate in the PEM file at path into a pool.
func readCertPool(path string) (*x509.CertPool, error) {
	data, readErr := readTLSFile(path)
	if readErr != nil {
		return nil, readErr
	}

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(data) {
		return nil, fmt.Errorf("%w in %s", ErrNoCertificates, path)
	}

	return pool, nil
}

// CertificateFilePrincipal returns the Principal (TLSCertificatePrincipal) of
// the first certificate in the PEM file at path: what a ResourcePolicy lists
// for the holder of that certificate.
func CertificateFilePrincipal(path string) (Principal, error) {
	data, readErr := readTLSFile(path)
	if readErr != nil {
		return "", readErr
	}

	cert, parseErr := parseCertificatePEM(data)
	if parseErr != nil {
		return "", fmt.Errorf("tls: %s: %w", path, parseErr)
	}

	return TLSCertificatePrincipal(cert), nil
}

// LoadServerTLS builds the configuration ListenTLS needs from the server's
// certificate and private key files and the file of the CA(s) whose client
// certificates it accepts. The result requires and verifies client
// certificates and allows TLS 1.3 only. The private key file is the server's
// identity: protect it.
func LoadServerTLS(certFile, keyFile, clientCAFile string) (*tls.Config, error) {
	cert, keyErr := tls.LoadX509KeyPair(certFile, keyFile)
	if keyErr != nil {
		return nil, fmt.Errorf("tls: loading the server certificate %s and key %s: %w", certFile, keyFile, keyErr)
	}

	clients, poolErr := readCertPool(clientCAFile)
	if poolErr != nil {
		return nil, poolErr
	}

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientCAs:    clients,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		MinVersion:   tls.VersionTLS13,
	}, nil
}

// LoadClientTLS builds the configuration DialTLS needs from the client's
// certificate and private key files and the file of the CA(s) that verify the
// server. serverName is the name the server's certificate must carry; empty
// means the host part of the address passed to DialTLS. The private key file
// is the client's identity: protect it.
func LoadClientTLS(certFile, keyFile, serverCAFile, serverName string) (*tls.Config, error) {
	cert, keyErr := tls.LoadX509KeyPair(certFile, keyFile)
	if keyErr != nil {
		return nil, fmt.Errorf("tls: loading the client certificate %s and key %s: %w", certFile, keyFile, keyErr)
	}

	roots, poolErr := readCertPool(serverCAFile)
	if poolErr != nil {
		return nil, poolErr
	}

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      roots,
		ServerName:   serverName,
		MinVersion:   tls.VersionTLS13,
	}, nil
}

// fileStamp identifies one version of a file cheaply: its modification time
// (nanoseconds since the epoch) and its size. A file rewritten to the same
// size within the clock's granularity is not told apart from the old one,
// which is what ReloadingServerTLS.Reload is for.
type fileStamp struct {
	modTime int64
	size    int64
}

// stampFiles returns the fileStamp of each of paths, in order.
func stampFiles(paths ...string) ([]fileStamp, error) {
	stamps := make([]fileStamp, 0, len(paths))

	for _, path := range paths {
		info, statErr := os.Stat(path)
		if statErr != nil {
			return nil, fmt.Errorf("tls: checking %s: %w", path, statErr)
		}

		stamps = append(stamps, fileStamp{modTime: info.ModTime().UnixNano(), size: info.Size()})
	}

	return stamps, nil
}

// ReloadingServerTLS is the server side of the TLS transport with its
// certificate, private key and client CA file kept in files and re-read when
// they change, so a certificate or a client CA can be rotated without
// restarting the host (theorystate.md section 113).
//
// The files are looked at on every handshake (a stat of three files) and the
// configuration is rebuilt when any of them changed; Reload forces it. A
// rebuilt configuration comes from LoadServerTLS and is validated exactly as
// ListenTLS validates a fixed one. Files that cannot be loaded (half-written,
// a key that does not match, a file that vanished while being replaced) never
// take the host down: the previous configuration keeps serving, and the
// problem is passed to the error callback once (it is not re-read until the
// files change again). Only a bad first load is an error, from
// NewReloadingServerTLS.
//
// It cannot be handed to ListenTLS, which refuses a configuration with
// GetConfigForClient; use Listen. That is safe by construction: every
// connection is served with a per-connection configuration that went through
// the same validation, and the base configuration requires client
// certificates and has no certificate or CA of its own, so a handshake that
// did not get a validated one cannot succeed.
//
// Connections that are already established are not affected by a rotation: a
// CA removed from the client CA file does not end sessions it admitted
// earlier. Session tickets are disabled, so that a changed certificate or CA
// applies to every new connection and cannot be bypassed by a resumed
// session; the cost is one full handshake per connection, and a connection
// here is one long-lived session.
//
// A client needs no reloader: one connection is one session and DialTLS takes
// a configuration per dial, so a client that calls LoadClientTLS before each
// dial presents its current certificate. Its Principal is its certificate's
// fingerprint, so rotating a client certificate still means updating the
// ResourcePolicy first.
type ReloadingServerTLS struct {
	certFile     string
	keyFile      string
	clientCAFile string
	onError      func(error)

	mu          sync.Mutex
	cfg         *tls.Config
	attempted   []fileStamp // the files' stamps at the last (re)build attempt
	statProblem string      // the last file-check problem reported, to say it once
}

// NewReloadingServerTLS loads the files (see LoadServerTLS for what each is)
// and returns the reloader, or the error if they cannot be used. onError
// receives every later problem (see ReloadingServerTLS); it runs on a
// handshake's goroutine, so it must not block. Nil means problems are ignored.
func NewReloadingServerTLS(certFile, keyFile, clientCAFile string, onError func(error)) (*ReloadingServerTLS, error) {
	if onError == nil {
		onError = func(error) {}
	}

	r := &ReloadingServerTLS{
		certFile:     certFile,
		keyFile:      keyFile,
		clientCAFile: clientCAFile,
		onError:      onError,
	}

	stamps, stampErr := r.currentStamps()
	if stampErr != nil {
		return nil, stampErr
	}

	cfg, buildErr := r.build()
	if buildErr != nil {
		return nil, buildErr
	}

	r.cfg = cfg
	r.attempted = stamps

	return r, nil
}

// currentStamps returns the stamps of the three files.
func (r *ReloadingServerTLS) currentStamps() ([]fileStamp, error) {
	return stampFiles(r.certFile, r.keyFile, r.clientCAFile)
}

// build loads the files into a validated per-connection configuration.
func (r *ReloadingServerTLS) build() (*tls.Config, error) {
	loaded, loadErr := LoadServerTLS(r.certFile, r.keyFile, r.clientCAFile)
	if loadErr != nil {
		return nil, loadErr
	}

	cfg, cfgErr := tlsServerConfig(loaded)
	if cfgErr != nil {
		return nil, cfgErr
	}

	cfg.SessionTicketsDisabled = true

	return cfg, nil
}

// refresh returns the configuration to serve, rebuilding it first if the
// files changed since the last attempt. problem is a failure to report, never
// a reason not to serve: the previous configuration is returned instead.
func (r *ReloadingServerTLS) refresh() (cfg *tls.Config, problem error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	stamps, stampErr := r.currentStamps()
	if stampErr != nil {
		// Typically a file that is missing while it is being replaced: keep
		// serving, and say so once per distinct message, not per handshake.
		if message := stampErr.Error(); message != r.statProblem {
			r.statProblem = message
			problem = stampErr
		}

		return r.cfg, problem
	}

	r.statProblem = ""

	if slices.Equal(stamps, r.attempted) {
		return r.cfg, nil
	}

	// Recorded before the attempt, success or not: a failed rebuild is not
	// retried (and reported again) until the files change once more.
	r.attempted = stamps

	rebuilt, buildErr := r.build()
	if buildErr != nil {
		return r.cfg, buildErr
	}

	r.cfg = rebuilt

	return rebuilt, nil
}

// report passes a problem to the error callback; nil is nothing to report.
func (r *ReloadingServerTLS) report(problem error) {
	if problem != nil {
		r.onError(problem)
	}
}

// getConfigForClient is the tls.Config.GetConfigForClient of the base
// configuration: the configuration for each new connection. It never fails,
// because there is always a configuration to serve.
func (r *ReloadingServerTLS) getConfigForClient(_ *tls.ClientHelloInfo) (*tls.Config, error) {
	cfg, problem := r.refresh()
	r.report(problem)

	return cfg, nil
}

// baseConfig is the configuration the listener is created with. The real,
// per-connection configuration comes from getConfigForClient; the base one
// demands client certificates and holds neither a certificate nor a CA, so it
// fails closed if that path were ever skipped.
func (r *ReloadingServerTLS) baseConfig() *tls.Config {
	return &tls.Config{
		MinVersion:             tls.VersionTLS13,
		ClientAuth:             tls.RequireAndVerifyClientCert,
		SessionTicketsDisabled: true,
		GetConfigForClient:     r.getConfigForClient,
	}
}

// Reload re-reads the files now, whether or not they look changed. On failure
// the previous configuration stays in use and the error is returned (it is
// not also passed to the error callback).
func (r *ReloadingServerTLS) Reload() error {
	stamps, stampErr := r.currentStamps()
	if stampErr != nil {
		return stampErr
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.attempted = stamps

	rebuilt, buildErr := r.build()
	if buildErr != nil {
		return buildErr
	}

	r.cfg = rebuilt

	return nil
}

// Listen listens on the TCP address like ListenTLS, serving whatever
// configuration the files currently hold. Pass the result to
// WireServer.Serve together with WithPeerIdentifier(TLSPeerPrincipal) and an
// Authorizer.
func (r *ReloadingServerTLS) Listen(address string) (net.Listener, error) {
	return listenTLS(address, r.baseConfig())
}
