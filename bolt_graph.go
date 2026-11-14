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
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	bolt "go.etcd.io/bbolt"
	berrors "go.etcd.io/bbolt/errors"
)

// BoltGraph is the disk-backed GraphAPI (theorystate.md sections 100-108):
// nodes, relationships and the ID counter live in one bbolt file, laid out
// as described at boltBucketNodes. Only one process may open the file
// (bbolt takes a file lock), which is the graph-host model of section 107.
//
// A bare BoltGraph supports one goroutine at a time (concurrentAccessGuard
// panics on overlap, exactly like *Graph); wrap it in a GraphActor for
// concurrent callers, which is also what serializes reads and writes so a
// read transaction is never opened while a write transaction is open on
// the same goroutine (bbolt's documented deadlock). Reads outside a
// Transact each run in their own short read transaction; inside a Transact
// every read goes through the update transaction.
//
// Durability: Transact is one bbolt update transaction, so each committed
// Transact is durable and an aborted or crashed one leaves no trace.
// OnCommit hooks run only after that commit has succeeded, and OnRollback
// hooks run on any abort, including a failed commit.
//
// NodeExists, HasRelationship, FindRelationships, and FindNodes return
// ErrGraphStoreUnavailable (wrapping the underlying failure) if the store
// itself fails, rather than panicking, per GraphReader's own doc comment.
// Paged reads remain the separate, still-open rework of theorystate.md
// section 105.
type BoltGraph struct {
	guard    concurrentAccessGuard
	db       *bolt.DB
	checkers []Checker
}

// Compile-time assertion that *BoltGraph satisfies GraphAPI.
var _ GraphAPI = (*BoltGraph)(nil)

// boltOpenTimeout is how long bolt waits for the file lock. A var, not a
// const, so tests can shrink it.
var boltOpenTimeout = time.Second

// boltOpenGuardFactor times boltOpenTimeout is how long openBoltDB waits for
// bolt.Open before giving up on it.
const boltOpenGuardFactor = 5

// boltOpenResult is what the goroutine running bolt.Open reports.
type boltOpenResult struct {
	db  *bolt.DB
	err error
}

// finishBoltOpen turns bolt.Open's outcome into this package's errors: a
// lock timeout becomes ErrStoreLocked.
func finishBoltOpen(path string, res boltOpenResult) (*bolt.DB, error) {
	switch {
	case res.err == nil:
		return res.db, nil
	case errors.Is(res.err, berrors.ErrTimeout):
		return nil, fmt.Errorf("%w: %s: %w", ErrStoreLocked, path, res.err)
	default:
		return nil, fmt.Errorf("bolt: opening %s: %w", path, res.err)
	}
}

// openBoltDB opens the bolt file at path, failing fast with ErrStoreLocked
// if it is already open. bbolt documents its Timeout option for Darwin and
// Linux only, so on Windows a second open of a locked file might block
// forever. bolt.Open therefore runs in its own goroutine and openBoltDB
// gives up after boltOpenGuardFactor times the timeout; if that abandoned
// open ever succeeds later, the late handle is closed at once so it cannot
// keep the file locked. An OS-held lock needs no cleanup after a crash,
// unlike a lock file (theorystate.md section 112).
func openBoltDB(path string) (*bolt.DB, error) {
	timeout := boltOpenTimeout
	if timeout <= 0 {
		timeout = time.Second
	}

	results := make(chan boltOpenResult, 1)

	go func() {
		db, openErr := bolt.Open(path, 0o600, &bolt.Options{Timeout: timeout})
		results <- boltOpenResult{db: db, err: openErr}
	}()

	guard := time.NewTimer(boltOpenGuardFactor * timeout)
	defer guard.Stop()

	select {
	case res := <-results:
		return finishBoltOpen(path, res)
	case <-guard.C:
		go func() {
			if late := <-results; late.db != nil {
				if closeErr := late.db.Close(); closeErr != nil {
					_ = closeErr
				}
			}
		}()

		return nil, fmt.Errorf("%w: %s did not open within %v", ErrStoreLocked, path, boltOpenGuardFactor*timeout)
	}
}

