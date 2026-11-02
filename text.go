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
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// The interned-string layer (theorystate.md section 114): one canonical node
// per piece of text, built from the existing primitives, plus a prefix index
// over the strings someone marked on purpose.

const (
	// NameAllFoundational tags every foundational name (and ROOT), so a
	// browser can list them without checking each parent of a node against
	// every known foundational node.
	NameAllFoundational = "AllFoundational"

	// NameAllStrings tags every string node, the empty string included.
	NameAllStrings = "AllStrings"

	// NameAllChars tags every char node (one per code point, seeded through
	// NameRegistry).
	NameAllChars = "AllChars"

	// NameAllIndexedStrings tags the strings that were marked for the prefix
	// index (MarkIndexed). It means "asked for on purpose", nothing more: the
	// text may contain spaces, or be a whole sentence.
	NameAllIndexedStrings = "AllIndexedStrings"

	// NameAllIndexNodes tags the nodes of the prefix index tree.
	NameAllIndexNodes = "AllIndexNodes"

	// NameAllIndexEntries tags the entry slots that end an index path at the
	// canonical string.
	NameAllIndexEntries = "AllIndexEntries"

	// NameAllStartsWithSlots tags a char's StartsWith slot: its children are
	// the strings whose first char it is.
	NameAllStartsWithSlots = "AllStartsWithSlots"

	// NameAllIndexStepSlots tags a char's IndexStep slot: its children are
	// the index nodes reached by that char.
	NameAllIndexStepSlots = "AllIndexStepSlots"

	// NameEmptyString names the root of the string trie: the empty string.
	NameEmptyString = "EmptyString"

	// NameIndexRoot names the root of the prefix index tree.
	NameIndexRoot = "IndexRoot"
)

const (
	// MaxTextRunes is the longest text Intern accepts, in runes. It bounds
	// the size of one intern transaction.
	MaxTextRunes = 4096

	// charNamePrefix starts the NameRegistry name of every char node.
	charNamePrefix = "char/U+"
)

var (
	// ErrNotString is returned for a node that is not tagged (AllStrings, node).
	ErrNotString = errors.New("node is not tagged as a string")

	// ErrInvalidText is returned for text that is not valid UTF-8. It is
	// rejected, never repaired.
	ErrInvalidText = errors.New("text is not valid UTF-8")

	// ErrTextTooLong is returned for text of more than MaxTextRunes runes.
	ErrTextTooLong = errors.New("text is too long")

	// ErrInvalidTextStructure is returned when the string trie or the prefix
	// index does not have the shape TextRegistry builds.
	ErrInvalidTextStructure = errors.New("text structure is invalid")
)

// charSlots is a char node and its two slots.
type charSlots struct {
	node, startsWith, indexStep NodeID
}

// stringParts is what identifies a non-empty string: the string it extends
// (its tail), the StartsWith slot of its first char, and that char.
type stringParts struct {
	tail, slot, char NodeID
}

// textCheck is one commit-time check, run for a touched node carrying tag.
type textCheck struct {
	tag NodeID
	run func(g GraphReader, node NodeID) error
}

// TextRegistry interns text (theorystate.md section 114).
//
// Every string is one canonical node. A non-empty string Q is the string
// tail with ONE CHAR PUT IN FRONT of it, so the strings that end with S are
// exactly the descendants of S:
//
//	(AllStrings, Q)
//	tail -> Q                         Q is tail with a char in front
//	char -> StartsWith -> Q           the char is Q's first char
//
// Looking up "char + tail" is the one child that tail and StartsWith share
// (findUniqueTaggedChild, the leapfrog join), so lookup never depends on how
// many strings exist. Text is read back by walking up: the StartsWith slot
// gives the first char, the AllStrings parent gives the rest. Nothing is
// stored reversed and no node stores text.
//
// A separate prefix index covers the strings marked with MarkIndexed:
//
//	index node X -> index node Y       Y is X with one more char at the END
//	char -> IndexStep -> Y             that char is the one added
//	index node -> entry -> string      the entry ends the path at the string
//
// Only Intern and MarkIndexed/UnmarkIndexed write either structure. Chars
// are seeded through NameRegistry ("char/U+0061"), the one out-of-graph
// part; everything above them is graph structure. Chars are case-sensitive
// and no Unicode normalization is done: the caller normalizes.
//
// Rule: list elements and anything else that refers to text must use string
// nodes, never char nodes. A one-char string and its char are different
// nodes.
type TextRegistry struct {
	names *NameRegistry

	allStrings         NodeID
	allChars           NodeID
	allIndexedStrings  NodeID
	allIndexNodes      NodeID
	allIndexEntries    NodeID
	allStartsWithSlots NodeID
	allIndexStepSlots  NodeID
	emptyString        NodeID
	indexRoot          NodeID

	checks []textCheck
}

