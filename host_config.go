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
	ilog "log"
	"time"
)

// Defaults for HostConfig fields left at zero.
const (
	defaultKeepaliveTTL    = 15 * time.Second
	defaultTeardownGrace   = 2 * time.Second
	defaultReapInterval    = time.Second
	defaultResyncInterval  = 30 * time.Second
	defaultRetryInterval   = 2 * time.Second
	defaultShutdownTimeout = 10 * time.Second
	defaultEffectTimeout   = 30 * time.Second
)

// HostConfig configures a Host. Only Path is required.
//
// For KeepaliveTTL, TeardownGrace and EffectTimeout, zero means "use the
// default" and a negative value means "disabled". The other durations must
// be positive;
// zero or negative means the default.
type HostConfig struct {
	// Path is the bbolt file. Exactly one Host may own it.
	Path string

	// KeepaliveTTL is how long a connection may stay silent before the Host
	// treats it as dead. Connection drop is the primary liveness signal;
	// this is the backstop for partitions where no drop is ever seen. The
	// clock lives in host memory only. Any operation counts as a sign of
	// life, as does Conn.Keepalive. A client that waits for longer than the
	// TTL inside WaitApplied must keep calling Keepalive from elsewhere.
	KeepaliveTTL time.Duration

	// TeardownGrace is how long a resource must stay unheld before its
	// effect is removed, so that release-then-acquire races do not flap the
	// effect. Applying is never delayed.
	TeardownGrace time.Duration

	// ReapInterval is how often expired connections are collected and failed
	// session closes are retried.
	ReapInterval time.Duration

	// ResyncInterval is the longest the reconciler sleeps: every resource is
	// re-compared with the outside world at least this often, which repairs
	// drift.
	ResyncInterval time.Duration

	// RetryInterval is how soon a failed effect is retried.
	RetryInterval time.Duration

	// ShutdownTimeout bounds the final reconcile pass in Close.
	ShutdownTimeout time.Duration

	// EffectTimeout bounds each Effect.Present, Apply and Remove call. A call
	// that does not return in time is abandoned and reported as
	// ErrEffectTimeout, so one hung effect cannot stall the reconciler (which
	// serves every resource) or Close. An abandoned call cannot be killed if
	// it ignores its context, so no further call to that effect starts until
	// it returns (ErrEffectBusy). Disabled (negative), calls are made
	// directly, and Close can then wait for an effect that ignores its
	// context.
	EffectTimeout time.Duration

	// VerifyPageSize is the page size of the startup VerifyAll sweep (zero
	// means VerifyAll's own default).
	VerifyPageSize int

	// OnError receives errors from background work (effects that failed,
	// sessions that could not be closed). Nil means log.Printf.
	OnError func(op string, err error)
}

// durationOrDefault implements the "zero is default, negative is disabled"
// rule: it returns the default for zero, 0 (disabled) for a negative value,
// and v otherwise.
func durationOrDefault(v, def time.Duration) time.Duration {
	switch {
	case v == 0:
		return def
	case v < 0:
		return 0
	default:
		return v
	}
}

// positiveOrDefault returns v if it is positive and def otherwise.
func positiveOrDefault(v, def time.Duration) time.Duration {
	if v <= 0 {
		return def
	}

	return v
}

func defaultHostOnError(op string, err error) {
	ilog.Printf("dml host: %s: %v", op, err)
}

// withDefaults returns c with every unset field replaced by its default.
// After this, KeepaliveTTL and TeardownGrace of 0 mean "disabled".
func (c HostConfig) withDefaults() HostConfig {
	c.KeepaliveTTL = durationOrDefault(c.KeepaliveTTL, defaultKeepaliveTTL)
	c.TeardownGrace = durationOrDefault(c.TeardownGrace, defaultTeardownGrace)
	c.EffectTimeout = durationOrDefault(c.EffectTimeout, defaultEffectTimeout)
	c.ReapInterval = positiveOrDefault(c.ReapInterval, defaultReapInterval)
	c.ResyncInterval = positiveOrDefault(c.ResyncInterval, defaultResyncInterval)
	c.RetryInterval = positiveOrDefault(c.RetryInterval, defaultRetryInterval)
	c.ShutdownTimeout = positiveOrDefault(c.ShutdownTimeout, defaultShutdownTimeout)

	if c.OnError == nil {
		c.OnError = defaultHostOnError
	}

	return c
}
