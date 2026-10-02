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
	"errors"
	"net"
	"testing"
)

// Tests in this file open real TCP sockets on 127.0.0.1, which a
// block-by-default firewall (Portmaster) refuses unless the test binary has an
// exception. They are compiled only with -tags portmasterFirewalled (see
// test.bat), into one stable binary, so a single permanent rule covers them.
// Everything else in the wire tests runs over net.Pipe and needs nothing.

// TestFWNeededWireOverLoopbackTCP runs the whole client/host flow over a real
// loopback TCP connection.
func TestFWNeededWireOverLoopbackTCP(t *testing.T) {
	ctx := hostTestContext(t)
	h := openTestHost(t, boltTestPath(t), nil)
	fw := &fakeEffect{}
	registerTestResource(t, h, "dns-out", fw)

	server := NewWireServer(h)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen(): %v", err)
	}

	served := make(chan error, 1)

	go func() { served <- server.Serve(listener) }()

	// Registered after the host's cleanup, so it runs before it.
	t.Cleanup(func() {
		if closeErr := server.Close(); closeErr != nil {
			t.Errorf("server Close(): %v", closeErr)
		}

		if serveErr := <-served; serveErr != nil && !errors.Is(serveErr, ErrWireServerClosed) {
			t.Errorf("Serve(): %v", serveErr)
		}
	})

	var dialer net.Dialer

	nc, dialErr := dialer.DialContext(ctx, "tcp", listener.Addr().String())
	if dialErr != nil {
		t.Fatalf("DialContext(): %v", dialErr)
	}

	client := startWireClient(t, nc)

	if acquireErr := AcquireAndWait(ctx, client, "dns-out"); acquireErr != nil {
		t.Fatalf("AcquireAndWait(): %v", acquireErr)
	}

	requireEffectCounts(t, fw, 1, 0, true)

	if last, releaseErr := client.Release(ctx, "dns-out"); releaseErr != nil || !last {
		t.Fatalf("Release() = (%v,%v), want last=true", last, releaseErr)
	}

	eventually(t, "the effect to be removed", func() bool {
		_, _, present := fw.counts()
		return !present
	})
}