// NewTextRegistry creates a TextRegistry over graph. ids must hold every
// name TextRegistry needs (as returned by NameRegistry.BootstrapNames over
// FoundationalNames); names must be the registry those names were
// bootstrapped through, since chars are named in it. The constructor tags
// the two roots (idempotently) and registers the Checker.
func NewTextRegistry(graph GraphAPI, names *NameRegistry, ids map[string]NodeID) (*TextRegistry, error) {
	t := &TextRegistry{names: names}

	wanted := [...]struct {
		name string
		dest *NodeID
	}{
		{NameAllStrings, &t.allStrings},
		{NameAllChars, &t.allChars},
		{NameAllIndexedStrings, &t.allIndexedStrings},
		{NameAllIndexNodes, &t.allIndexNodes},
		{NameAllIndexEntries, &t.allIndexEntries},
		{NameAllStartsWithSlots, &t.allStartsWithSlots},
		{NameAllIndexStepSlots, &t.allIndexStepSlots},
		{NameEmptyString, &t.emptyString},
		{NameIndexRoot, &t.indexRoot},
	}

	nodes := make([]NodeID, 0, len(wanted))

	for _, want := range wanted {
		id, ok := ids[want.name]
		if !ok {
			return nil, fmt.Errorf("%w: %q", ErrNameNotFound, want.name)
		}

		*want.dest = id
		nodes = append(nodes, id)
	}

	if existsErr := requireAllExist(graph, nodes...); existsErr != nil {
		return nil, existsErr
	}

	tagErr := graph.Transact(func(tx Tx) error {
		if rootErr := tagNodeTx(tx, t.allStrings, t.emptyString); rootErr != nil {
			return rootErr
		}

		return tagNodeTx(tx, t.allIndexNodes, t.indexRoot)
	})
	if tagErr != nil {
		return nil, wrapInterfaceErr(tagErr)
	}

	t.checks = []textCheck{
		{tag: t.allStrings, run: t.checkString},
		{tag: t.allChars, run: t.checkChar},
		{tag: t.allStartsWithSlots, run: t.checkSlot},
		{tag: t.allIndexStepSlots, run: t.checkSlot},
		{tag: t.allIndexNodes, run: t.checkIndexNode},
		{tag: t.allIndexEntries, run: t.checkEntry},
		{tag: t.allIndexedStrings, run: t.checkIndexedString},
	}

	tags := make([]NodeID, 0, len(t.checks))
	for _, check := range t.checks {
		tags = append(tags, check.tag)
	}

	graph.RegisterChecker(Checker{
		Name:  fmt.Sprintf("TextRegistry(tag=%d)", t.allStrings),
		Tags:  tags,
		Check: t.checkTouched,
	})

	return t, nil
}

// EmptyString returns the node of the empty string, the root of the trie.
func (t *TextRegistry) EmptyString() NodeID {
	return t.emptyString
}

// charName is the NameRegistry name of the char node of r.
func charName(r rune) string {
	return fmt.Sprintf("%s%04X", charNamePrefix, r)
}

// validateText rejects invalid UTF-8 and over-long text, and returns the
// runes.
func validateText(text string) ([]rune, error) {
	if !utf8.ValidString(text) {
		return nil, ErrInvalidText
	}

	runes := []rune(text)
	if len(runes) > MaxTextRunes {
		return nil, fmt.Errorf("%w: %d runes, limit %d", ErrTextTooLong, len(runes), MaxTextRunes)
	}

	return runes, nil
}

