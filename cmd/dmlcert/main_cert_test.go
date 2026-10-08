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

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dml"
)

// runCommand runs dmlcert with args and returns what it printed.
func runCommand(args ...string) (stdout, stderr string, err error) {
	var out, errOut bytes.Buffer

	err = run(args, &out, &errOut)

	return out.String(), errOut.String(), err
}

func requireFiles(t *testing.T, dir string, names ...string) {
	t.Helper()

	for _, name := range names {
		path := filepath.Join(dir, name)

		if _, statErr := os.Stat(path); statErr != nil {
			t.Fatalf("expected %s to exist: %v", path, statErr)
		}
	}
}

func TestRunMakesACAServerAndClientThatLoad(t *testing.T) {
	dir := t.TempDir()

	if _, _, caErr := runCommand("ca", "-dir", dir); caErr != nil {
		t.Fatalf("ca: %v", caErr)
	}

	if _, _, serverErr := runCommand("server", "-dir", dir, "-name", "srv", "-hosts", "localhost,127.0.0.1"); serverErr != nil {
		t.Fatalf("server: %v", serverErr)
	}

	stdout, _, clientErr := runCommand("client", "-dir", dir, "-name", "alice")
	if clientErr != nil {
		t.Fatalf("client: %v", clientErr)
	}

	requireFiles(t, dir, "ca.pem", "ca-key.pem", "srv.pem", "srv-key.pem", "alice.pem", "alice-key.pem")

	want, principalErr := dml.CertificateFilePrincipal(filepath.Join(dir, "alice.pem"))
	if principalErr != nil {
		t.Fatalf("CertificateFilePrincipal(): %v", principalErr)
	}

	if !strings.Contains(stdout, string(want)) {
		t.Fatalf("client output %q does not contain the principal %q", stdout, want)
	}

	caFile := filepath.Join(dir, "ca.pem")

	if _, loadErr := dml.LoadServerTLS(filepath.Join(dir, "srv.pem"), filepath.Join(dir, "srv-key.pem"), caFile); loadErr != nil {
		t.Fatalf("LoadServerTLS() on the generated files: %v", loadErr)
	}

	if _, loadErr := dml.LoadClientTLS(filepath.Join(dir, "alice.pem"), filepath.Join(dir, "alice-key.pem"), caFile, "localhost"); loadErr != nil {
		t.Fatalf("LoadClientTLS() on the generated files: %v", loadErr)
	}
}

func TestRunNeverOverwritesAnExistingFile(t *testing.T) {
	dir := t.TempDir()
	keyFile := filepath.Join(dir, "ca-key.pem")

	if _, _, firstErr := runCommand("ca", "-dir", dir); firstErr != nil {
		t.Fatalf("first ca: %v", firstErr)
	}

	before, readErr := os.ReadFile(keyFile)
	if readErr != nil {
		t.Fatalf("reading the CA key: %v", readErr)
	}

	_, _, secondErr := runCommand("ca", "-dir", dir)
	if secondErr == nil || !strings.Contains(secondErr.Error(), "already exists") {
		t.Fatalf("second ca error = %v, want a refusal to overwrite", secondErr)
	}

	after, readAgainErr := os.ReadFile(keyFile)
	if readAgainErr != nil {
		t.Fatalf("reading the CA key again: %v", readAgainErr)
	}

	if !bytes.Equal(before, after) {
		t.Fatal("the CA key was changed by a refused ca command")
	}

	if _, _, clientErr := runCommand("client", "-dir", dir, "-name", "bob"); clientErr != nil {
		t.Fatalf("client bob: %v", clientErr)
	}

	if _, _, repeatErr := runCommand("client", "-dir", dir, "-name", "bob"); repeatErr == nil {
		t.Fatal("a second client bob was issued over the first")
	}
}

