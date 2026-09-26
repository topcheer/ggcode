package agent

// Concurrent Map Access Detection in Go Code
//
// Problem: AI coding agents frequently generate Go code that accesses maps
// from multiple goroutines without proper synchronization. Go's runtime
// detects this at runtime with a fatal crash:
//
//	fatal error: concurrent map writes
//	fatal error: concurrent map read and map write
//
// These crashes are non-recoverable (they abort the entire process) and are
// among the most common production incidents in Go services. Unlike data
// races (which require -race flag to detect), concurrent map access crashes
// happen unconditionally whenever the timing aligns.
//
// Common LLM failure modes this check catches:
//  1. Map declared as struct field, written in one goroutine, read in another
//     without a mutex: `go s.process()` where process reads/writes s.items
//  2. Map accessed inside a goroutine spawned by a method that also modifies
//     the same map: `go func() { m[key] = val }()` without sync
//  3. Map passed to a goroutine and concurrently modified by the caller
//  4. Map used as a cache shared across goroutines with no synchronization
//
// Competitor analysis:
//   - Claude Code: no automatic detection (relies on external -race testing)
//   - Cursor: go vet does NOT catch this (it only checks for some race patterns)
//   - Cline/OpenHands: no detection
//   - Aider: no detection
//   - GitHub Copilot: no automatic detection
//
// go vet does NOT detect concurrent map access. The -race detector catches
// it at runtime but only if the concurrent path is actually exercised during
// testing. staticcheck does not check this either.
//
// Approach: Heuristic AST-based analysis. For each function, if it (a) spawns
// goroutines via `go` statements AND (b) has map read/write operations on the
// same map variable, AND (c) does NOT use sync primitives (Mutex, RWMutex,
// sync.Map) to protect those accesses, flag as a potential concurrent map
// access. This is conservative: it only flags within a single function scope
// where both the goroutine spawn and map access coexist without sync.
//
// False positive mitigation:
//   - Channels: if the map is only accessed through channel-mediated patterns,
//     we skip (hard to detect precisely, but we check for obvious sync usage)
//   - sync.Map: explicitly excluded (designed for concurrent use)
//   - Functions with Mutex/RWMutex/Locker calls are excluded
//   - Delta-aware: only flags patterns newly introduced by this edit

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

// concurrentMapInstance represents a potential concurrent map access.
type concurrentMapInstance struct {
	posStr   string // human-readable position
	fp       string // content fingerprint: funcName|mapName (delta key)
	mapName  string // the map variable name
	goCalled bool   // whether this is in a goroutine context
}

// checkConcurrentMapAccess detects potential concurrent map access in Go code
// where maps are accessed both directly and inside spawned goroutines without
// synchronization. Delta-aware: only flags NEW instances.
func checkConcurrentMapAccess(filePath, oldContent, newContent string) string {
	if filepath.Ext(filePath) != ".go" || strings.TrimSpace(newContent) == "" {
		return ""
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filePath, newContent, 0)
	if err != nil || file == nil {
		return ""
	}

	instances := findConcurrentMapAccess(fset, file)
	if len(instances) == 0 {
		return ""
	}

	// Delta check: compare against old content positions (fix #140/#142).
	var oldLines map[string]bool
	if strings.TrimSpace(oldContent) != "" {
		for _, iss := range findConcurrentMapAccess(token.NewFileSet(), func() *ast.File {
			// Parse with the SAME filePath as the new content (#220): parsing
			// with "" made posStr keys "file.go:N" vs ".:N" — never equal,
			// so the skip below was dead code and every edit re-reported
			// pre-existing patterns.
			f, _ := parser.ParseFile(token.NewFileSet(), filePath, oldContent, 0)
			return f
		}()) {
			if oldLines == nil {
				oldLines = make(map[string]bool)
			}
			oldLines[iss.fp] = true
		}
	}

	var b strings.Builder
	b.WriteString("[Concurrent map access detection] Potential unsynchronized concurrent map access detected.\n")
	b.WriteString("Go maps are NOT safe for concurrent use - the runtime will fatally crash with 'concurrent map read/write'.\n")
	reported := 0
	for _, inst := range instances {
		if oldLines != nil && oldLines[inst.fp] {
			continue
		}
		b.WriteString(fmt.Sprintf("  - %s: map '%s' is accessed in a function that spawns goroutines without sync (Mutex/RWMutex/sync.Map). ",
			inst.posStr, inst.mapName))
		b.WriteString("Protect with sync.RWMutex, or use sync.Map for concurrent access patterns.\n")
		reported++
	}
	if reported == 0 {
		// Everything was pre-existing: emit nothing rather than a bare
		// header — callers treat any non-empty string as a warning.
		return ""
	}
	return b.String()
}