// slotsOf finds char's two slots, each by its own tag and verified to be
// owned by char.
func (t *TextRegistry) slotsOf(g GraphReader, char NodeID) (charSlots, error) {
	slots := charSlots{node: char}

	wanted := [...]struct {
		tag  NodeID
		dest *NodeID
	}{
		{t.allStartsWithSlots, &slots.startsWith},
		{t.allIndexStepSlots, &slots.indexStep},
	}

	for _, want := range wanted {
		slot, found, err := findOwnedSlot(g, char, t.allChars, want.tag)
		if err != nil {
			return charSlots{}, err
		}
		if !found {
			return charSlots{}, ErrInvalidTextStructure
		}

		*want.dest = slot
	}

	return slots, nil
}

// buildChar tags node as a char and gives it its two slots.
func (t *TextRegistry) buildChar(tx txOps, node NodeID) error {
	if tagErr := tagNodeTx(tx, t.allChars, node); tagErr != nil {
		return tagErr
	}

	for _, tag := range []NodeID{t.allStartsWithSlots, t.allIndexStepSlots} {
		slot, slotErr := createTaggedNodeTx(tx, tag)
		if slotErr != nil {
			return slotErr
		}

		if linkErr := addRelationshipTx(tx, node, slot); linkErr != nil {
			return linkErr
		}
	}

	return nil
}

// ensureChar returns the char node of r and its slots, creating them if
// needed. cache lives for one transaction closure (it may be re-run).
func (t *TextRegistry) ensureChar(tx Tx, cache map[rune]charSlots, r rune) (charSlots, error) {
	if cached, ok := cache[r]; ok {
		return cached, nil
	}

	node, err := t.names.EnsureNamedNode(tx, charName(r))
	if err != nil {
		return charSlots{}, err
	}

	tagged, taggedErr := tx.HasRelationship(t.allChars, node)
	if taggedErr != nil {
		return charSlots{}, wrapInterfaceErr(taggedErr)
	}

	if !tagged {
		if buildErr := t.buildChar(tx, node); buildErr != nil {
			return charSlots{}, buildErr
		}
	}

	slots, slotsErr := t.slotsOf(tx, node)
	if slotsErr != nil {
		return charSlots{}, slotsErr
	}

	cache[r] = slots

	return slots, nil
}

// existingChar is ensureChar for reads: found is false if r was never used.
func (t *TextRegistry) existingChar(g GraphReader, cache map[rune]charSlots, r rune) (charSlots, bool, error) {
	if cached, ok := cache[r]; ok {
		return cached, true, nil
	}

	node, bound, err := t.names.lookupLive(g, charName(r))
	if err != nil || !bound {
		return charSlots{}, false, err
	}

	slots, slotsErr := t.slotsOf(g, node)
	if slotsErr != nil {
		return charSlots{}, false, slotsErr
	}

	cache[r] = slots

	return slots, true, nil
}

// prepend returns the string "r + tail", creating it if needed.
func (t *TextRegistry) prepend(tx Tx, cache map[rune]charSlots, tail NodeID, r rune) (NodeID, error) {
	slots, err := t.ensureChar(tx, cache, r)
	if err != nil {
		return 0, err
	}

	existing, found, findErr := findUniqueTaggedChild(tx, tail, slots.startsWith)
	if findErr != nil || found {
		return existing, findErr
	}

	created, createErr := createTaggedNodeTx(tx, t.allStrings)
	if createErr != nil {
		return 0, createErr
	}

	if tailErr := addRelationshipTx(tx, tail, created); tailErr != nil {
		return 0, tailErr
	}

	if slotErr := addRelationshipTx(tx, slots.startsWith, created); slotErr != nil {
		return 0, slotErr
	}

	return created, nil
}

// Intern returns the node of text, creating it (and every string it needs,
// which are its suffixes) in one transaction. Interning the same text again
// returns the same node. The empty string is EmptyString.
func (t *TextRegistry) Intern(graph Transactor, text string) (NodeID, error) {
	runes, err := validateText(text)
	if err != nil {
		return 0, err
	}

	return transactValue(graph, func(tx Tx) (NodeID, error) {
		cache := make(map[rune]charSlots)
		current := t.emptyString

		for i := len(runes) - 1; i >= 0; i-- {
			next, prependErr := t.prepend(tx, cache, current, runes[i])
			if prependErr != nil {
				return 0, prependErr
			}

			current = next
		}

		return current, nil
	})
}