func TestRunRejectsBadInput(t *testing.T) {
	dir := t.TempDir()

	if _, _, caErr := runCommand("ca", "-dir", dir); caErr != nil {
		t.Fatalf("ca: %v", caErr)
	}

	cases := []struct {
		name string
		args []string
	}{
		{name: "no command", args: nil},
		{name: "unknown command", args: []string{"bogus"}},
		{name: "unknown flag", args: []string{"ca", "-bogus"}},
		{name: "zero days", args: []string{"ca", "-dir", filepath.Join(dir, "fresh"), "-days", "0"}},
		{name: "client without a name", args: []string{"client", "-dir", dir}},
		{name: "client name with a directory", args: []string{"client", "-dir", dir, "-name", "../evil"}},
		{name: "client name with a backslash", args: []string{"client", "-dir", dir, "-name", `..\evil`}},
		{name: "client name that is a Windows device", args: []string{"client", "-dir", dir, "-name", "con"}},
		{name: "client name that is a Windows device with an extension", args: []string{"client", "-dir", dir, "-name", "NUL.txt"}},
		{name: "client name with a character Windows forbids", args: []string{"client", "-dir", dir, "-name", "a|b"}},
		{name: "server without a CA", args: []string{"server", "-dir", filepath.Join(dir, "no-ca")}},
		{name: "server without hosts", args: []string{"server", "-dir", dir, "-hosts", " , "}},
		{name: "principal without files", args: []string{"principal"}},
		{name: "principal of a missing file", args: []string{"principal", filepath.Join(dir, "missing.pem")}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := runCommand(tc.args...); err == nil {
				t.Fatalf("run(%v) succeeded, want an error", tc.args)
			}
		})
	}
}

func TestIsSafeFileName(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{name: "alice", want: true},
		{name: "host-1", want: true},
		{name: "my cert", want: true},
		{name: "a.b", want: true},
		{name: "console", want: true},
		{name: "com1x", want: true},
		{name: "com10", want: true},
		{name: "com0", want: true},
		{name: "", want: false},
		{name: ".", want: false},
		{name: "..", want: false},
		{name: "a/b", want: false},
		{name: `a\b`, want: false},
		{name: "c:x", want: false},
		{name: "a<b", want: false},
		{name: "a>b", want: false},
		{name: `a"b`, want: false},
		{name: "a|b", want: false},
		{name: "a?b", want: false},
		{name: "a*b", want: false},
		{name: "a\x00b", want: false},
		{name: "a\tb", want: false},
		{name: "con", want: false},
		{name: "CON", want: false},
		{name: "con ", want: false},
		{name: "Nul.txt", want: false},
		{name: "aux", want: false},
		{name: "prn", want: false},
		{name: "com1", want: false},
		{name: "LPT9", want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isSafeFileName(tc.name); got != tc.want {
				t.Fatalf("isSafeFileName(%q) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

func TestRunPrincipalPrintsTheFingerprint(t *testing.T) {
	dir := t.TempDir()

	if _, _, caErr := runCommand("ca", "-dir", dir); caErr != nil {
		t.Fatalf("ca: %v", caErr)
	}

	caFile := filepath.Join(dir, "ca.pem")

	stdout, _, principalErr := runCommand("principal", caFile)
	if principalErr != nil {
		t.Fatalf("principal: %v", principalErr)
	}

	if !strings.HasPrefix(stdout, "tls-sha256:") || !strings.Contains(stdout, "\t"+caFile) {
		t.Fatalf("principal output = %q, want a tls-sha256 fingerprint and the file name", stdout)
	}
}

func TestRunHelpSucceeds(t *testing.T) {
	stdout, _, helpErr := runCommand("help")
	if helpErr != nil || !strings.Contains(stdout, "usage:") {
		t.Fatalf("help = (%q,%v), want the usage text and no error", stdout, helpErr)
	}

	_, stderr, flagHelpErr := runCommand("ca", "-h")
	if flagHelpErr != nil || !strings.Contains(stderr, "-dir") {
		t.Fatalf("ca -h = (%q,%v), want the flags and no error", stderr, flagHelpErr)
	}
}
