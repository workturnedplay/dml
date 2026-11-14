//go:build ignore

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


// split_main.go is a one-off tool that splits main.go (and moves the helpers
// shared by both list types out of slotted_list.go) into one file per
// subsystem, as planned in implementation_state.md. It moves declarations
// verbatim, together with the comments in front of them, and recomputes the
// imports of every file it writes. Nothing is changed in any declaration.
//
//	go run split_main.go -batch 1 -dry    # check and report, write nothing
//	go run split_main.go -batch 1         # do it
//
// Run it from the repository root, one batch at a time.
package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"path"
	"slices"
	"strconv"
	"strings"
)

const docFile = "doc.go"

// batches are the files extracted by each run. Each batch is one commit.
var batches = [][]string{
	{docFile, "graph.go", "graph_api.go", "txn.go", "checker.go", "paging.go", "verify.go", "graph_actor.go"},
	{"bolt_view.go", "bolt_txn.go", "bolt_graph.go", "names.go", "foundational_names.go", "root_graph.go"},
	{"errors.go", "tx_helpers.go", "tagged_lookup.go", "pointer.go", "pointer_metadata.go", "capsule.go", "ordered_list.go", "list.go", "list_shared.go", "set.go"},
	{"operand_descriptor.go", "composite_set.go", "composite_set_log.go", "domain_pointer.go", "lease.go", "registries.go"},
	{"host_config.go", "host.go", "conn.go", "reconciler.go"},
}