// findConcurrentMapAccess performs heuristic detection of potential concurrent
// map access within each function. A function is flagged if:
//  1. It contains at least one `go` statement (goroutine spawn)
//  2. It has map write operations (m[k] = v, delete(m, k))
//  3. It does NOT declare or use sync primitives (Mutex, RWMutex, sync.Map)
//
// We focus on map WRITES because those are the ones that cause fatal crashes.
// Map reads combined with concurrent writes also crash, but if we detect the
// write pattern we already flag it.
func findConcurrentMapAccess(fset *token.FileSet, file *ast.File) []concurrentMapInstance {
	var instances []concurrentMapInstance

	// #1533-A: package-level `var counters map[string]int` is the canonical
	// cross-goroutine shared-registry shape - the top-level GenDecl loop used
	// to be skipped entirely (only FuncDecls were analyzed), so the detector's
	// headline scenario went silent after #1445 required declaration proof.
	pkgMaps := map[string]bool{}
	for _, topDecl := range file.Decls {
		gd, ok := topDecl.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR {
			continue
		}
		for _, spec := range gd.Specs {
			if vs, ok := spec.(*ast.ValueSpec); ok {
				mapNamesInValueSpec(vs, pkgMaps)
			}
		}
	}

	for _, topDecl := range file.Decls {
		fn, ok := topDecl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}

		// Collect function-level info (#1445-A: the FuncDecl seeds map-typed
		// params/receivers into the declaration-proof set internally).
		info := analyzeFuncForMapConcurrency(fn, pkgMaps)
		if len(info.unsyncMapWrites) == 0 || !info.hasGoStatement {
			continue
		}

		// Flag each unsynchronized map write.
		for mapName, pos := range info.unsyncMapWrites {
			p := fset.Position(pos)
			instances = append(instances, concurrentMapInstance{
				posStr:   fmt.Sprintf("%s:%d", filepath.Base(p.Filename), p.Line),
				fp:       fn.Name.Name + "|" + mapName,
				mapName:  mapName,
				goCalled: true,
			})
		}
	}

	return instances
}

// mapConcurrencyInfo holds analysis results for a function body.
type mapConcurrencyInfo struct {
	hasGoStatement  bool
	hasSync         bool                 // uses Mutex, RWMutex, sync.Map, or Locker
	unsyncMapWrites map[string]token.Pos // map var name -> position of first write
	mapDeclared     map[string]bool      // #1445-A: names PROVEN to be maps
}

// analyzeFuncForMapConcurrency inspects a function for concurrent map
// access patterns (#1445-A: takes the FuncDecl so map-typed params and
// receivers seed the declaration-proof set BEFORE the write scan).
// // r110 decomposition (behavior-preserving):
//  1. seed proof: package-level maps + map-typed params/receivers
//  2. pass 1: collectDeclarationProof — names PROVEN to be maps
//  3. pass 2: scanWritesAndSync — go statements, sync evidence, writes
//  4. finalize: any sync evidence clears all pending writes
func analyzeFuncForMapConcurrency(fn *ast.FuncDecl, pkgMaps map[string]bool) mapConcurrencyInfo {
	info := mapConcurrencyInfo{
		unsyncMapWrites: make(map[string]token.Pos),
		mapDeclared:     make(map[string]bool),
	}
	// #1533-A: package-level maps are proof BEFORE the inspect pass - the
	// write detection consults mapDeclared while walking, so seeding after
	// the call (as an earlier draft did) was a no-op.
	for name := range pkgMaps {
		info.mapDeclared[name] = true
	}
	// Params/receiver declared as maps are proof (func worker(m map...)).
	seedMapTypedFields(fn.Type.Params, info.mapDeclared)
	seedMapTypedFields(fn.Recv, info.mapDeclared)

	// #1445-A pass 1: collect names PROVEN to be maps - the old check
	// counted ANY indexed assignment (`out[i] = v` on a slice, `arr[0]`,
	// struct-slice rows) as a map write, so the extremely common fan-out
	// pattern (parallel goroutines filling a preallocated slice) fired
	// "map 'out' accessed without sync" on an object that was never a map
	// (the #511 tombstone lesson repeating). Without go/types we prove
	// map-ness from the declaration shapes: make(map[...]), var x map[...],
	// or a map composite literal.
	info.collectDeclarationProof(fn.Body)

	// Pass 2: goroutine spawns, sync evidence, unsynchronized map writes.
	info.scanWritesAndSync(fn.Body)

	// If the function uses sync primitives, clear the warnings.
	if info.hasSync {
		info.unsyncMapWrites = make(map[string]token.Pos)
	}

	return info
}

