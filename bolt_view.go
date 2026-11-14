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
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"

	bolt "go.etcd.io/bbolt"
)

// ErrStoreCorrupt is returned when the persistent store does not have the
// structure BoltGraph writes: a missing bucket or a malformed counter
// record. It indicates a damaged or foreign file, never a caller mistake.
var ErrStoreCorrupt = errors.New("persistent graph store is corrupt")

// ErrStoreFormat is returned when the store's format version record is
// malformed or names a version this build does not read. The version is
// stored because on-disk keys are 8-byte big-endian NodeIDs; a future layout
// change must be detected, never guessed at (theorystate.md section 112).
var ErrStoreFormat = errors.New("persistent graph store has an unsupported format version")

// ErrStoreLocked is returned when the store file is already open in another
// process (or elsewhere in this one), see openBoltDB.
var ErrStoreLocked = errors.New("persistent graph store is already open")

// BoltGraph's on-disk layout (theorystate.md section 102). NodeIDs are
// 8-byte big-endian, so byte order equals numeric order and a prefix scan
// returns relationships sorted exactly as Graph does. nodes: id ->
// present. out: from||to -> present. in: to||from -> present (the reverse
// index). meta: one counter record, next ID (8 bytes) plus an exhausted
// flag (1 byte). names: name (the key's raw bytes) -> one name record, a
// state byte (bound or retired) plus a NodeID (theorystate.md section 103).
// The values of nodes, out and in are the one-byte boltPresent, never
// empty, so "key exists" is always Get(key) != nil.
var (
	boltBucketNodes = []byte("nodes")
	boltBucketOut   = []byte("out")
	boltBucketIn    = []byte("in")
	boltBucketMeta  = []byte("meta")
	boltBucketNames = []byte("names")
	boltCounterKey  = []byte("counter")
	boltPresent     = []byte{1}
	boltFormatKey   = []byte("format")
)

const boltIDSize = 8

// boltFormatVersion is the store layout version this build reads and
// writes: the layout described at boltBucketNodes. It is stored in the meta
// bucket under boltFormatKey as 8 bytes, big-endian.
const boltFormatVersion uint64 = 1

// boltKey8 encodes id as an 8-byte big-endian key.
func boltKey8(id NodeID) []byte {
	key := make([]byte, boltIDSize)
	binary.BigEndian.PutUint64(key, uint64(id))

	return key
}

// boltKey16 encodes the ordered pair (first, second) as a 16-byte key.
func boltKey16(first, second NodeID) []byte {
	key := make([]byte, 2*boltIDSize)
	binary.BigEndian.PutUint64(key[:boltIDSize], uint64(first))
	binary.BigEndian.PutUint64(key[boltIDSize:], uint64(second))

	return key
}

// boltID decodes the NodeID held in the first eight bytes of key.
func boltID(key []byte) NodeID {
	return NodeID(binary.BigEndian.Uint64(key))
}

// wrapBoltErr wraps an error returned by the bolt package with the
// operation that failed, preserving errors.Is/As. A nil err stays nil.
func wrapBoltErr(op string, err error) error {
	if err == nil {
		return nil
	}

	return fmt.Errorf("bolt %s: %w", op, err)
}

// boltScanPrefix calls visit for every key in bucket that starts with
// prefix, in key order, until visit returns false. The key slice is valid
// only during the call.
func boltScanPrefix(bucket *bolt.Bucket, prefix []byte, visit func(key []byte) bool) {
	cursor := bucket.Cursor()

	for key, _ := cursor.Seek(prefix); key != nil && bytes.HasPrefix(key, prefix); key, _ = cursor.Next() {
		if !visit(key) {
			return
		}
	}
}

// boltHasPrefix reports whether bucket holds any key starting with prefix.
func boltHasPrefix(bucket *bolt.Bucket, prefix []byte) bool {
	found := false

	boltScanPrefix(bucket, prefix, func(_ []byte) bool {
		found = true
		return false
	})

	return found
}

