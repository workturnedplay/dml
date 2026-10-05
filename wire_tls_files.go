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
	"os"
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