// InternIndexed is Intern followed by MarkIndexed, in one transaction.
func (t *TextRegistry) InternIndexed(graph Transactor, text string) (NodeID, error) {
	return transactValue(graph, func(tx Tx) (NodeID, error) {
		node, internErr := t.Intern(tx, text)
		if internErr != nil {
			return 0, internErr
		}

		if _, markErr := t.MarkIndexed(tx, node); markErr != nil {
			return 0, markErr
		}

		return node, nil
	})
}

// Lookup returns the node of text if it was interned, without creating
// anything.
func (t *TextRegistry) Lookup(graph GraphReader, text string) (NodeID, bool, error) {
	runes, err := validateText(text)
	if err != nil {
		return 0, false, err
	}

	cache := make(map[rune]charSlots)
	current := t.emptyString

	for i := len(runes) - 1; i >= 0; i-- {
		slots, charFound, charErr := t.existingChar(graph, cache, runes[i])
		if charErr != nil || !charFound {
			return 0, false, charErr
		}

		next, stepFound, stepErr := findUniqueTaggedChild(graph, current, slots.startsWith)
		if stepErr != nil || !stepFound {
			return 0, false, stepErr
		}

		current = next
	}

	return current, true, nil
}

// IsString reports whether id is tagged (AllStrings, id).
func (t *TextRegistry) IsString(graph GraphReader, id NodeID) (bool, error) {
	has, err := graph.HasRelationship(t.allStrings, id)

	return has, wrapInterfaceErr(err)
}

// IsIndexed reports whether id is tagged (AllIndexedStrings, id).
func (t *TextRegistry) IsIndexed(graph GraphReader, id NodeID) (bool, error) {
	has, err := graph.HasRelationship(t.allIndexedStrings, id)

	return has, wrapInterfaceErr(err)
}

// partsOf finds what identifies the non-empty string q. A string missing
// any of them is ErrInvalidTextStructure.
func (t *TextRegistry) partsOf(g GraphReader, q NodeID) (stringParts, error) {
	tail, hasTail, err := findUniqueTaggedParent(g, q, t.allStrings)
	if err != nil {
		return stringParts{}, err
	}

	slot, hasSlot, err := findUniqueTaggedParent(g, q, t.allStartsWithSlots)
	if err != nil {
		return stringParts{}, err
	}

	if !hasTail || !hasSlot {
		return stringParts{}, ErrInvalidTextStructure
	}

	char, hasChar, charErr := findUniqueTaggedParent(g, slot, t.allChars)
	if charErr != nil {
		return stringParts{}, charErr
	}
	if !hasChar {
		return stringParts{}, ErrInvalidTextStructure
	}

	return stringParts{tail: tail, slot: slot, char: char}, nil
}

// runeOf returns the code point a char node stands for, from its name.
func (t *TextRegistry) runeOf(char NodeID) (rune, error) {
	name, named := t.names.effectiveNameFor(char)
	if !named {
		return 0, fmt.Errorf("%w: char node %d has no name", ErrInvalidTextStructure, char)
	}

	digits, hasPrefix := strings.CutPrefix(name, charNamePrefix)
	if !hasPrefix {
		return 0, fmt.Errorf("%w: char node %d is named %q", ErrInvalidTextStructure, char, name)
	}

	var r rune

	if _, scanErr := fmt.Sscanf(digits, "%x", &r); scanErr != nil {
		return 0, fmt.Errorf("%w: char name %q: %w", ErrInvalidTextStructure, name, scanErr)
	}

	if !utf8.ValidRune(r) || charName(r) != name {
		return 0, fmt.Errorf("%w: char name %q does not name a code point", ErrInvalidTextStructure, name)
	}

	return r, nil
}