// boltView is the read side of BoltGraph over one bolt transaction: a
// read-only one for reads made outside a Transact, or the update
// transaction itself for reads made inside one, where it sees that
// transaction's own uncommitted writes (theorystate.md section 101). It is
// also what a Checker's Check function receives.
type boltView struct {
	nodes *bolt.Bucket
	out   *bolt.Bucket
	in    *bolt.Bucket
}

// Compile-time assertion that boltView satisfies GraphReader.
var _ GraphReader = boltView{}

// newBoltView returns the view over btx, or ErrStoreCorrupt if a bucket is
// missing.
func newBoltView(btx *bolt.Tx) (boltView, error) {
	view := boltView{
		nodes: btx.Bucket(boltBucketNodes),
		out:   btx.Bucket(boltBucketOut),
		in:    btx.Bucket(boltBucketIn),
	}

	if view.nodes == nil || view.out == nil || view.in == nil {
		return boltView{}, ErrStoreCorrupt
	}

	return view, nil
}

// NodeExists reports whether id exists. bolt reads from an already-open
// transaction cannot themselves fail, so this always returns a nil
// error; the return exists to satisfy GraphReader (see its doc comment).
func (v boltView) NodeExists(id NodeID) (bool, error) {
	return v.nodes.Get(boltKey8(id)) != nil, nil
}

// HasRelationship reports whether the relationship (a, b) exists.
func (v boltView) HasRelationship(a, b NodeID) (bool, error) {
	existsA, err := v.NodeExists(a)
	if err != nil {
		return false, err
	}
	if !existsA {
		return false, nil
	}

	existsB, err := v.NodeExists(b)
	if err != nil {
		return false, err
	}
	if !existsB {
		return false, nil
	}

	return v.out.Get(boltKey16(a, b)) != nil, nil
}

// FindRelationship reports whether the exact relationship (from, to)
// exists.
func (v boltView) FindRelationship(from, to NodeID) (Relationship, bool, error) {
	existsFrom, err := v.NodeExists(from)
	if err != nil {
		return Relationship{}, false, err
	}

	existsTo, err := v.NodeExists(to)
	if err != nil {
		return Relationship{}, false, err
	}

	if !existsFrom || !existsTo {
		return Relationship{}, false, ErrNodeNotFound
	}

	if v.out.Get(boltKey16(from, to)) == nil {
		return Relationship{}, false, nil
	}

	return Relationship{From: from, To: to}, true, nil
}

// FindOutgoing returns every relationship whose source is from, sorted by
// To.
func (v boltView) FindOutgoing(from NodeID) ([]Relationship, error) {
	exists, err := v.NodeExists(from)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrNodeNotFound
	}

	relationships := []Relationship{}

	boltScanPrefix(v.out, boltKey8(from), func(key []byte) bool {
		relationships = append(relationships, Relationship{From: from, To: boltID(key[boltIDSize:])})
		return true
	})

	return relationships, nil
}

// FindIncoming returns every relationship whose target is to, sorted by
// From.
func (v boltView) FindIncoming(to NodeID) ([]Relationship, error) {
	exists, err := v.NodeExists(to)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrNodeNotFound
	}

	relationships := []Relationship{}

	boltScanPrefix(v.in, boltKey8(to), func(key []byte) bool {
		relationships = append(relationships, Relationship{From: boltID(key[boltIDSize:]), To: to})
		return true
	})

	return relationships, nil
}

// FindRelationships returns every relationship, sorted by From then To.
// This is O(graph); see theorystate.md section 105. bolt cursor reads
// from an already-open transaction cannot themselves fail, so this
// always returns a nil error.
func (v boltView) FindRelationships() ([]Relationship, error) {
	relationships := []Relationship{}
	cursor := v.out.Cursor()

	for key, _ := cursor.First(); key != nil; key, _ = cursor.Next() {
		relationships = append(relationships, Relationship{From: boltID(key), To: boltID(key[boltIDSize:])})
	}

	return relationships, nil
}