// layout says which file owns which declaration. A key is:
//   - the name of a type, func, var or const (for a block, its first name);
//   - "Type.method" to send one method elsewhere than its type;
//   - "Iface:Impl" or "Impl" for a blank "var _ Iface = Impl" assertion.
//
// A method follows its type unless it has its own "Type.method" entry.
var layout = map[string][]string{
	"graph.go": {
		"NodeID", "Relationship", "ErrNodeNotFound", "concurrentAccessGuard", "Graph",
	},
	"graph_api.go": {
		"GraphReader", "GraphStore", "GraphAPI", "Transactor", "Tx",
		"GraphAPI:Graph", "Tx:Txn",
	},
	"txn.go": {
		"Txn", "touchNodes", "txMark", "txLog",
	},
	"checker.go": {
		"Checker", "graphCoreReader", "Graph.runCheckers", "runCheckersOver", "checkerRelevant",
	},
	"paging.go": {
		"nodePager", "pageNodeIDs", "nodesAfter", "readViewer", "readView",
		"commonChildrenFinder", "commonChildrenFinder:Graph",
		"outgoingPager", "incomingPager", "pageSorted", "errPagingUnsupported",
		"relationshipTarget", "relationshipSource", "pageIterator", "outgoingPages", "incomingPages",
	},
	"verify.go": {
		"ErrLoadVerification", "defaultVerifyPageSize", "VerifyAll",
	},
	"graph_actor.go": {
		"graphActorReentrancyDetectionEnabled", "EnableGraphActorReentrancyDetection",
		"DisableGraphActorReentrancyDetection", "currentGoroutineID",
		"GraphActor", "NewGraphActor", "actorPage",
	},
	"bolt_view.go": {
		"ErrStoreCorrupt", "ErrStoreFormat", "ErrStoreLocked", "boltBucketNodes",
		"boltIDSize", "boltFormatVersion", "boltKey8", "boltKey16", "boltID", "wrapBoltErr",
		"boltScanPrefix", "boltHasPrefix", "boltView", "newBoltView",
	},
	"bolt_txn.go": {
		"boltTxn", "newBoltTxn", "boltNameRecordSize", "encodeNameRecord", "decodeNameRecord",
	},
	"bolt_graph.go": {
		"BoltGraph", "boltOpenTimeout", "boltOpenGuardFactor", "boltOpenResult", "finishBoltOpen",
		"openBoltDB", "encodeBoltFormat", "readBoltFormat", "requireBoltFormat", "ensureBoltFormat",
		"OpenBoltGraph", "boltRead",
	},
	"names.go": {
		"ErrNameAlreadyBound", "NameRegistry", "nameState", "nameAbsent", "nameRecord",
		"nameRecordStore", "nameRecordProvider", "nameStoreOf", "NewNameRegistry",
	},
	"foundational_names.go": {
		"NameAllPointers", "FoundationalNames", "TagFoundational",
	},
	"root_graph.go": {
		"ErrCannotDeleteRoot", "ErrRootGraphOverActor", "rootReader", "rootStore", "rootTx",
		"newRootTx", "newRootStore", "RootGraph", "NewRootGraph", "sortBySource",
	},
	"errors.go": {
		"ErrNotPointer",
	},
	"tx_helpers.go": {
		"txOps", "txReader", "wrapInterfaceErr", "joinErrors", "transactValue", "transactBool",
		"tagNodeTx", "createNodeTx", "addRelationshipTx", "removeRelationshipTx", "deleteNodeTx",
		"untagAndDeleteNodeTx", "createTaggedNodeTx", "newPointerTx", "setPointerTargetTx",
		"singleChildTargetSetTx", "singleChildTargetRemoveTx",
		"requireTagged", "requireExists", "requireAllExist",
	},
	"tagged_lookup.go": {
		"singleChildTarget", "findUniqueTaggedParent", "findUniqueTaggedChild", "uniqueNode",
		"scanTaggedChild", "exactlyOneTag",
	},
	"pointer.go": {
		"PointerRegistry", "NewPointerRegistry",
	},
	"pointer_metadata.go": {
		"locateBySubjectSlot", "ensureMetadataWithSubjectSlotTx", "subjectMetadataBase",
		"PointerMetadataRegistry", "NewPointerMetadataRegistry",
		"PointerMetadataRegistryD", "NewPointerMetadataRegistryD",
	},
	"capsule.go": {
		"CapsuleRegistry", "NewCapsuleRegistry", "buildCapsuleTx", "slotOwnersOf",
		"capsulesWithValuePageSize", "forEachIncoming",
	},
	"ordered_list.go": {
		"OrderedList", "OrderedList:ListRegistry",
	},
	"list.go": {
		"ListRegistry", "NewListRegistry",
	},
	"list_shared.go": {
		"capsuleLinks", "requireNeighbour", "checkStaleLink",
		"findOwnedSlot", "checkCapsuleInChain", "validateChain", "stepBackWithin", "verifyLists",
	},
	"set.go": {
		"SetRegistry", "NewSetRegistry",
	},
	"operand_descriptor.go": {
		"sortedNodeSet", "operandDescriptorAxes", "buildOperandDescriptorTx",
		"clearOperandDescriptorEdgesTx", "deleteOperandDescriptorTx", "operandTargetGeneric",
		"operandCarriesKnownSetTag", "requireOperand", "domainContainsGeneric",
		"resolveSetOperandGeneric", "resolveOperandGeneric", "setReverseLookup",
	},
	"composite_set.go": {
		"CompositeSetRegistry", "NewCompositeSetRegistry",
	},
	"composite_set_log.go": {
		"CompositeSetLogRegistry", "NewCompositeSetLogRegistry",
	},
	"domain_pointer.go": {
		"domainConstraint", "anchorTargetFunc", "DomainPointerRegistryB", "NewDomainPointerRegistryB",
		"DomainPointerRegistryD", "NewDomainPointerRegistryD",
	},
	"lease.go": {
		"LeaseRegistry", "NewLeaseRegistry",
	},
	"registries.go": {
		"Registries", "NewRegistries",
	},
	"host_config.go": {
		"defaultKeepaliveTTL", "HostConfig", "durationOrDefault", "positiveOrDefault", "defaultHostOnError",
	},
	"host.go": {
		"ErrHostClosed", "resourcePrefix", "Effect", "Host", "OpenHost", "startHost", "finishHostStart",
	},
	"conn.go": {
		"AcquireResult", "Client", "AcquireAndWait", "Conn", "waitErr",
		"Host.Connect", "Host.watchConn", "Host.dropConn", "Host.releaseConn", "Host.closeSession",
		"Host.reapLoop", "Host.reapOnce", "Host.expiredConns",
	},
	"reconciler.go": {
		"passOutcome", "hostResource", "newHostResource",
		"Host.kickReconciler", "Host.markDirty", "Host.markDirtyNodes", "Host.takeDirty",
		"Host.clearDirty", "Host.reconcileLoop", "Host.nextWake", "Host.pendingResources",
		"Host.reconcileSet", "Host.reconcileAll", "Host.graceRemaining", "Host.failPass",
		"Host.reconcile",
	},
}

// sourceSpecs are the files declarations are taken from. Every declaration
// of a mustMap file must have an entry in layout (so none can be forgotten);
// a declaration of the other files without one simply stays.
var sourceSpecs = []struct {
	name    string
	mustMap bool
}{
	{"main.go", true},
	{"slotted_list.go", false},
}

