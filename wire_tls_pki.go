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
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"math/big"
	"net"
	"time"
)

// A small certificate authority for the TLS transport (theorystate.md section
// 113): enough to give a host and its clients certificates without any other
// tooling. dmlcert (cmd/dmlcert) is its command-line face.

var (
	// ErrCertificateSpec is returned for a certificate request that cannot be
	// issued: a missing name or use, a non-positive validity, or a validity
	// that outlives the CA.
	ErrCertificateSpec = errors.New("invalid certificate specification")

	// ErrCAKeyMismatch is returned when a CA certificate and key do not belong
	// together.
	ErrCAKeyMismatch = errors.New("the CA certificate and key do not belong together")

	// ErrNotCA is returned when a certificate given as a CA is not one.
	ErrNotCA = errors.New("the certificate is not a CA certificate")
)

const (
	// certBackdate is how far before now a new certificate is valid, so that a
	// peer whose clock runs a little behind still accepts it.
	certBackdate = 5 * time.Minute

	// serialBits is the size of a random certificate serial number.
	serialBits = 127
)

// CertificateAuthority signs certificates. It holds the CA's private key:
// whoever has it can make certificates every peer of this CA accepts.
type CertificateAuthority struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

// CertificateSpec says what a certificate is for.
type CertificateSpec struct {
	// CommonName names the holder.
	CommonName string

	// Names are the DNS names and IP addresses the certificate is valid for;
	// a server certificate needs at least one.
	Names []string

	// Server and Client are the uses the certificate is valid for; at least
	// one must be set.
	Server bool
	Client bool

	// Validity is how long the certificate is valid from now; it must not
	// outlive the CA.
	Validity time.Duration
}

// IssuedCertificate is a certificate made by CertificateAuthority.Issue, with
// its new private key, both as PEM ready to be written to files.
type IssuedCertificate struct {
	Certificate *x509.Certificate
	CertPEM     []byte
	KeyPEM      []byte
}

// Principal returns the Principal of the certificate (TLSCertificatePrincipal).
func (c *IssuedCertificate) Principal() Principal {
	return TLSCertificatePrincipal(c.Certificate)
}

// newCertificateKey makes a fresh ECDSA P-256 key.
func newCertificateKey() (*ecdsa.PrivateKey, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("tls: generating a key: %w", err)
	}

	return key, nil
}

// marshalKeyPEM encodes key as a PKCS#8 PEM block.
func marshalKeyPEM(key *ecdsa.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("tls: encoding a key: %w", err)
	}

	return encodePEM(pemTypePrivateKey, der), nil
}

// newSerialNumber returns a random positive certificate serial number.
func newSerialNumber() (*big.Int, error) {
	limit := new(big.Int).Lsh(big.NewInt(1), serialBits)

	serial, err := rand.Int(rand.Reader, limit)
	if err != nil {
		return nil, fmt.Errorf("tls: generating a serial number: %w", err)
	}

	return serial.Add(serial, big.NewInt(1)), nil
}

// NewCertificateAuthority creates a CA called commonName, valid for validity
// from now. It can sign leaf certificates only, not other CAs.
func NewCertificateAuthority(commonName string, validity time.Duration) (*CertificateAuthority, error) {
	if commonName == "" || validity <= 0 {
		return nil, fmt.Errorf("%w: a CA needs a common name and a positive validity", ErrCertificateSpec)
	}

	key, keyErr := newCertificateKey()
	if keyErr != nil {
		return nil, keyErr
	}

	serial, serialErr := newSerialNumber()
	if serialErr != nil {
		return nil, serialErr
	}

	now := time.Now()

	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: commonName},
		NotBefore:             now.Add(-certBackdate),
		NotAfter:              now.Add(validity),
		IsCA:                  true,
		BasicConstraintsValid: true,
		MaxPathLenZero:        true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}

	der, createErr := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if createErr != nil {
		return nil, fmt.Errorf("tls: creating the CA certificate: %w", createErr)
	}

	cert, parseErr := x509.ParseCertificate(der)
	if parseErr != nil {
		return nil, fmt.Errorf("tls: parsing the new CA certificate: %w", parseErr)
	}

	return &CertificateAuthority{cert: cert, key: key}, nil
}

// ParseCertificateAuthority restores a CA from the PEM of its certificate and
// its (PKCS#8, ECDSA) private key, as CertPEM and KeyPEM produce them.
func ParseCertificateAuthority(certPEM, keyPEM []byte) (*CertificateAuthority, error) {
	cert, certErr := parseCertificatePEM(certPEM)
	if certErr != nil {
		return nil, certErr
	}

	if !cert.IsCA {
		return nil, ErrNotCA
	}

	keyDER, decodeErr := decodePEM(keyPEM, pemTypePrivateKey)
	if decodeErr != nil {
		return nil, decodeErr
	}

	parsed, parseErr := x509.ParsePKCS8PrivateKey(keyDER)
	if parseErr != nil {
		return nil, fmt.Errorf("%w: parsing the CA key: %w", ErrInvalidPEM, parseErr)
	}

	key, isECDSA := parsed.(*ecdsa.PrivateKey)
	if !isECDSA {
		return nil, fmt.Errorf("%w: the CA key is a %T, want an ECDSA key", ErrInvalidPEM, parsed)
	}

	if !key.PublicKey.Equal(cert.PublicKey) {
		return nil, ErrCAKeyMismatch
	}

	return &CertificateAuthority{cert: cert, key: key}, nil
}