// FindNodes returns every existing NodeID, sorted ascending. This is
// O(graph); see theorystate.md section 105. bolt cursor reads from an
// already-open transaction cannot themselves fail, so this always
// returns a nil error.
func (v boltView) FindNodes() ([]NodeID, error) {
	ids := []NodeID{}
	cursor := v.nodes.Cursor()

	for key, _ := cursor.First(); key != nil; key, _ = cursor.Next() {
		ids = append(ids, boltID(key))
	}

	return ids, nil
}

// findNodesAfter returns up to limit node IDs greater than after (from the
// first node if hasAfter is false), in ascending order, reading only that
// range with a cursor (see nodePager). bolt cursor reads from an
// already-open transaction cannot themselves fail, so this always
// returns a nil error; the return exists to satisfy nodePager, whose
// signature must accommodate a backend-generic caller (nodesAfter) that
// can genuinely fail elsewhere (e.g. its own fallback path calling
// GraphReader.FindNodes on a backend that has no paging of its own).
func (v boltView) findNodesAfter(after NodeID, hasAfter bool, limit int) ([]NodeID, error) {
	ids := []NodeID{}
	cursor := v.nodes.Cursor()

	var key []byte

	if hasAfter {
		key, _ = cursor.Seek(boltKey8(after))
		if key != nil && boltID(key) == after {
			key, _ = cursor.Next()
		}
	} else {
		key, _ = cursor.First()
	}

	for ; key != nil && len(ids) < limit; key, _ = cursor.Next() {
		ids = append(ids, boltID(key))
	}

	return ids, nil
}

// findOutgoingAfter returns up to limit relationships whose source is from
// and whose target is greater than after (from the first if hasAfter is
// false), in ascending target order, reading only that range with a cursor
// (see outgoingPager). bolt cursor reads from an already-open transaction
// cannot themselves fail, so this always returns a nil error once from's
// existence is confirmed.
func (v boltView) findOutgoingAfter(from, after NodeID, hasAfter bool, limit int) ([]Relationship, error) {
	exists, err := v.NodeExists(from)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrNodeNotFound
	}

	relationships := []Relationship{}
	prefix := boltKey8(from)
	cursor := v.out.Cursor()

	var key []byte

	if hasAfter {
		key, _ = cursor.Seek(boltKey16(from, after))
		if key != nil && boltID(key[boltIDSize:]) == after {
			key, _ = cursor.Next()
		}
	} else {
		key, _ = cursor.Seek(prefix)
	}

	for ; key != nil && bytes.HasPrefix(key, prefix) && len(relationships) < limit; key, _ = cursor.Next() {
		relationships = append(relationships, Relationship{From: from, To: boltID(key[boltIDSize:])})
	}

	return relationships, nil
}

// findIncomingAfter is findOutgoingAfter's mirror over the incoming index
// (see incomingPager).
func (v boltView) findIncomingAfter(to, after NodeID, hasAfter bool, limit int) ([]Relationship, error) {
	exists, err := v.NodeExists(to)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrNodeNotFound
	}

	relationships := []Relationship{}
	prefix := boltKey8(to)
	cursor := v.in.Cursor()

	var key []byte

	if hasAfter {
		key, _ = cursor.Seek(boltKey16(to, after))
		if key != nil && boltID(key[boltIDSize:]) == after {
			key, _ = cursor.Next()
		}
	} else {
		key, _ = cursor.Seek(prefix)
	}

	for ; key != nil && bytes.HasPrefix(key, prefix) && len(relationships) < limit; key, _ = cursor.Next() {
		relationships = append(relationships, Relationship{From: boltID(key[boltIDSize:]), To: to})
	}

	return relationships, nil
}

