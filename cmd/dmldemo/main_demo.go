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

// Command dmldemo shows the host/client split of dml with the one thing the
// wire protocol offers today: holds on a named resource.
//
//	dmldemo host [-pipe NAME] [-db FILE] [-marker FILE]
//	    owns the store and serves the resource "lamp" on a named pipe. The
//	    effect of "lamp" is a marker file that exists exactly while at least
//	    one client holds the resource.
//	dmldemo hold [-pipe NAME] [-for DURATION]
//	    connects, holds "lamp" (waiting until its effect is in place) and
//	    keeps the hold until Ctrl-C, the end of -for, or the process dying.
//
// Run several holds at once: the marker file is created once and removed
// once, a couple of seconds after the last holder is gone, including when a
// holder is killed instead of letting go.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/signal"
	"time"

	"dml"
)

const (
	resourceName  = "lamp"
	defaultPipe   = "dml-demo"
	defaultDB     = "dmldemo.db"
	defaultMarker = "dmldemo-lamp.on"
	dialTimeout   = 10 * time.Second
)

var errUsage = errors.New("usage: dmldemo host|hold [flags]")

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	err := run(ctx, os.Args[1:], os.Stdout)

	stop()

	if err != nil {
		say(os.Stderr, "dmldemo: %v\n", err)
		os.Exit(1)
	}
}

// say writes formatted text to w. A failed write has nobody to tell.
func say(w io.Writer, format string, args ...any) {
	if _, err := fmt.Fprintf(w, format, args...); err != nil {
		_ = err
	}
}

// joinErrors joins the non-nil errs and wraps the result with op; it is nil
// if every err is nil.
func joinErrors(op string, errs ...error) error {
	if joined := errors.Join(errs...); joined != nil {
		return fmt.Errorf("%s: %w", op, joined)
	}

	return nil
}

func run(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errUsage
	}

	switch args[0] {
	case "host":
		return runHost(ctx, args[1:], out)
	case "hold":
		return runHold(ctx, args[1:], out)
	default:
		return fmt.Errorf("unknown command %q: %w", args[0], errUsage)
	}
}

// markerEffect is an dml.Effect whose "outside world" is one file: present
// means the file exists. Present reports the real state, so the reconciler
// also repairs a marker left behind by a crash or deleted by hand.
type markerEffect struct {
	path string
}

// Compile-time assertion that markerEffect satisfies dml.Effect.
var _ dml.Effect = markerEffect{}

func (e markerEffect) Present(context.Context) (bool, error) {
	_, err := os.Stat(e.path)

	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	default:
		return false, fmt.Errorf("checking %s: %w", e.path, err)
	}
}

func (e markerEffect) Apply(context.Context) error {
	if err := os.WriteFile(e.path, []byte("on\n"), 0o600); err != nil {
		return fmt.Errorf("creating %s: %w", e.path, err)
	}

	return nil
}

func (e markerEffect) Remove(context.Context) error {
	if err := os.Remove(e.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("removing %s: %w", e.path, err)
	}

	return nil
}

func runHost(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("host", flag.ContinueOnError)
	pipe := flags.String("pipe", defaultPipe, "name of the named pipe to serve on")
	db := flags.String("db", defaultDB, "the host's bbolt store")
	marker := flags.String("marker", defaultMarker, "the marker file that is the effect of the resource")

	if parseErr := flags.Parse(args); parseErr != nil {
		return fmt.Errorf("host: %w", parseErr)
	}

	host, openErr := dml.OpenHost(dml.HostConfig{Path: *db})
	if openErr != nil {
		return fmt.Errorf("host: opening the host: %w", openErr)
	}

	if registerErr := host.RegisterResource(resourceName, markerEffect{path: *marker}); registerErr != nil {
		return joinErrors("host", registerErr, host.Close())
	}

	// serveHost runs first, then the host is closed: the server must stop
	// before the host does.
	return joinErrors("host", serveHost(ctx, host, *pipe, out), host.Close())
}

// serveHost serves host on the named pipe until ctx ends or Serve gives up,
// then closes the server (which ends every client connection).
func serveHost(ctx context.Context, host *dml.Host, pipe string, out io.Writer) error {
	listener, listenErr := dml.ListenPipe(dml.PipePath(pipe), "")
	if listenErr != nil {
		return fmt.Errorf("listening on pipe %q: %w", pipe, listenErr)
	}

	// No Authorizer: every client that can open the pipe (by default only
	// the current user and SYSTEM) may use every resource. See WithAuthorizer
	// and WithPeerIdentifier(dml.PipePeerPrincipal) for per-resource rules.
	server := dml.NewWireServer(host)
	served := make(chan error, 1)

	go func() { served <- server.Serve(listener) }()

	say(out, "host: serving resource %q on %s; Ctrl-C stops it\n", resourceName, dml.PipePath(pipe))

	var serveErr error

	received := false

	select {
	case <-ctx.Done():
	case serveErr = <-served:
		received = true
	}

	closeErr := server.Close()

	if !received {
		serveErr = <-served
	}

	return joinErrors("serving", serveErr, closeErr)
}

func runHold(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("hold", flag.ContinueOnError)
	pipe := flags.String("pipe", defaultPipe, "name of the named pipe of the host")
	holdFor := flags.Duration("for", 0, "how long to hold the resource (0: until Ctrl-C)")

	if parseErr := flags.Parse(args); parseErr != nil {
		return fmt.Errorf("hold: %w", parseErr)
	}

	dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()

	client, dialErr := dml.DialPipe(dialCtx, dml.PipePath(*pipe))
	if dialErr != nil {
		return fmt.Errorf("hold: connecting: %w", dialErr)
	}

	// Returns once the host has confirmed the effect is in place.
	if acquireErr := dml.AcquireAndWait(dialCtx, client, resourceName); acquireErr != nil {
		return joinErrors("hold", acquireErr, client.Close())
	}

	say(out, "hold: %q is held and in effect\n", resourceName)

	// A nil channel blocks forever, so without -for only Ctrl-C ends this.
	var expired <-chan time.Time

	if *holdFor > 0 {
		timer := time.NewTimer(*holdFor)
		defer timer.Stop()

		expired = timer.C
	}

	select {
	case <-ctx.Done():
	case <-expired:
	}

	// Close releases every hold before it returns.
	say(out, "hold: letting go\n")

	return joinErrors("hold", client.Close())
}