// assignWriteTokens lists every assignment token that stores into its LHS.
// #1533-C: compound assignments (m[k] += v etc.) count alongside = and :=.
var assignWriteTokens = map[token.Token]bool{
	token.ASSIGN: true, token.DEFINE: true,
	token.ADD_ASSIGN: true, token.SUB_ASSIGN: true,
	token.MUL_ASSIGN: true, token.QUO_ASSIGN: true, token.REM_ASSIGN: true,
	token.AND_ASSIGN: true, token.OR_ASSIGN: true, token.XOR_ASSIGN: true,
	token.SHL_ASSIGN: true, token.SHR_ASSIGN: true, token.AND_NOT_ASSIGN: true,
}

// seedMapTypedFields records every map-typed name in a field list (params
// or receiver) as proven map (func worker(m map[string]int)).
func seedMapTypedFields(fl *ast.FieldList, declared map[string]bool) {
	if fl == nil {
		return
	}
	for _, fld := range fl.List {
		if _, isMap := fld.Type.(*ast.MapType); isMap {
			for _, name := range fld.Names {
				declared[name.Name] = true
			}
		}
	}
}

// mapNamesInValueSpec records map-typed or map-initialized names from one
// var ValueSpec. Shared by the package-level scan (findConcurrentMapAccess)
// and the in-function proof pass - one copy prevents the two proof paths
// from drifting (the #1533-B gap was exactly such drift).
func mapNamesInValueSpec(vs *ast.ValueSpec, declared map[string]bool) {
	if _, isMap := vs.Type.(*ast.MapType); isMap {
		for _, name := range vs.Names {
			declared[name.Name] = true
		}
		return
	}
	for i, val := range vs.Values {
		if isMapValuedExpr(val) && i < len(vs.Names) {
			declared[vs.Names[i].Name] = true
		}
	}
}

// collectDeclarationProof is pass 1 (#1445-A): walk the body and record
// every name whose declaration shape proves it holds a map.
func (m *mapConcurrencyInfo) collectDeclarationProof(body ast.Node) {
	ast.Inspect(body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.AssignStmt:
			for _, rhs := range node.Rhs {
				if isMapValuedExpr(rhs) {
					for _, lhs := range node.Lhs {
						if id, ok := lhs.(*ast.Ident); ok {
							m.mapDeclared[id.Name] = true
						}
					}
				}
			}
		case *ast.DeclStmt:
			// #1533-B: `var m = make(map[string]int)` and
			// `var m = map[string]int{}` fall between the DeclStmt
			// (Type-only) and AssignStmt (isMapValuedExpr) branches -
			// the idiomatic inferred declaration was never proven.
			if d, ok := node.Decl.(*ast.GenDecl); ok && d.Tok == token.VAR {
				for _, spec := range d.Specs {
					if vs, ok := spec.(*ast.ValueSpec); ok {
						mapNamesInValueSpec(vs, m.mapDeclared)
					}
				}
			}
		}
		return true
	})
}

// scanWritesAndSync is pass 2: goroutine spawns, mutual-exclusion evidence,
// and unsynchronized map writes (delete / index-assign / index-incdec).
func (m *mapConcurrencyInfo) scanWritesAndSync(body ast.Node) {
	ast.Inspect(body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.GoStmt:
			m.hasGoStatement = true
		case *ast.SelectorExpr:
			m.noteSyncSelector(node)
		case *ast.CallExpr:
			m.noteCallEvidence(node)
		case *ast.IncDecStmt:
			// #1533-C: m[k]++ (concurrent counters are a top real-world
			// concurrent-map crash shape) - IncDecStmt is NOT an AssignStmt,
			// so it escaped the switch entirely.
			if idx, ok := node.X.(*ast.IndexExpr); ok {
				m.noteProvenMapWrite(idx.X, node.Pos())
			}
		case *ast.AssignStmt:
			m.noteAssignWrites(node)
		}
		return true
	})
}