// runesOf reads the text of the string node back, first char first, by
// walking up the trie. A broken chain, or one that revisits a node, is
// ErrInvalidTextStructure.
func (t *TextRegistry) runesOf(g GraphReader, node NodeID) ([]rune, error) {
	if requireErr := requireTagged(g, node, t.allStrings, ErrNotString); requireErr != nil {
		return nil, requireErr
	}

	runes := make([]rune, 0, 16)
	visited := make(map[NodeID]struct{})

	for current := node; current != t.emptyString; {
		if _, seen := visited[current]; seen || len(runes) >= MaxTextRunes {
			return nil, ErrInvalidTextStructure
		}

		visited[current] = struct{}{}

		parts, partsErr := t.partsOf(g, current)
		if partsErr != nil {
			return nil, partsErr
		}

		r, runeErr := t.runeOf(parts.char)
		if runeErr != nil {
			return nil, runeErr
		}

		runes = append(runes, r)
		current = parts.tail
	}

	return runes, nil
}

// Text returns the text of the string node.
func (t *TextRegistry) Text(graph GraphReader, node NodeID) (string, error) {
	runes, err := t.runesOf(graph, node)
	if err != nil {
		return "", err
	}

	return string(runes), nil
}

// EndingWith returns up to limit strings that end with suffix (suffix
// itself included), shortest first: the descendants of the suffix's node.
// With indexedOnly only strings marked with MarkIndexed are returned; the
// others are stepping stones the trie needs. A suffix that was never
// interned has no matches. A non-positive limit returns nothing.
func (t *TextRegistry) EndingWith(graph GraphReader, suffix string, limit int, indexedOnly bool) ([]NodeID, error) {
	start, found, err := t.Lookup(graph, suffix)
	if err != nil || !found || limit <= 0 {
		return nil, err
	}

	var matches []NodeID

	visited := map[NodeID]struct{}{start: {}}
	queue := []NodeID{start}

	for len(queue) > 0 && len(matches) < limit {
		current := queue[0]
		queue = queue[1:]

		take := true

		if indexedOnly {
			indexed, indexedErr := t.IsIndexed(graph, current)
			if indexedErr != nil {
				return nil, indexedErr
			}

			take = indexed
		}

		if take {
			matches = append(matches, current)
		}

		children, childErr := graph.FindOutgoing(current)
		if childErr != nil {
			return nil, wrapInterfaceErr(childErr)
		}

		for _, rel := range children {
			if _, seen := visited[rel.To]; seen {
				continue
			}

			visited[rel.To] = struct{}{}

			// A trie node can also have a form slot as a child
			// (text_forms.go): only strings are part of the trie.
			isString, stringErr := t.IsString(graph, rel.To)
			if stringErr != nil {
				return nil, stringErr
			}
			if !isString {
				continue
			}

			queue = append(queue, rel.To)
		}
	}

	return matches, nil
}

// extendIndex returns the index child of node for step, creating it if
// needed.
func (t *TextRegistry) extendIndex(tx Tx, node, step NodeID) (NodeID, error) {
	existing, found, err := findUniqueTaggedChild(tx, node, step)
	if err != nil || found {
		return existing, err
	}

	created, createErr := createTaggedNodeTx(tx, t.allIndexNodes)
	if createErr != nil {
		return 0, createErr
	}

	if nodeErr := addRelationshipTx(tx, node, created); nodeErr != nil {
		return 0, nodeErr
	}

	if stepErr := addRelationshipTx(tx, step, created); stepErr != nil {
		return 0, stepErr
	}

	return created, nil
}

// attachEntry ends the index path at node with an entry slot for str and
// tags str as indexed. It reports false if str was already indexed there.
func (t *TextRegistry) attachEntry(tx Tx, node, str NodeID) (bool, error) {
	entry, found, err := findUniqueTaggedChild(tx, node, t.allIndexEntries)
	if err != nil {
		return false, err
	}

	if found {
		target, hasTarget, targetErr := singleChildTarget(tx, entry)
		if targetErr != nil {
			return false, targetErr
		}
		if !hasTarget || target != str {
			return false, ErrInvalidTextStructure
		}

		return false, nil
	}

	created, createErr := createTaggedNodeTx(tx, t.allIndexEntries)
	if createErr != nil {
		return false, createErr
	}

	for _, edge := range []Relationship{
		{From: node, To: created},
		{From: created, To: str},
		{From: t.allIndexedStrings, To: str},
	} {
		if linkErr := addRelationshipTx(tx, edge.From, edge.To); linkErr != nil {
			return false, linkErr
		}
	}

	return true, nil
}