// owner maps a layout key to its file.
var owner = buildOwners()

func buildOwners() map[string]string {
	owners := make(map[string]string)

	for file, keys := range layout {
		for _, key := range keys {
			if previous, duplicate := owners[key]; duplicate {
				panic(fmt.Sprintf("layout: %q is assigned to both %s and %s", key, previous, file))
			}

			owners[key] = file
		}
	}

	return owners
}

type importSpec struct {
	alias string
	path  string
}

// local is the name the import is used by in code.
func (i importSpec) local() string {
	if i.alias != "" {
		return i.alias
	}

	return path.Base(i.path)
}

func (i importSpec) String() string {
	if i.alias != "" {
		return i.alias + " " + strconv.Quote(i.path)
	}

	return strconv.Quote(i.path)
}

// segment is one top-level declaration with the text in front of it (its doc
// comment and any free-standing comment), cut verbatim from the source.
type segment struct {
	label   string
	target  string
	text    string
	imports []importSpec
}

type sourceFile struct {
	name     string
	mustMap  bool
	header   string
	pkgDoc   string
	segments []segment
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "split_main:", err)
		os.Exit(1)
	}
}

func run() error {
	batch := flag.Int("batch", 0, "batch number to extract")
	dry := flag.Bool("dry", false, "check and report only; write nothing")
	flag.Parse()

	if *batch < 1 || *batch > len(batches) {
		return fmt.Errorf("-batch must be between 1 and %d", len(batches))
	}

	if err := checkLayout(); err != nil {
		return err
	}

	selected := make(map[string]bool)
	for _, file := range batches[*batch-1] {
		selected[file] = true
	}

	sources := make([]*sourceFile, 0, len(sourceSpecs))

	for _, spec := range sourceSpecs {
		parsed, err := parseSource(spec.name, spec.mustMap)
		if err != nil {
			return err
		}

		sources = append(sources, parsed)
	}

	if err := checkMapped(sources); err != nil {
		return err
	}

	primary := sources[0]
	moved := make(map[string][]segment)
	changed := make(map[string]bool)

	for _, src := range sources {
		var kept []segment

		for _, seg := range src.segments {
			if selected[seg.target] {
				moved[seg.target] = append(moved[seg.target], seg)
				changed[src.name] = true

				continue
			}

			kept = append(kept, seg)
		}

		src.segments = kept
	}

	docMoved := selected[docFile] && primary.pkgDoc != ""
	if docMoved {
		changed[primary.name] = true
	}

	created := make(map[string]string)

	if docMoved {
		content, err := formatSource(primary.header + "\n\n" + primary.pkgDoc + "\n")
		if err != nil {
			return err
		}

		created[docFile] = content
	}

	for _, file := range slices.Sorted(maps.Keys(moved)) {
		content, err := render(primary.header, "package dml", moved[file])
		if err != nil {
			return fmt.Errorf("%s: %w", file, err)
		}

		created[file] = content
	}

	rewritten := make(map[string]string)

	var removed []string

	for _, src := range sources {
		if !changed[src.name] {
			continue
		}

		clause := "package dml"
		keepDoc := src.pkgDoc != "" && (src != primary || !docMoved)

		if keepDoc {
			clause = src.pkgDoc
		}

		if len(src.segments) == 0 && !keepDoc {
			removed = append(removed, src.name)
			continue
		}

		content, err := render(primary.header, clause, src.segments)
		if err != nil {
			return fmt.Errorf("%s: %w", src.name, err)
		}

		rewritten[src.name] = content
	}

	for _, file := range slices.Sorted(maps.Keys(created)) {
		if _, statErr := os.Stat(file); statErr == nil {
			return fmt.Errorf("%s already exists; refusing to overwrite it", file)
		}
	}

	report(created, moved, sources, removed)

	if *dry {
		fmt.Println("\ndry run: nothing was written")
		return nil
	}

	for _, file := range slices.Sorted(maps.Keys(created)) {
		if err := os.WriteFile(file, []byte(created[file]), 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", file, err)
		}
	}

	for _, file := range slices.Sorted(maps.Keys(rewritten)) {
		if err := os.WriteFile(file, []byte(rewritten[file]), 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", file, err)
		}
	}

	for _, file := range removed {
		if err := os.Remove(file); err != nil {
			return fmt.Errorf("removing %s: %w", file, err)
		}
	}

	return nil
}

