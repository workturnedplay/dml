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
	"fmt"

	bolt "go.etcd.io/bbolt"
)

// boltTxn is the Tx BoltGraph.Transact hands its closure: reads come from
// the embedded boltView (over the update transaction, so they see this
// transaction's own writes), writes go to the same bolt transaction, and
// the embedded txLog records how to undo each write so a nested
// transaction can be rolled back to a savepoint. The outermost rollback
// needs no data undo at all: bolt discards the whole update transaction,
// and only the rollback hooks must run (see abort).
type boltTxn struct {
	boltView
	txLog

	meta    *bolt.Bucket
	names   *bolt.Bucket
	touched map[NodeID]struct{}

	// broken is the first failure of a data undo step. A transaction whose
	// savepoint could not be restored is no longer trustworthy, so
	// Transact aborts it instead of committing.
	broken error

	// dead is set once the outermost transaction is being abandoned or has
	// ended: data undo steps must no longer touch the (discarded or
	// closed) bolt transaction, only rollback hooks still run.
	dead bool
}

// Compile-time assertion that *boltTxn satisfies Tx.
var _ Tx = (*boltTxn)(nil)

// newBoltTxn returns the Tx over the update transaction btx, or
// ErrStoreCorrupt if a bucket is missing.
func newBoltTxn(btx *bolt.Tx) (*boltTxn, error) {
	view, err := newBoltView(btx)
	if err != nil {
		return nil, err
	}

	meta := btx.Bucket(boltBucketMeta)
	names := btx.Bucket(boltBucketNames)

	if meta == nil || names == nil {
		return nil, ErrStoreCorrupt
	}

	return &boltTxn{boltView: view, meta: meta, names: names}, nil
}

// pushUndo records step as the undo of a data mutation. A failing step
// poisons the transaction (see broken) rather than being ignored, and a
// step is skipped once the transaction is dead (see dead).
func (t *boltTxn) pushUndo(step func() error) {
	t.undo = append(t.undo, func() {
		if t.dead {
			return
		}

		if err := step(); err != nil && t.broken == nil {
			t.broken = err
		}
	})
}

// abort abandons the outermost transaction: bolt itself discards every
// write, so only the OnRollback hooks run (in LIFO order), and no data
// undo step touches the store. Calling it again does nothing.
func (t *boltTxn) abort() {
	t.dead = true
	t.rollback()
}

// Touch marks ids as touched without changing anything (see Tx.Touch).
func (t *boltTxn) Touch(ids ...NodeID) {
	t.touched = touchNodes(t.touched, ids...)
}

// Transact opens a nested transaction, a savepoint (see txLog.runNested).
func (t *boltTxn) Transact(fn func(nested Tx) error) error {
	return t.runNested(t, fn)
}

// counter reads the persisted ID counter: the next NodeID CreateNode will
// hand out and whether the ID space is exhausted. A store that has never
// created a node has no counter record, which reads as (0, false).
func (t *boltTxn) counter() (next NodeID, exhausted bool, err error) {
	raw := t.meta.Get(boltCounterKey)
	if raw == nil {
		return 0, false, nil
	}

	if len(raw) != boltIDSize+1 {
		return 0, false, ErrStoreCorrupt
	}

	return boltID(raw), raw[boltIDSize] == 1, nil
}

// setCounter persists the ID counter, in the same bolt transaction as the
// node creation it belongs to, so a committed node and the counter that
// covers it are never separated by a crash -- the never-reuse guarantee
// (theorystate.md sections 40, 78) survives restarts.
func (t *boltTxn) setCounter(next NodeID, exhausted bool) error {
	raw := make([]byte, boltIDSize+1)
	binary.BigEndian.PutUint64(raw, uint64(next))

	if exhausted {
		raw[boltIDSize] = 1
	}

	return wrapBoltErr("write counter", t.meta.Put(boltCounterKey, raw))
}