// LoadCertificateAuthority is ParseCertificateAuthority over two files.
func LoadCertificateAuthority(certFile, keyFile string) (*CertificateAuthority, error) {
	certPEM, certErr := readTLSFile(certFile)
	if certErr != nil {
		return nil, certErr
	}

	keyPEM, keyErr := readTLSFile(keyFile)
	if keyErr != nil {
		return nil, keyErr
	}

	authority, parseErr := ParseCertificateAuthority(certPEM, keyPEM)
	if parseErr != nil {
		return nil, fmt.Errorf("tls: CA %s and %s: %w", certFile, keyFile, parseErr)
	}

	return authority, nil
}

// Certificate returns the CA's own certificate.
func (ca *CertificateAuthority) Certificate() *x509.Certificate {
	return ca.cert
}

// CertPEM returns the CA certificate as PEM: the file clients and servers load
// to trust this CA. It is public.
func (ca *CertificateAuthority) CertPEM() []byte {
	return encodePEM(pemTypeCertificate, ca.cert.Raw)
}

// KeyPEM returns the CA's private key as PEM. Keep it secret.
func (ca *CertificateAuthority) KeyPEM() ([]byte, error) {
	return marshalKeyPEM(ca.key)
}

// Pool returns a certificate pool trusting only this CA.
func (ca *CertificateAuthority) Pool() *x509.CertPool {
	pool := x509.NewCertPool()
	pool.AddCert(ca.cert)

	return pool
}

// validate reports whether s describes a certificate that can be issued
// (without looking at any CA).
func (s CertificateSpec) validate() error {
	switch {
	case s.CommonName == "":
		return fmt.Errorf("%w: a certificate needs a common name", ErrCertificateSpec)
	case !s.Server && !s.Client:
		return fmt.Errorf("%w: a certificate must be for a server, a client or both", ErrCertificateSpec)
	case s.Validity <= 0:
		return fmt.Errorf("%w: the validity must be positive", ErrCertificateSpec)
	case s.Server && len(s.Names) == 0:
		return fmt.Errorf("%w: a server certificate needs at least one name", ErrCertificateSpec)
	}

	for _, name := range s.Names {
		if name == "" {
			return fmt.Errorf("%w: a name must not be empty", ErrCertificateSpec)
		}
	}

	return nil
}

// template returns the x509 template for s, valid from just before now.
func (s CertificateSpec) template(serial *big.Int, now time.Time) *x509.Certificate {
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: s.CommonName},
		NotBefore:    now.Add(-certBackdate),
		NotAfter:     now.Add(s.Validity),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}

	if s.Server {
		template.ExtKeyUsage = append(template.ExtKeyUsage, x509.ExtKeyUsageServerAuth)
	}

	if s.Client {
		template.ExtKeyUsage = append(template.ExtKeyUsage, x509.ExtKeyUsageClientAuth)
	}

	for _, name := range s.Names {
		if ip := net.ParseIP(name); ip != nil {
			template.IPAddresses = append(template.IPAddresses, ip)
		} else {
			template.DNSNames = append(template.DNSNames, name)
		}
	}

	return template
}

// Issue makes a certificate and a new private key for spec, signed by the CA.
// The certificate may not outlive the CA, and an expired CA issues nothing.
func (ca *CertificateAuthority) Issue(spec CertificateSpec) (*IssuedCertificate, error) {
	if specErr := spec.validate(); specErr != nil {
		return nil, specErr
	}

	now := time.Now()

	if !now.Before(ca.cert.NotAfter) {
		return nil, fmt.Errorf("%w: the CA expired at %v", ErrCertificateSpec, ca.cert.NotAfter)
	}

	if now.Add(spec.Validity).After(ca.cert.NotAfter) {
		return nil, fmt.Errorf("%w: a validity of %v would outlive the CA, which expires at %v", ErrCertificateSpec, spec.Validity, ca.cert.NotAfter)
	}

	key, keyErr := newCertificateKey()
	if keyErr != nil {
		return nil, keyErr
	}

	serial, serialErr := newSerialNumber()
	if serialErr != nil {
		return nil, serialErr
	}

	der, createErr := x509.CreateCertificate(rand.Reader, spec.template(serial, now), ca.cert, &key.PublicKey, ca.key)
	if createErr != nil {
		return nil, fmt.Errorf("tls: creating the certificate of %q: %w", spec.CommonName, createErr)
	}

	leaf, parseErr := x509.ParseCertificate(der)
	if parseErr != nil {
		return nil, fmt.Errorf("tls: parsing the new certificate of %q: %w", spec.CommonName, parseErr)
	}

	keyPEM, marshalErr := marshalKeyPEM(key)
	if marshalErr != nil {
		return nil, marshalErr
	}

	return &IssuedCertificate{
		Certificate: leaf,
		CertPEM:     encodePEM(pemTypeCertificate, der),
		KeyPEM:      keyPEM,
	}, nil
}