// encodeBoltFormat encodes a format version as the 8-byte record.
func encodeBoltFormat(version uint64) []byte {
	raw := make([]byte, boltIDSize)
	binary.BigEndian.PutUint64(raw, version)

	return raw
}

// readBoltFormat returns the stored format version; present is false if the
// meta bucket has no record. A record of the wrong size is ErrStoreFormat.
func readBoltFormat(meta *bolt.Bucket) (version uint64, present bool, err error) {
	raw := meta.Get(boltFormatKey)
	if raw == nil {
		return 0, false, nil
	}

	if len(raw) != boltIDSize {
		return 0, false, fmt.Errorf("%w: format record of %d bytes, want %d", ErrStoreFormat, len(raw), boltIDSize)
	}

	return binary.BigEndian.Uint64(raw), true, nil
}

// requireBoltFormat accepts only the version this build reads.
func requireBoltFormat(version uint64) error {
	if version != boltFormatVersion {
		return fmt.Errorf("%w: the store is version %d, this build reads version %d", ErrStoreFormat, version, boltFormatVersion)
	}

	return nil
}

// ensureBoltFormat checks the store's format record, writing it if there is
// none (a new store, or a store from before the record existed, which has
// exactly this layout), and fails loudly on any other version.
func ensureBoltFormat(meta *bolt.Bucket) error {
	version, present, err := readBoltFormat(meta)
	if err != nil {
		return err
	}

	if present {
		return requireBoltFormat(version)
	}

	return wrapBoltErr("write format version", meta.Put(boltFormatKey, encodeBoltFormat(boltFormatVersion)))
}

// OpenBoltGraph opens, creating if necessary, the store at path. It fails
// with ErrStoreLocked if the file is already open (see openBoltDB; the graph
// host must not be started twice, theorystate.md section 107) and with
// ErrStoreFormat if the store's format version is not the one this build
// reads (see ensureBoltFormat).
func OpenBoltGraph(path string) (*BoltGraph, error) {
	db, err := openBoltDB(path)
	if err != nil {
		return nil, err
	}

	initErr := db.Update(func(btx *bolt.Tx) error {
		for _, name := range [][]byte{boltBucketNodes, boltBucketOut, boltBucketIn, boltBucketMeta, boltBucketNames} {
			if _, bucketErr := btx.CreateBucketIfNotExists(name); bucketErr != nil {
				return wrapBoltErr("create bucket "+string(name), bucketErr)
			}
		}

		return ensureBoltFormat(btx.Bucket(boltBucketMeta))
	})
	if initErr != nil {
		if closeErr := db.Close(); closeErr != nil {
			return nil, fmt.Errorf("bolt: initializing %s: %w (closing also failed: %w)", path, initErr, closeErr)
		}

		return nil, fmt.Errorf("bolt: initializing %s: %w", path, initErr)
	}

	return &BoltGraph{db: db}, nil
}

// Close closes the store. It is idempotent.
func (g *BoltGraph) Close() error {
	release := g.guard.acquire()
	defer release()

	return wrapBoltErr("close", g.db.Close())
}

// CheckStore is the physical half of the startup integrity check
// (theorystate.md section 104): bolt's own page-level consistency check,
// followed by BoltGraph's layout checks (see boltView.checkLayout). It
// returns ErrStoreCorrupt, wrapping the first problem found and counting the
// rest, and fixes nothing. It runs directly against the store, so call it
// right after OpenBoltGraph, before the graph is handed to a GraphActor;
// bolt documents that its checker must not run alongside other writers.
func (g *BoltGraph) CheckStore() error {
	release := g.guard.acquire()
	defer release()

	err := g.db.View(func(btx *bolt.Tx) error {
		var first error

		extra := 0

		// The channel must be drained completely: bolt's checker runs in
		// its own goroutine.
		for checkErr := range btx.Check() {
			if first == nil {
				first = checkErr
				continue
			}

			extra++
		}

		if first != nil {
			return fmt.Errorf("%w: %w (and %d more problems)", ErrStoreCorrupt, first, extra)
		}

		txn, openErr := newBoltTxn(btx)
		if openErr != nil {
			return openErr
		}

		if formatErr := txn.checkFormat(); formatErr != nil {
			return formatErr
		}

		next, exhausted, counterErr := txn.counter()
		if counterErr != nil {
			return counterErr
		}

		return txn.checkLayout(next, exhausted)
	})

	return wrapBoltErr("check store", err)
}