// findCommonChildren returns up to limit nodes that are children of both a
// and b, ascending (see commonChildrenFinder). It is a leapfrog join over the
// two sorted runs of the out bucket: the run that is behind seeks straight to
// the other's current child, so the cost is about the smaller run, not the
// larger. boltTxn picks this up through embedding boltView.
func (v boltView) findCommonChildren(a, b NodeID, limit int) ([]NodeID, error) {
	exists, err := v.NodeExists(a)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrNodeNotFound
	}

	common := []NodeID{}
	prefixA, prefixB := boltKey8(a), boltKey8(b)
	cursorA, cursorB := v.out.Cursor(), v.out.Cursor()
	keyA, _ := cursorA.Seek(prefixA)
	keyB, _ := cursorB.Seek(prefixB)

	for len(common) < limit && bytes.HasPrefix(keyA, prefixA) && bytes.HasPrefix(keyB, prefixB) {
		childA, childB := boltID(keyA[boltIDSize:]), boltID(keyB[boltIDSize:])

		switch {
		case childA == childB:
			common = append(common, childA)
			keyA, _ = cursorA.Next()
			keyB, _ = cursorB.Next()
		case childA < childB:
			keyA, _ = cursorA.Seek(boltKey16(a, childB))
		default:
			keyB, _ = cursorB.Seek(boltKey16(b, childA))
		}
	}

	return common, nil
}

// Compile-time assertions that boltView pages nodes and relationships.
// boltTxn picks these up for free through embedding boltView.
var (
	_ nodePager     = boltView{}
	_ outgoingPager = boltView{}
	_ incomingPager = boltView{}
)

// checkEdges verifies every key of the edge index primary against its
// mirror index: the key has the right length, the mirrored key exists (the
// two indexes describe the same relationships, theorystate.md section 4),
// and both endpoints are existing nodes. primaryName and mirrorName are
// only for the message.
func (v boltView) checkEdges(primary, mirror *bolt.Bucket, primaryName, mirrorName string) error {
	cursor := primary.Cursor()

	for key, _ := cursor.First(); key != nil; key, _ = cursor.Next() {
		if len(key) != 2*boltIDSize {
			return fmt.Errorf("%w: %s index key of %d bytes, want %d", ErrStoreCorrupt, primaryName, len(key), 2*boltIDSize)
		}

		first, second := boltID(key), boltID(key[boltIDSize:])

		if mirror.Get(boltKey16(second, first)) == nil {
			return fmt.Errorf("%w: key (%d,%d) is in the %s index but its mirror is missing from the %s index", ErrStoreCorrupt, first, second, primaryName, mirrorName)
		}

		existsFirst, err := v.NodeExists(first)
		if err != nil {
			return err
		}

		existsSecond, err := v.NodeExists(second)
		if err != nil {
			return err
		}

		if !existsFirst || !existsSecond {
			return fmt.Errorf("%w: key (%d,%d) in the %s index refers to a node that does not exist", ErrStoreCorrupt, first, second, primaryName)
		}
	}

	return nil
}

// checkLayout verifies BoltGraph's own layout, which bolt's page-level check
// knows nothing about: node keys have the right length, the persisted ID
// counter is ahead of every node (so no existing ID can be issued again,
// theorystate.md section 40) unless the ID space is exhausted, and the
// outgoing and incoming indexes mirror each other. next and exhausted are
// the persisted counter.
func (v boltView) checkLayout(next NodeID, exhausted bool) error {
	var highest NodeID

	hasNodes := false
	cursor := v.nodes.Cursor()

	for key, _ := cursor.First(); key != nil; key, _ = cursor.Next() {
		if len(key) != boltIDSize {
			return fmt.Errorf("%w: node key of %d bytes, want %d", ErrStoreCorrupt, len(key), boltIDSize)
		}

		highest = boltID(key)
		hasNodes = true
	}

	if hasNodes && !exhausted && highest >= next {
		return fmt.Errorf("%w: node %d exists but the ID counter is only at %d, so it could be issued again", ErrStoreCorrupt, highest, next)
	}

	if err := v.checkEdges(v.out, v.in, "outgoing", "incoming"); err != nil {
		return err
	}

	return v.checkEdges(v.in, v.out, "incoming", "outgoing")
}
