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

//go:build windows

package dml

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestDefaultPipeSecurityNamesTheCurrentUserAndDeniesNetworkLogons(t *testing.T) {
	sddl, err := DefaultPipeSecurity()
	if err != nil {
		t.Fatalf("DefaultPipeSecurity(): %v", err)
	}

	if !strings.HasPrefix(sddl, "D:P(D;;GA;;;NU)(A;;GA;;;S-1-") || !strings.HasSuffix(sddl, ")(A;;GA;;;SY)") {
		t.Fatalf("DefaultPipeSecurity() = %q, want a protected DACL denying NU, then allowing the user's SID and SY", sddl)
	}
}

func TestPipeEndToEnd(t *testing.T) {
	ctx := hostTestContext(t)
	h := openTestHost(t, boltTestPath(t), nil)
	fw := &fakeEffect{}
	registerTestResource(t, h, "example-resource", fw)

	server := NewWireServer(h)
	path := PipePath(fmt.Sprintf("dml-test-%d", time.Now().UnixNano()))

	listener, err := ListenPipe(path, "")
	if err != nil {
		t.Fatalf("ListenPipe(): %v", err)
	}

	served := make(chan error, 1)

	go func() { served <- server.Serve(listener) }()

	t.Cleanup(func() {
		if closeErr := server.Close(); closeErr != nil {
			t.Errorf("server Close(): %v", closeErr)
		}

		if serveErr := <-served; serveErr != nil && !errors.Is(serveErr, ErrWireServerClosed) {
			t.Errorf("Serve(): %v", serveErr)
		}
	})

	client, err := DialPipe(ctx, path)
	if err != nil {
		t.Fatalf("DialPipe(): %v", err)
	}

	t.Cleanup(func() {
		if closeErr := client.Close(); closeErr != nil {
			t.Errorf("client Close(): %v", closeErr)
		}
	})

	if acquireErr := AcquireAndWait(ctx, client, "example-resource"); acquireErr != nil {
		t.Fatalf("AcquireAndWait(): %v", acquireErr)
	}

	requireEffectCounts(t, fw, 1, 0, true)

	if last, releaseErr := client.Release(ctx, "example-resource"); releaseErr != nil || !last {
		t.Fatalf("Release() = (%v,%v), want last=true", last, releaseErr)
	}

	eventually(t, "the effect to be removed", func() bool {
		_, _, present := fw.counts()
		return !present
	})
}