// MarkIndexed adds str to the prefix index: the path of its chars from the
// index root (created as needed), an entry slot at its end, and the
// (AllIndexedStrings, str) tag, in one transaction. It reports whether str
// was newly indexed; indexing an indexed string is a no-op.
func (t *TextRegistry) MarkIndexed(graph Transactor, str NodeID) (bool, error) {
	return transactBool(graph, func(tx Tx) (bool, error) {
		runes, err := t.runesOf(tx, str)
		if err != nil {
			return false, err
		}

		cache := make(map[rune]charSlots)
		node := t.indexRoot

		for _, r := range runes {
			slots, charErr := t.ensureChar(tx, cache, r)
			if charErr != nil {
				return false, charErr
			}

			next, stepErr := t.extendIndex(tx, node, slots.indexStep)
			if stepErr != nil {
				return false, stepErr
			}

			node = next
		}

		return t.attachEntry(tx, node, str)
	})
}

// indexPath follows runes down the prefix index. path starts at the index
// root and has one more element than steps, which holds the IndexStep slot
// used at each step. found is false if the index has no such path.
func (t *TextRegistry) indexPath(g GraphReader, runes []rune) ([]NodeID, []NodeID, bool, error) {
	path := make([]NodeID, 1, len(runes)+1)
	path[0] = t.indexRoot
	steps := make([]NodeID, 0, len(runes))
	cache := make(map[rune]charSlots)

	for _, r := range runes {
		slots, charFound, charErr := t.existingChar(g, cache, r)
		if charErr != nil || !charFound {
			return nil, nil, false, charErr
		}

		next, stepFound, stepErr := findUniqueTaggedChild(g, path[len(path)-1], slots.indexStep)
		if stepErr != nil || !stepFound {
			return nil, nil, false, stepErr
		}

		path = append(path, next)
		steps = append(steps, slots.indexStep)
	}

	return path, steps, true, nil
}

// detachEntry removes the entry slot of str at index node and str's
// indexed tag.
func (t *TextRegistry) detachEntry(tx Tx, node, str NodeID) error {
	entry, found, err := findUniqueTaggedChild(tx, node, t.allIndexEntries)
	if err != nil {
		return err
	}
	if !found {
		return ErrInvalidTextStructure
	}

	target, hasTarget, targetErr := singleChildTarget(tx, entry)
	if targetErr != nil {
		return targetErr
	}
	if !hasTarget || target != str {
		return ErrInvalidTextStructure
	}

	for _, edge := range []Relationship{
		{From: node, To: entry},
		{From: t.allIndexEntries, To: entry},
		{From: entry, To: str},
		{From: t.allIndexedStrings, To: str},
	} {
		if removeErr := removeRelationshipTx(tx, edge.From, edge.To); removeErr != nil {
			return removeErr
		}
	}

	return deleteNodeTx(tx, entry)
}

// pruneIndexNode deletes the index node (with its edges from parent and the
// step slot) if nothing else uses it. It runs as a nested savepoint, so a
// node that still has children or other referrers (ErrNodeNotEmpty) is left
// exactly as it was, and pruned is false.
func (t *TextRegistry) pruneIndexNode(tx Tx, parent, step, node NodeID) (bool, error) {
	pruned, err := transactBool(tx, func(inner Tx) (bool, error) {
		for _, edge := range []Relationship{
			{From: parent, To: node},
			{From: step, To: node},
			{From: t.allIndexNodes, To: node},
		} {
			if removeErr := removeRelationshipTx(inner, edge.From, edge.To); removeErr != nil {
				return false, removeErr
			}
		}

		if deleteErr := deleteNodeTx(inner, node); deleteErr != nil {
			return false, deleteErr
		}

		return true, nil
	})
	if errors.Is(err, ErrNodeNotEmpty) {
		return false, nil
	}

	return pruned, err
}