// checkLayout makes sure the batches and the layout table agree.
func checkLayout() error {
	inBatch := make(map[string]bool)

	for _, batch := range batches {
		for _, file := range batch {
			inBatch[file] = true
		}
	}

	for file := range layout {
		if !inBatch[file] {
			return fmt.Errorf("layout file %s is in no batch", file)
		}
	}

	for file := range inBatch {
		if _, known := layout[file]; !known && file != docFile {
			return fmt.Errorf("batch file %s has no layout entry", file)
		}
	}

	return nil
}

// checkMapped fails if a declaration of a mustMap source has no layout entry.
func checkMapped(sources []*sourceFile) error {
	var unmapped []string

	for _, src := range sources {
		if !src.mustMap {
			continue
		}

		for _, seg := range src.segments {
			if seg.target == "" {
				unmapped = append(unmapped, src.name+": "+seg.label)
			}
		}
	}

	if len(unmapped) > 0 {
		return fmt.Errorf("declarations with no entry in the layout table:\n  %s", strings.Join(unmapped, "\n  "))
	}

	return nil
}

func report(created map[string]string, moved map[string][]segment, sources []*sourceFile, removed []string) {
	for _, file := range slices.Sorted(maps.Keys(created)) {
		segs := moved[file]
		fmt.Printf("%s (%d declarations)\n", file, len(segs))

		for _, seg := range segs {
			fmt.Printf("    %s\n", seg.label)
		}
	}

	for _, src := range sources {
		fmt.Printf("%s keeps %d declarations\n", src.name, len(src.segments))
	}

	for _, file := range removed {
		fmt.Printf("%s is empty and is removed\n", file)
	}
}

func parseSource(name string, mustMap bool) (*sourceFile, error) {
	src, err := os.ReadFile(name)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", name, err)
	}

	fset := token.NewFileSet()

	file, err := parser.ParseFile(fset, name, src, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", name, err)
	}

	handle := fset.File(file.Pos())
	offset := func(pos token.Pos) int { return handle.Offset(pos) }

	if len(file.Comments) == 0 || file.Comments[0] == file.Doc {
		return nil, fmt.Errorf("%s: no license header comment before the package comment", name)
	}

	result := &sourceFile{
		name:    name,
		mustMap: mustMap,
		header:  string(src[offset(file.Comments[0].Pos()):offset(file.Comments[0].End())]),
	}

	if file.Doc != nil {
		result.pkgDoc = string(src[offset(file.Doc.Pos()):offset(file.Name.End())])
	}

	byName := make(map[string]importSpec)
	importsEnd := offset(file.Name.End())

	var decls []ast.Decl

	for _, decl := range file.Decls {
		gen, isGen := decl.(*ast.GenDecl)
		if !isGen || gen.Tok != token.IMPORT {
			decls = append(decls, decl)
			continue
		}

		importsEnd = max(importsEnd, offset(gen.End()))

		for _, spec := range gen.Specs {
			imp, isImport := spec.(*ast.ImportSpec)
			if !isImport {
				continue
			}

			importPath, unquoteErr := strconv.Unquote(imp.Path.Value)
			if unquoteErr != nil {
				return nil, fmt.Errorf("%s: import %s: %w", name, imp.Path.Value, unquoteErr)
			}

			found := importSpec{path: importPath}
			if imp.Name != nil {
				found.alias = imp.Name.Name
			}

			byName[found.local()] = found
		}
	}

	previous := importsEnd

	for i, decl := range decls {
		end := offset(decl.End())
		if i == len(decls)-1 {
			end = len(src) // trailing comments travel with the last declaration
		}

		label, target := classify(decl)

		result.segments = append(result.segments, segment{
			label:   label,
			target:  target,
			text:    strings.TrimSpace(string(src[previous:end])),
			imports: usedImports(decl, byName),
		})

		previous = end
	}

	return result, nil
}

// classify returns a label for decl and the file that owns it ("" if the
// layout table does not say).
func classify(decl ast.Decl) (label, target string) {
	switch d := decl.(type) {
	case *ast.FuncDecl:
		if d.Recv == nil || len(d.Recv.List) == 0 {
			return d.Name.Name, owner[d.Name.Name]
		}

		recv := typeName(d.Recv.List[0].Type)
		label = recv + "." + d.Name.Name

		if file, own := owner[label]; own {
			return label, file
		}

		return label, owner[recv]
	case *ast.GenDecl:
		if len(d.Specs) == 0 {
			return "", ""
		}

		switch spec := d.Specs[0].(type) {
		case *ast.TypeSpec:
			return spec.Name.Name, owner[spec.Name.Name]
		case *ast.ValueSpec:
			if spec.Names[0].Name != "_" {
				return spec.Names[0].Name, owner[spec.Names[0].Name]
			}

			return classifyAssertion(spec)
		}
	}

	return "", ""
}

