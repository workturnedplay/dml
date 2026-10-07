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

// Command dmlcert makes the certificates the dml wire transport uses over
// TLS: a certificate authority, a server certificate and one certificate per
// client, and it prints the Principal (the "tls-sha256:" fingerprint) a
// ResourcePolicy lists to let a client use a resource. It never overwrites an
// existing file.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"dml"
)

const usageText = `dmlcert makes the certificates the dml wire transport uses over TLS.

usage:
  dmlcert ca        [-dir DIR] [-name CN] [-days N]
      make the CA: DIR/ca.pem (public) and DIR/ca-key.pem (SECRET)
  dmlcert server    [-dir DIR] [-name NAME] [-hosts LIST] [-days N]
      make a server certificate NAME.pem and NAME-key.pem, valid for the
      comma-separated DNS names and IP addresses in LIST
  dmlcert client    [-dir DIR] -name NAME [-days N]
      make a client certificate NAME.pem and NAME-key.pem and print the
      Principal that lists it in a ResourcePolicy
  dmlcert principal FILE...
      print the Principal of each certificate file

DIR defaults to the current directory. server and client read the CA from DIR.
Every file is written with mode 0600 and nothing is ever overwritten.
`

const (
	caBase          = "ca"
	defaultCADays   = 3650
	defaultLeafDays = 365
	maxDays         = 36500

	privateFileMode os.FileMode = 0o600
	privateDirMode  os.FileMode = 0o700
)

var errNoCommand = errors.New("no command given")

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		say(os.Stderr, "dmlcert: %v\n", err)
		os.Exit(1)
	}
}

// say writes formatted text to w. A failed write has nobody to tell.
func say(w io.Writer, format string, args ...any) {
	if _, err := fmt.Fprintf(w, format, args...); err != nil {
		_ = err
	}
}

// run executes one command line.
func run(args []string, out, errOut io.Writer) error {
	if len(args) == 0 {
		say(errOut, "%s", usageText)

		return errNoCommand
	}

	command, rest := args[0], args[1:]

	switch command {
	case "ca":
		return runCA(rest, out, errOut)
	case "server":
		return runServer(rest, out, errOut)
	case "client":
		return runClient(rest, out, errOut)
	case "principal":
		return runPrincipal(rest, out, errOut)
	case "help", "-h", "-help", "--help":
		say(out, "%s", usageText)

		return nil
	default:
		return fmt.Errorf("unknown command %q (try \"dmlcert help\")", command)
	}
}

// newFlagSet returns a flag set that prints nothing by itself: errors are
// returned and -h is handled by handleFlagError.
func newFlagSet(name string) *flag.FlagSet {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(io.Discard)

	return flags
}

// handleFlagError turns a flag parsing failure into the command's result: -h
// prints the flags and succeeds, anything else is an error.
func handleFlagError(flags *flag.FlagSet, parseErr error, errOut io.Writer) error {
	if !errors.Is(parseErr, flag.ErrHelp) {
		return fmt.Errorf("%s: %w", flags.Name(), parseErr)
	}

	say(errOut, "usage of dmlcert %s:\n", flags.Name())
	flags.SetOutput(errOut)
	flags.PrintDefaults()

	return nil
}

// validityFromDays converts a number of days to a validity.
func validityFromDays(days int) (time.Duration, error) {
	if days <= 0 || days > maxDays {
		return 0, fmt.Errorf("-days must be between 1 and %d, got %d", maxDays, days)
	}

	return time.Duration(days) * 24 * time.Hour, nil
}

// splitList splits a comma-separated list, dropping blanks.
func splitList(list string) []string {
	parts := strings.Split(list, ",")
	items := make([]string, 0, len(parts))

	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			items = append(items, trimmed)
		}
	}

	return items
}

// isSafeFileName reports whether name is a plain file name: no directory, no
// separator, nothing that names another place.
func isSafeFileName(name string) bool {
	return name != "" && name != "." && name != ".." &&
		name == filepath.Base(name) && !strings.ContainsAny(name, `/\:`)
}

// pairPaths returns the certificate and key file paths for base in dir.
func pairPaths(dir, base string) (string, string) {
	return filepath.Join(dir, base+".pem"), filepath.Join(dir, base+"-key.pem")
}

// requireAbsent fails if any of paths exists (or cannot be checked).
func requireAbsent(paths ...string) error {
	for _, path := range paths {
		_, statErr := os.Lstat(path)

		switch {
		case statErr == nil:
			return fmt.Errorf("%s already exists; refusing to overwrite it", path)
		case !errors.Is(statErr, os.ErrNotExist):
			return fmt.Errorf("checking %s: %w", path, statErr)
		}
	}

	return nil
}

// writeNewFile creates path, which must not exist, with data in it.
func writeNewFile(path string, data []byte) error {
	file, openErr := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, privateFileMode)
	if openErr != nil {
		return fmt.Errorf("creating %s: %w", path, openErr)
	}

	_, writeErr := file.Write(data)
	closeErr := file.Close()

	if joined := errors.Join(writeErr, closeErr); joined != nil {
		return fmt.Errorf("writing %s: %w", path, joined)
	}

	return nil
}