// boltRead runs fn against a read-only view of g's store, in its own
// short bolt read transaction, and returns its result. The caller must
// hold g's guard.
func boltRead[T any](g *BoltGraph, fn func(v boltView) (T, error)) (T, error) {
	var (
		result T
		fnErr  error
	)

	viewErr := g.db.View(func(btx *bolt.Tx) error {
		view, openErr := newBoltView(btx)
		if openErr != nil {
			fnErr = openErr
			return openErr
		}

		result, fnErr = fn(view)

		return fnErr
	})

	var zero T

	switch {
	case fnErr != nil:
		// The request itself failed (ErrNodeNotFound, ErrStoreCorrupt, ...).
		return zero, wrapInterfaceErr(fnErr)
	case viewErr != nil:
		// fn never failed, so bolt's own read channel did: a closed
		// database, an I/O error beginning or ending the transaction.
		return zero, fmt.Errorf("%w: %w", ErrGraphStoreUnavailable, viewErr)
	default:
		return result, nil
	}
}

// NodeExists reports whether id exists.
func (g *BoltGraph) NodeExists(id NodeID) (bool, error) {
	release := g.guard.acquire()
	defer release()

	return boltRead(g, func(v boltView) (bool, error) { return v.NodeExists(id) })
}

// HasRelationship reports whether (a, b) exists.
func (g *BoltGraph) HasRelationship(a, b NodeID) (bool, error) {
	release := g.guard.acquire()
	defer release()

	return boltRead(g, func(v boltView) (bool, error) { return v.HasRelationship(a, b) })
}

// FindRelationship reports whether the exact relationship exists.
func (g *BoltGraph) FindRelationship(from, to NodeID) (Relationship, bool, error) {
	release := g.guard.acquire()
	defer release()

	type found struct {
		relationship Relationship
		exists       bool
	}

	result, err := boltRead(g, func(v boltView) (found, error) {
		relationship, exists, findErr := v.FindRelationship(from, to)
		return found{relationship: relationship, exists: exists}, findErr
	})

	return result.relationship, result.exists, err
}

// FindOutgoing returns every relationship whose source is from.
func (g *BoltGraph) FindOutgoing(from NodeID) ([]Relationship, error) {
	release := g.guard.acquire()
	defer release()

	return boltRead(g, func(v boltView) ([]Relationship, error) { return v.FindOutgoing(from) })
}

// FindIncoming returns every relationship whose target is to.
func (g *BoltGraph) FindIncoming(to NodeID) ([]Relationship, error) {
	release := g.guard.acquire()
	defer release()

	return boltRead(g, func(v boltView) ([]Relationship, error) { return v.FindIncoming(to) })
}

// FindRelationships returns every relationship. O(graph).
func (g *BoltGraph) FindRelationships() ([]Relationship, error) {
	release := g.guard.acquire()
	defer release()

	return boltRead(g, func(v boltView) ([]Relationship, error) { return v.FindRelationships() })
}

// FindNodes returns every existing NodeID. O(graph).
func (g *BoltGraph) FindNodes() ([]NodeID, error) {
	release := g.guard.acquire()
	defer release()

	return boltRead(g, func(v boltView) ([]NodeID, error) { return v.FindNodes() })
}