// classifyAssertion handles "var _ Iface = Impl": "Iface:Impl" if the layout
// has it, otherwise the file that owns Impl.
func classifyAssertion(spec *ast.ValueSpec) (label, target string) {
	if len(spec.Values) == 0 {
		return "_", ""
	}

	iface := typeName(spec.Type)
	impl := valueTypeName(spec.Values[0])
	label = "_ " + iface + " = " + impl

	if file, own := owner[iface+":"+impl]; own {
		return label, file
	}

	return label, owner[impl]
}

// typeName is the name of a type expression, without pointer or type
// parameters.
func typeName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return typeName(t.X)
	case *ast.ParenExpr:
		return typeName(t.X)
	case *ast.IndexExpr:
		return typeName(t.X)
	case *ast.IndexListExpr:
		return typeName(t.X)
	default:
		return ""
	}
}

// valueTypeName is the type of "T{}" or "(*T)(nil)".
func valueTypeName(expr ast.Expr) string {
	switch v := expr.(type) {
	case *ast.CompositeLit:
		return typeName(v.Type)
	case *ast.CallExpr:
		if paren, isParen := v.Fun.(*ast.ParenExpr); isParen {
			return typeName(paren.X)
		}
	}

	return ""
}

// usedImports returns the imports decl refers to. It looks only at
// "name.Selector" expressions, so it can include an import that a local
// variable of the same name merely shadows; the compiler (or goimports)
// reports that as an unused import, which is then deleted by hand.
func usedImports(decl ast.Decl, byName map[string]importSpec) []importSpec {
	found := make(map[string]struct{})

	ast.Inspect(decl, func(node ast.Node) bool {
		sel, isSel := node.(*ast.SelectorExpr)
		if !isSel {
			return true
		}

		ident, isIdent := sel.X.(*ast.Ident)
		if !isIdent {
			return true
		}

		if _, known := byName[ident.Name]; known {
			found[ident.Name] = struct{}{}
		}

		return true
	})

	used := make([]importSpec, 0, len(found))
	for name := range found {
		used = append(used, byName[name])
	}

	return used
}

// importBlock renders the union of the imports of segs, standard library
// first.
func importBlock(segs []segment) string {
	set := make(map[importSpec]struct{})

	for _, seg := range segs {
		for _, imp := range seg.imports {
			set[imp] = struct{}{}
		}
	}

	if len(set) == 0 {
		return ""
	}

	var std, other []importSpec

	for imp := range set {
		first, _, _ := strings.Cut(imp.path, "/")
		if strings.Contains(first, ".") {
			other = append(other, imp)
		} else {
			std = append(std, imp)
		}
	}

	byPath := func(a, b importSpec) int { return strings.Compare(a.path, b.path) }
	slices.SortFunc(std, byPath)
	slices.SortFunc(other, byPath)

	var b strings.Builder

	b.WriteString("import (\n")

	for _, imp := range std {
		b.WriteString("\t" + imp.String() + "\n")
	}

	if len(std) > 0 && len(other) > 0 {
		b.WriteString("\n")
	}

	for _, imp := range other {
		b.WriteString("\t" + imp.String() + "\n")
	}

	b.WriteString(")")

	return b.String()
}

// render assembles one file: header, package clause, imports, declarations.
func render(header, clause string, segs []segment) (string, error) {
	var b strings.Builder

	b.WriteString(header)
	b.WriteString("\n\n")
	b.WriteString(clause)
	b.WriteString("\n")

	if block := importBlock(segs); block != "" {
		b.WriteString("\n")
		b.WriteString(block)
		b.WriteString("\n")
	}

	for _, seg := range segs {
		b.WriteString("\n")
		b.WriteString(seg.text)
		b.WriteString("\n")
	}

	return formatSource(b.String())
}

func formatSource(source string) (string, error) {
	formatted, err := format.Source([]byte(source))
	if err != nil {
		return "", fmt.Errorf("formatting: %w", err)
	}

	return string(formatted), nil
}
