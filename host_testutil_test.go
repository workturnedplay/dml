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
	"errors"
	"slices"
	"sync"
	"testing"
	"time"
)

// Host (theorystate.md section 112). The driving scenario is one external
// effect that many processes want at once; fakeEffect stands in for it,
// and no production Effect lives in dml.

var errFakeEffect = errors.New("fake effect failure")

// fakeEffect is an in-memory Effect that records how it was driven.
type fakeEffect struct {
	mu            sync.Mutex
	present       bool
	presents      int
	applies       int
	removes       int
	applyFailures int
}

func (f *fakeEffect) Present(_ context.Context) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.presents++

	return f.present, nil
}

func (f *fakeEffect) Apply(_ context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.applies++

	if f.applyFailures > 0 {
		f.applyFailures--
		return errFakeEffect
	}

	f.present = true

	return nil
}

func (f *fakeEffect) Remove(_ context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.removes++
	f.present = false

	return nil
}

func (f *fakeEffect) counts() (applies, removes int, present bool) {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.applies, f.removes, f.present
}

// presentCalls is how often the reconciler asked this effect whether it is
// in place, which is how a test sees whether a resource was looked at.
func (f *fakeEffect) presentCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.presents
}

// eventually polls cond until it holds or a generous deadline passes.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)

	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}

		time.Sleep(5 * time.Millisecond)
	}
}

func hostTestContext(t *testing.T) context.Context {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)

	return ctx
}

// openTestHost opens a Host with short intervals and closes it when the
// test ends. KeepaliveTTL is disabled unless mutate sets it.
func openTestHost(t *testing.T, path string, mutate func(cfg *HostConfig)) *Host {
	t.Helper()

	cfg := HostConfig{
		Path:           path,
		KeepaliveTTL:   -1,
		TeardownGrace:  60 * time.Millisecond,
		ReapInterval:   10 * time.Millisecond,
		ResyncInterval: 50 * time.Millisecond,
		RetryInterval:  10 * time.Millisecond,
		OnError:        func(string, error) {},
	}

	if mutate != nil {
		mutate(&cfg)
	}

	h, err := OpenHost(cfg)
	if err != nil {
		t.Fatalf("OpenHost(): %v", err)
	}

	t.Cleanup(func() {
		if closeErr := h.Close(); closeErr != nil {
			t.Errorf("Close(): %v", closeErr)
		}
	})

	return h
}

func registerTestResource(t *testing.T, h *Host, name string, effect Effect) {
	t.Helper()

	if err := h.RegisterResource(name, effect); err != nil {
		t.Fatalf("RegisterResource(%q): %v", name, err)
	}
}

func connectTest(t *testing.T, h *Host) *Conn {
	t.Helper()

	c, err := h.Connect(context.Background())
	if err != nil {
		t.Fatalf("Connect(): %v", err)
	}

	return c
}

func requireEffectCounts(t *testing.T, fw *fakeEffect, wantApplies, wantRemoves int, wantPresent bool) {
	t.Helper()

	applies, removes, present := fw.counts()
	if applies != wantApplies || removes != wantRemoves || present != wantPresent {
		t.Fatalf("effect applies=%d removes=%d present=%v, want %d,%d,%v", applies, removes, present, wantApplies, wantRemoves, wantPresent)
	}
}

// errorLog collects what the Host reports through HostConfig.OnError.
type errorLog struct {
	mu   sync.Mutex
	ops  []string
	errs []error
}

func (l *errorLog) add(op string, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.ops = append(l.ops, op)
	l.errs = append(l.errs, err)
}

// hasOp reports whether anything was reported under op.
func (l *errorLog) hasOp(op string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	return slices.Contains(l.ops, op)
}

// count is how many errors were reported in all.
func (l *errorLog) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()

	return len(l.errs)
}

func (l *errorLog) has(target error) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	for _, err := range l.errs {
		if errors.Is(err, target) {
			return true
		}
	}

	return false
}