// UnmarkIndexed removes str from the prefix index: its entry slot and
// indexed tag, then every index node on its path that nothing else needs,
// from the end upward, in one transaction. The string itself stays. It
// reports whether str was indexed.
func (t *TextRegistry) UnmarkIndexed(graph Transactor, str NodeID) (bool, error) {
	return transactBool(graph, func(tx Tx) (bool, error) {
		runes, err := t.runesOf(tx, str)
		if err != nil {
			return false, err
		}

		indexed, indexedErr := t.IsIndexed(tx, str)
		if indexedErr != nil || !indexed {
			return false, indexedErr
		}

		path, steps, found, pathErr := t.indexPath(tx, runes)
		if pathErr != nil {
			return false, pathErr
		}
		if !found {
			return false, ErrInvalidTextStructure
		}

		if detachErr := t.detachEntry(tx, path[len(path)-1], str); detachErr != nil {
			return false, detachErr
		}

		for i := len(path) - 1; i > 0; i-- {
			pruned, pruneErr := t.pruneIndexNode(tx, path[i-1], steps[i-1], path[i])
			if pruneErr != nil {
				return false, pruneErr
			}
			if !pruned {
				break
			}
		}

		return true, nil
	})
}

// IndexedWithPrefix returns up to limit indexed strings that start with
// prefix, shortest first. Only strings marked with MarkIndexed are found. A
// prefix no indexed string starts with has no matches. A non-positive limit
// returns nothing.
func (t *TextRegistry) IndexedWithPrefix(graph GraphReader, prefix string, limit int) ([]NodeID, error) {
	runes, err := validateText(prefix)
	if err != nil || limit <= 0 {
		return nil, err
	}

	path, _, found, pathErr := t.indexPath(graph, runes)
	if pathErr != nil || !found {
		return nil, pathErr
	}

	var matches []NodeID

	visited := map[NodeID]struct{}{path[len(path)-1]: {}}
	queue := []NodeID{path[len(path)-1]}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		children, childErr := graph.FindOutgoing(current)
		if childErr != nil {
			return nil, wrapInterfaceErr(childErr)
		}

		for _, rel := range children {
			if _, seen := visited[rel.To]; seen {
				continue
			}

			visited[rel.To] = struct{}{}

			isEntry, entryErr := graph.HasRelationship(t.allIndexEntries, rel.To)
			if entryErr != nil {
				return nil, wrapInterfaceErr(entryErr)
			}

			if isEntry {
				target, hasTarget, targetErr := singleChildTarget(graph, rel.To)
				if targetErr != nil {
					return nil, targetErr
				}
				if !hasTarget {
					return nil, ErrInvalidTextStructure
				}

				matches = append(matches, target)
				if len(matches) == limit {
					return matches, nil
				}

				continue
			}

			isNode, nodeErr := graph.HasRelationship(t.allIndexNodes, rel.To)
			if nodeErr != nil {
				return nil, wrapInterfaceErr(nodeErr)
			}
			if isNode {
				queue = append(queue, rel.To)
			}
		}
	}

	return matches, nil
}

// ---------------------------------------------------------------------
// Commit-time Checker. Local: it costs time proportional to what the
// transaction touched. A cycle longer than a self-loop in the string chain
// cannot come from a valid history; it is found by Text on every read and by
// the startup sweep (VerifyAll).

// checkTouched runs every check whose tag a touched node carries.
func (t *TextRegistry) checkTouched(g GraphReader, touched map[NodeID]struct{}) error {
	for node := range touched {
		for _, check := range t.checks {
			tagged, tagErr := g.HasRelationship(check.tag, node)
			if tagErr != nil {
				return wrapInterfaceErr(tagErr)
			}
			if !tagged {
				continue
			}

			if checkErr := check.run(g, node); checkErr != nil {
				return checkErr
			}
		}
	}

	return nil
}

// requireNoParent returns ErrInvalidTextStructure if node has a parent
// tagged with any of tags.
func requireNoParent(g GraphReader, node NodeID, tags ...NodeID) error {
	for _, tag := range tags {
		_, found, err := findUniqueTaggedParent(g, node, tag)
		if err != nil {
			return err
		}
		if found {
			return ErrInvalidTextStructure
		}
	}

	return nil
}