// CreateNode creates a node, exactly like Graph's node creation. Undoing
// it (nested rollback) deletes the node but, like Graph, does not give the
// ID back: the counter only increases within a process.
func (t *boltTxn) CreateNode() (NodeID, error) {
	next, exhausted, err := t.counter()
	if err != nil {
		return 0, err
	}

	if exhausted {
		return 0, ErrNodeIDExhausted
	}

	id := next
	key := boltKey8(id)

	t.pushUndo(func() error {
		return wrapBoltErr("undo create node", t.nodes.Delete(key))
	})

	if putErr := t.nodes.Put(key, boltPresent); putErr != nil {
		return 0, wrapBoltErr("create node", putErr)
	}

	if id == ^NodeID(0) {
		exhausted = true
	} else {
		next++
	}

	if counterErr := t.setCounter(next, exhausted); counterErr != nil {
		return 0, counterErr
	}

	t.Touch(id)

	return id, nil
}

// putEdgeKeys writes both index entries of one relationship.
func (t *boltTxn) putEdgeKeys(outKey, inKey []byte) error {
	if err := t.out.Put(outKey, boltPresent); err != nil {
		return wrapBoltErr("put outgoing edge", err)
	}

	return wrapBoltErr("put incoming edge", t.in.Put(inKey, boltPresent))
}

// deleteEdgeKeys removes both index entries of one relationship.
func (t *boltTxn) deleteEdgeKeys(outKey, inKey []byte) error {
	if err := t.out.Delete(outKey); err != nil {
		return wrapBoltErr("delete outgoing edge", err)
	}

	return wrapBoltErr("delete incoming edge", t.in.Delete(inKey))
}

// AddRelationship adds (a, b); both nodes must exist. Adding an existing
// relationship reports created == false and records nothing to undo.
func (t *boltTxn) AddRelationship(a, b NodeID) (created bool, err error) {
	existsA, err := t.NodeExists(a)
	if err != nil {
		return false, err
	}
	existsB, err := t.NodeExists(b)
	if err != nil {
		return false, err
	}
	if !existsA || !existsB {
		return false, ErrNodeNotFound
	}

	outKey, inKey := boltKey16(a, b), boltKey16(b, a)
	if t.out.Get(outKey) != nil {
		return false, nil
	}

	// The undo is recorded first: deleting keys that were never written is
	// a no-op, so a failure halfway through the two writes is still undone.
	t.pushUndo(func() error { return t.deleteEdgeKeys(outKey, inKey) })

	if putErr := t.putEdgeKeys(outKey, inKey); putErr != nil {
		return false, putErr
	}

	t.Touch(a, b)

	return true, nil
}

// RemoveRelationship removes (a, b); both nodes must exist. Removing a
// relationship that is not there reports removed == false.
func (t *boltTxn) RemoveRelationship(a, b NodeID) (removed bool, err error) {
	existsA, err := t.NodeExists(a)
	if err != nil {
		return false, err
	}
	existsB, err := t.NodeExists(b)
	if err != nil {
		return false, err
	}
	if !existsA || !existsB {
		return false, ErrNodeNotFound
	}

	outKey, inKey := boltKey16(a, b), boltKey16(b, a)
	if t.out.Get(outKey) == nil {
		return false, nil
	}

	t.pushUndo(func() error { return t.putEdgeKeys(outKey, inKey) })

	if deleteErr := t.deleteEdgeKeys(outKey, inKey); deleteErr != nil {
		return false, deleteErr
	}

	t.Touch(a, b)

	return true, nil
}

// DeleteNode deletes id only if it has no relationships in either
// direction (theorystate.md section 18). The undo restores the bare node,
// which is a complete restoration for the same reason as Txn.DeleteNode
// (theorystate.md section 78).
func (t *boltTxn) DeleteNode(id NodeID) error {
	if existsErr := requireExists(t, id); existsErr != nil {
		return existsErr
	}

	key := boltKey8(id)
	if boltHasPrefix(t.out, key) || boltHasPrefix(t.in, key) {
		return ErrNodeNotEmpty
	}

	t.pushUndo(func() error {
		return wrapBoltErr("undo delete node", t.nodes.Put(key, boltPresent))
	})

	if err := t.nodes.Delete(key); err != nil {
		return wrapBoltErr("delete node", err)
	}

	t.Touch(id)

	return nil
}