// findNodesAfter, findOutgoingAfter and findIncomingAfter let BoltGraph
// itself, not only a transaction over it, page natively (see nodePager,
// outgoingPager, incomingPager): each is one short read transaction
// returning one bounded page. This is what makes paged reads effective
// for a caller that holds the graph outside a Transact, including through
// a RootGraph or GraphActor forwarding to it.
func (g *BoltGraph) findNodesAfter(after NodeID, hasAfter bool, limit int) ([]NodeID, error) {
	release := g.guard.acquire()
	defer release()

	return boltRead(g, func(v boltView) ([]NodeID, error) { return v.findNodesAfter(after, hasAfter, limit) })
}

func (g *BoltGraph) findOutgoingAfter(from, after NodeID, hasAfter bool, limit int) ([]Relationship, error) {
	release := g.guard.acquire()
	defer release()

	return boltRead(g, func(v boltView) ([]Relationship, error) {
		return v.findOutgoingAfter(from, after, hasAfter, limit)
	})
}

func (g *BoltGraph) findIncomingAfter(to, after NodeID, hasAfter bool, limit int) ([]Relationship, error) {
	release := g.guard.acquire()
	defer release()

	return boltRead(g, func(v boltView) ([]Relationship, error) {
		return v.findIncomingAfter(to, after, hasAfter, limit)
	})
}

// findCommonChildren is one short read transaction (see commonChildrenFinder).
func (g *BoltGraph) findCommonChildren(a, b NodeID, limit int) ([]NodeID, error) {
	release := g.guard.acquire()
	defer release()

	return boltRead(g, func(v boltView) ([]NodeID, error) { return v.findCommonChildren(a, b, limit) })
}

// view runs fn in one short bolt read transaction (see readViewer). fn must
// read only through the reader it receives: calling a method of g itself
// would trip the concurrent-access guard.
func (g *BoltGraph) view(fn func(reader GraphReader) error) error {
	release := g.guard.acquire()
	defer release()

	_, err := boltRead(g, func(v boltView) (struct{}, error) { return struct{}{}, fn(v) })

	return err
}

// Compile-time assertions that BoltGraph pages and gives read views.
var _ readViewer = (*BoltGraph)(nil)

// Compile-time assertions that BoltGraph pages.
var (
	_ nodePager     = (*BoltGraph)(nil)
	_ outgoingPager = (*BoltGraph)(nil)
	_ incomingPager = (*BoltGraph)(nil)
)

// RegisterChecker registers c, exactly like Graph.RegisterChecker.
func (g *BoltGraph) RegisterChecker(c Checker) {
	release := g.guard.acquire()
	defer release()

	g.checkers = append(g.checkers, c)
}

// Transact runs fn as one bbolt update transaction. Every read fn makes
// goes through tx, which sees fn's own writes. If fn returns an error or
// panics, or a relevant Checker declines the resulting state, or a nested
// rollback could not be restored, the update is abandoned: bolt discards
// every write and only the OnRollback hooks run. Otherwise the update
// commits durably and only then do the OnCommit hooks run, still inside
// this call and under the guard, so they are serialized with every other
// closure. If the commit itself fails, the OnRollback hooks run instead.
func (g *BoltGraph) Transact(fn func(tx Tx) error) error {
	release := g.guard.acquire()
	defer release()

	var txn *boltTxn

	err := g.db.Update(func(btx *bolt.Tx) error {
		var openErr error

		txn, openErr = newBoltTxn(btx)
		if openErr != nil {
			return openErr
		}

		defer func() {
			if r := recover(); r != nil {
				txn.abort()
				panic(r)
			}
		}()

		if fnErr := fn(txn); fnErr != nil {
			txn.abort()
			return fnErr
		}

		if checkErr := runCheckersOver(g.checkers, txn.boltView, txn.touched); checkErr != nil {
			txn.abort()
			return checkErr
		}

		if txn.broken != nil {
			txn.abort()
			return txn.broken
		}

		return nil
	})
	if err != nil {
		if txn != nil {
			// Only reached with work still to do when the commit itself
			// failed; abort is idempotent otherwise.
			txn.abort()
		}

		return wrapInterfaceErr(err)
	}

	txn.runCommitHooks()

	return nil
}