// checkString: the root has no tail and no first char; any other string has
// exactly one of each, and is the only child of its tail with that char.
func (t *TextRegistry) checkString(g GraphReader, q NodeID) error {
	if q == t.emptyString {
		return requireNoParent(g, q, t.allStrings, t.allStartsWithSlots)
	}

	parts, err := t.partsOf(g, q)
	if err != nil {
		return err
	}

	if parts.tail == q {
		return ErrInvalidTextStructure
	}

	owner, found, findErr := findUniqueTaggedChild(g, parts.tail, parts.slot)
	if findErr != nil {
		return findErr
	}
	if !found || owner != q {
		return ErrInvalidTextStructure
	}

	return nil
}

// checkChar: a char is not a string and has both its slots.
func (t *TextRegistry) checkChar(g GraphReader, char NodeID) error {
	isString, err := g.HasRelationship(t.allStrings, char)
	if err != nil {
		return wrapInterfaceErr(err)
	}
	if isString {
		return ErrInvalidTextStructure
	}

	_, slotsErr := t.slotsOf(g, char)

	return slotsErr
}

// checkSlot: a char slot belongs to exactly one char, as one of its two slots.
func (t *TextRegistry) checkSlot(g GraphReader, slot NodeID) error {
	char, found, err := findUniqueTaggedParent(g, slot, t.allChars)
	if err != nil {
		return err
	}
	if !found {
		return ErrInvalidTextStructure
	}

	slots, slotsErr := t.slotsOf(g, char)
	if slotsErr != nil {
		return slotsErr
	}
	if slot != slots.startsWith && slot != slots.indexStep {
		return ErrInvalidTextStructure
	}

	return nil
}

// checkIndexNode: the index root has no parent index node or step slot; any
// other index node has exactly one of each and is the only child of its
// parent for that step. No index node has two entry slots.
func (t *TextRegistry) checkIndexNode(g GraphReader, node NodeID) error {
	if _, _, entryErr := findUniqueTaggedChild(g, node, t.allIndexEntries); entryErr != nil {
		return entryErr
	}

	if node == t.indexRoot {
		return requireNoParent(g, node, t.allIndexNodes, t.allIndexStepSlots)
	}

	parent, hasParent, err := findUniqueTaggedParent(g, node, t.allIndexNodes)
	if err != nil {
		return err
	}

	step, hasStep, err := findUniqueTaggedParent(g, node, t.allIndexStepSlots)
	if err != nil {
		return err
	}

	if !hasParent || !hasStep {
		return ErrInvalidTextStructure
	}

	owner, found, findErr := findUniqueTaggedChild(g, parent, step)
	if findErr != nil {
		return findErr
	}
	if !found || owner != node {
		return ErrInvalidTextStructure
	}

	return nil
}

// checkEntry: an entry belongs to one index node, has exactly one child, a
// string that is indexed, and that string points back at this entry alone.
func (t *TextRegistry) checkEntry(g GraphReader, entry NodeID) error {
	_, hasParent, err := findUniqueTaggedParent(g, entry, t.allIndexNodes)
	if err != nil {
		return err
	}
	if !hasParent {
		return ErrInvalidTextStructure
	}

	target, hasTarget, targetErr := singleChildTarget(g, entry)
	if targetErr != nil {
		return targetErr
	}
	if !hasTarget {
		return ErrInvalidTextStructure
	}

	for _, tag := range []NodeID{t.allStrings, t.allIndexedStrings} {
		has, hasErr := g.HasRelationship(tag, target)
		if hasErr != nil {
			return wrapInterfaceErr(hasErr)
		}
		if !has {
			return ErrInvalidTextStructure
		}
	}

	owner, found, ownerErr := findUniqueTaggedParent(g, target, t.allIndexEntries)
	if ownerErr != nil {
		return ownerErr
	}
	if !found || owner != entry {
		return ErrInvalidTextStructure
	}

	return nil
}

// checkIndexedString: an indexed string is a string with exactly one entry.
func (t *TextRegistry) checkIndexedString(g GraphReader, str NodeID) error {
	isString, err := g.HasRelationship(t.allStrings, str)
	if err != nil {
		return wrapInterfaceErr(err)
	}
	if !isString {
		return ErrInvalidTextStructure
	}

	_, found, entryErr := findUniqueTaggedParent(g, str, t.allIndexEntries)
	if entryErr != nil {
		return entryErr
	}
	if !found {
		return ErrInvalidTextStructure
	}

	return nil
}