// writePair writes certPEM and keyPEM as base.pem and base-key.pem in dir,
// creating dir if needed, and returns the two paths. Nothing is written if
// either file exists already.
func writePair(dir, base string, certPEM, keyPEM []byte) (string, string, error) {
	certPath, keyPath := pairPaths(dir, base)

	if absentErr := requireAbsent(certPath, keyPath); absentErr != nil {
		return "", "", absentErr
	}

	if mkdirErr := os.MkdirAll(dir, privateDirMode); mkdirErr != nil {
		return "", "", fmt.Errorf("creating %s: %w", dir, mkdirErr)
	}

	// The key first, so a certificate never exists without its key.
	if keyErr := writeNewFile(keyPath, keyPEM); keyErr != nil {
		return "", "", keyErr
	}

	if certErr := writeNewFile(certPath, certPEM); certErr != nil {
		return "", "", certErr
	}

	return certPath, keyPath, nil
}

// reportWritten tells the user which files were made.
func reportWritten(out io.Writer, certPath, keyPath string) {
	say(out, "wrote %s (certificate) and %s (PRIVATE KEY: keep it secret)\n", certPath, keyPath)
}

func runCA(args []string, out, errOut io.Writer) error {
	flags := newFlagSet("ca")
	dir := flags.String("dir", ".", "directory for ca.pem and ca-key.pem (created if missing)")
	name := flags.String("name", "dml CA", "common name of the CA")
	days := flags.Int("days", defaultCADays, "validity in days")

	if parseErr := flags.Parse(args); parseErr != nil {
		return handleFlagError(flags, parseErr, errOut)
	}

	validity, daysErr := validityFromDays(*days)
	if daysErr != nil {
		return daysErr
	}

	authority, newErr := dml.NewCertificateAuthority(*name, validity)
	if newErr != nil {
		return fmt.Errorf("creating the CA: %w", newErr)
	}

	keyPEM, keyErr := authority.KeyPEM()
	if keyErr != nil {
		return fmt.Errorf("encoding the CA key: %w", keyErr)
	}

	certPath, keyPath, writeErr := writePair(*dir, caBase, authority.CertPEM(), keyPEM)
	if writeErr != nil {
		return writeErr
	}

	reportWritten(out, certPath, keyPath)
	say(out, "give %s to every server and client; never give out %s\n", certPath, keyPath)

	return nil
}

// issueLeaf issues a certificate for spec from the CA in dir and writes it as
// base.pem and base-key.pem there.
func issueLeaf(out io.Writer, dir, base string, spec dml.CertificateSpec) error {
	if !isSafeFileName(base) {
		return fmt.Errorf("%q cannot be used as a file name", base)
	}

	authority, loadErr := dml.LoadCertificateAuthority(pairPaths(dir, caBase))
	if loadErr != nil {
		return fmt.Errorf("loading the CA from %s (run \"dmlcert ca\" first): %w", dir, loadErr)
	}

	issued, issueErr := authority.Issue(spec)
	if issueErr != nil {
		return fmt.Errorf("issuing the certificate: %w", issueErr)
	}

	certPath, keyPath, writeErr := writePair(dir, base, issued.CertPEM, issued.KeyPEM)
	if writeErr != nil {
		return writeErr
	}

	reportWritten(out, certPath, keyPath)
	say(out, "principal: %s\n", issued.Principal())

	return nil
}

func runServer(args []string, out, errOut io.Writer) error {
	flags := newFlagSet("server")
	dir := flags.String("dir", ".", "directory holding the CA, and receiving the certificate")
	name := flags.String("name", "server", "common name, and the file name without extension")
	hosts := flags.String("hosts", "localhost,127.0.0.1", "comma-separated DNS names and IP addresses clients connect to")
	days := flags.Int("days", defaultLeafDays, "validity in days")

	if parseErr := flags.Parse(args); parseErr != nil {
		return handleFlagError(flags, parseErr, errOut)
	}

	validity, daysErr := validityFromDays(*days)
	if daysErr != nil {
		return daysErr
	}

	return issueLeaf(out, *dir, *name, dml.CertificateSpec{
		CommonName: *name,
		Names:      splitList(*hosts),
		Server:     true,
		Validity:   validity,
	})
}

func runClient(args []string, out, errOut io.Writer) error {
	flags := newFlagSet("client")
	dir := flags.String("dir", ".", "directory holding the CA, and receiving the certificate")
	name := flags.String("name", "", "common name, and the file name without extension (required)")
	days := flags.Int("days", defaultLeafDays, "validity in days")

	if parseErr := flags.Parse(args); parseErr != nil {
		return handleFlagError(flags, parseErr, errOut)
	}

	if *name == "" {
		return errors.New("client: -name is required")
	}

	validity, daysErr := validityFromDays(*days)
	if daysErr != nil {
		return daysErr
	}

	return issueLeaf(out, *dir, *name, dml.CertificateSpec{
		CommonName: *name,
		Client:     true,
		Validity:   validity,
	})
}

func runPrincipal(args []string, out, errOut io.Writer) error {
	flags := newFlagSet("principal")

	if parseErr := flags.Parse(args); parseErr != nil {
		return handleFlagError(flags, parseErr, errOut)
	}

	paths := flags.Args()
	if len(paths) == 0 {
		return errors.New("principal: at least one certificate file is required")
	}

	for _, path := range paths {
		principal, principalErr := dml.CertificateFilePrincipal(path)
		if principalErr != nil {
			return fmt.Errorf("principal: %w", principalErr)
		}

		say(out, "%s\t%s\n", principal, path)
	}

	return nil
}