// noteSyncSelector detects sync.Map/Mutex/RWMutex/Locker usage. Only
// MUTUAL-EXCLUSION or concurrent-safe-map types count (#218): WaitGroup /
// Once/Pool provide no map protection — counting them cleared all warnings
// for the most common fan-out crash pattern (goroutines writing a map
// under wg.Add/Done).
func (m *mapConcurrencyInfo) noteSyncSelector(node *ast.SelectorExpr) {
	ident, ok := node.X.(*ast.Ident)
	if !ok || ident.Name != "sync" || node.Sel == nil {
		return
	}
	switch node.Sel.Name {
	case "Map", "Mutex", "RWMutex", "Locker":
		m.hasSync = true
	}
}

// noteCallEvidence inspects a call expression for sync evidence (Lock /
// Unlock/RLock/RUnlock/TryLock/TryRLock method calls, or a bare Lock
// identifier) and for the delete(m, k) map-write form. delete requires a
// map operand to compile, so no declaration proof is needed there.
func (m *mapConcurrencyInfo) noteCallEvidence(node *ast.CallExpr) {
	if sel, ok := node.Fun.(*ast.SelectorExpr); ok && sel.Sel != nil {
		switch sel.Sel.Name {
		case "Lock", "Unlock", "RLock", "RUnlock", "TryLock", "TryRLock":
			m.hasSync = true
		}
	}
	if ident, ok := node.Fun.(*ast.Ident); ok {
		switch ident.Name {
		case "Lock":
			m.hasSync = true
		case "delete":
			if len(node.Args) > 0 {
				if mapName := mapVarName(node.Args[0]); mapName != "" {
					m.recordMapWrite(mapName, node.Pos())
				}
			}
		}
	}
}

// noteAssignWrites records m[k] = v (and compound forms) as map writes.
func (m *mapConcurrencyInfo) noteAssignWrites(node *ast.AssignStmt) {
	if !assignWriteTokens[node.Tok] {
		return
	}
	for _, lhs := range node.Lhs {
		if idx, ok := lhs.(*ast.IndexExpr); ok {
			// #1445-A: plain identifiers need declaration proof (a
			// slice out[i]=v is not a map write); struct-field
			// selectors keep the conservative old behavior (the
			// field's type lives outside this function).
			m.noteProvenMapWrite(idx.X, node.Pos())
		}
	}
}

// noteProvenMapWrite records the first write position for a base expression
// that is plausibly a map: dotted names (s.items) pass conservatively
// without proof (the field's type lives outside this function); plain
// identifiers require proof (#1445-A).
func (m *mapConcurrencyInfo) noteProvenMapWrite(base ast.Expr, pos token.Pos) {
	name := mapVarName(base)
	if name == "" || (!strings.Contains(name, ".") && !m.mapDeclared[name]) {
		return
	}
	m.recordMapWrite(name, pos)
}

// recordMapWrite keeps only the first write position per map name.
func (m *mapConcurrencyInfo) recordMapWrite(name string, pos token.Pos) {
	if _, exists := m.unsyncMapWrites[name]; !exists {
		m.unsyncMapWrites[name] = pos
	}
}

// mapVarName extracts the variable name from a map expression, handling
// simple identifiers (m), selector expressions (s.items), and returning
// empty string for complex expressions.
func mapVarName(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		if base, ok := e.X.(*ast.Ident); ok {
			return base.Name + "." + e.Sel.Name
		}
		return ""
	default:
		return ""
	}
}

// isMapValuedExpr reports whether an expression yields a map: a
// make(map[...]) call, a map type conversion, or a map composite literal
// (#1445-A declaration proof, pass 1).
func isMapValuedExpr(e ast.Expr) bool {
	switch v := e.(type) {
	case *ast.CallExpr:
		if id, ok := v.Fun.(*ast.Ident); ok && id.Name == "make" && len(v.Args) > 0 {
			if _, isMap := v.Args[0].(*ast.MapType); isMap {
				return true
			}
		}
	case *ast.CompositeLit:
		if _, isMap := v.Type.(*ast.MapType); isMap {
			return true
		}
	case *ast.ParenExpr:
		return isMapValuedExpr(v.X)
	}
	return false
}