// boltNameRecordSize is the size of a stored name record: one state byte
// followed by an 8-byte NodeID.
const boltNameRecordSize = 1 + boltIDSize

// encodeNameRecord encodes rec as a state byte followed by its NodeID.
func encodeNameRecord(rec nameRecord) []byte {
	raw := make([]byte, boltNameRecordSize)
	raw[0] = byte(rec.state)
	binary.BigEndian.PutUint64(raw[1:], uint64(rec.id))

	return raw
}

// decodeNameRecord is encodeNameRecord's inverse. A record that is not
// exactly the expected size, or whose state is neither bound nor retired
// (an absent record is never stored), is ErrStoreCorrupt.
func decodeNameRecord(raw []byte) (nameRecord, error) {
	if len(raw) != boltNameRecordSize {
		return nameRecord{}, ErrStoreCorrupt
	}

	state := nameState(raw[0])
	if state != nameBound && state != nameRetired {
		return nameRecord{}, ErrStoreCorrupt
	}

	return nameRecord{state: state, id: boltID(raw[1:])}, nil
}

// Compile-time assertions that boltTxn provides the name store.
var (
	_ nameRecordProvider = (*boltTxn)(nil)
	_ nameRecordStore    = (*boltTxn)(nil)
)

// nameRecords implements nameRecordProvider: a bolt transaction stores name
// records in its own store, inside the same bolt transaction as the nodes
// (theorystate.md section 103).
func (t *boltTxn) nameRecords() nameRecordStore {
	return t
}

// getNameRecord returns name's record, or a record in the nameAbsent state
// if there is none.
func (t *boltTxn) getNameRecord(name string) (nameRecord, error) {
	raw := t.names.Get([]byte(name))
	if raw == nil {
		return nameRecord{}, nil
	}

	return decodeNameRecord(raw)
}

// putNameRecord stores rec as name's record.
func (t *boltTxn) putNameRecord(name string, rec nameRecord) error {
	return t.setNameRaw(name, encodeNameRecord(rec))
}

// deleteNameRecord removes name's record, if any.
func (t *boltTxn) deleteNameRecord(name string) error {
	return t.setNameRaw(name, nil)
}

// setNameRaw replaces name's stored bytes with raw (nil deletes the
// record), recording how to restore the previous bytes so that a nested
// rollback undoes the write like every other mutation.
func (t *boltTxn) setNameRaw(name string, raw []byte) error {
	key := []byte(name)

	// Copied: bolt's returned slice is only valid until the next write.
	// A nil previous value means "no record", since stored values are
	// never empty.
	previous := append([]byte(nil), t.names.Get(key)...)

	t.pushUndo(func() error { return t.writeNameRaw(key, previous) })

	return t.writeNameRaw(key, raw)
}

// writeNameRaw puts raw under key, or deletes key if raw is nil.
func (t *boltTxn) writeNameRaw(key, raw []byte) error {
	if raw == nil {
		return wrapBoltErr("delete name record", t.names.Delete(key))
	}

	return wrapBoltErr("put name record", t.names.Put(key, raw))
}

// forEachNameRecord calls fn for every stored name record, in name order.
func (t *boltTxn) forEachNameRecord(fn func(name string, rec nameRecord)) error {
	cursor := t.names.Cursor()

	for key, raw := cursor.First(); key != nil; key, raw = cursor.Next() {
		rec, err := decodeNameRecord(raw)
		if err != nil {
			return err
		}

		fn(string(key), rec)
	}

	return nil
}

// checkFormat is ensureBoltFormat's read-only counterpart for CheckStore: a
// missing record is corruption here, since opening a store always writes one.
func (t *boltTxn) checkFormat() error {
	version, present, err := readBoltFormat(t.meta)
	if err != nil {
		return err
	}

	if !present {
		return fmt.Errorf("%w: the store has no format version record", ErrStoreCorrupt)
	}

	return requireBoltFormat(version)
}
