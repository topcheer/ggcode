package agent

// Post-Write Map Preallocation Detection (Check #57)
//
// Trend: Memory Allocation Optimization - Map Growth & Rehashing Cost
//
// Problem: AI coding agents generate code like:
//
//	m := make(map[K]V)
//	for _, item := range items {
//	    m[item.Key] = item.Value
//	}
//
// Without a size hint, the Go runtime starts with a small hash table and
// rehashes (rebuilds) it multiple times as entries are added. Each rehash
// is O(N) and causes a burst of allocations. For N=10000, this means ~13
// rehashes and significant GC churn. The fix is trivial:
// make(map[K]V, len(items)) allocates the right-sized table once.
//
// Competitor analysis:
//   - gocritic: no map preallocation check
//   - staticcheck: no map preallocation check
//   - go vet: does not flag this pattern
//   - prealloc linter: only covers slices, not maps
//   - Claude Code / Cursor / Cline / OpenHands / Aider: no detection
//
// Detection approach: AST-based, delta-aware, function-scoped. Find for/range
// loops where:
//  1. A map is declared via make(map[K]V) without a size hint (single arg)
//     inside the same function unit as the loop
//  2. Inside the loop body, the map is written to (map[key] = value)
//  3. The range expression iterates a slice/array whose len() is known
//     at declaration time (same variable name or make immediately before loop)
//
// False positive avoidance:
//   - Skip if make already has a second argument (size hint present)
//   - Skip if the loop body has a conditional (if/guard) that might skip entries
//   - Skip if the range source is a channel (unknown size)
//   - Skip test files
//   - #1103: skip when the hintless map declaration belongs to a different
//     function than the loop. Each function unit is scanned against only its
//     own declarations, so a same-named slice populated by a loop in another
//     function can no longer be mistaken for a cross-file-scope map write.
//   - #1121: skip package-level var maps entirely when assembling candidate
//     declarations. Such maps are registries/caches accumulated across many
//     calls, so no single loop's len(source) is a meaningful size hint and
//     per-loop "add a size hint" advice cannot be implemented there.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

// mapPreallocWarning represents a single map preallocation warning.
type mapPreallocWarning struct {
	varName   string
	loopLine  int
	sourceLen string // expression whose len() should be used as hint
}

func (w mapPreallocWarning) String() string {
	return fmt.Sprintf("map %q populated in loop without size hint", w.varName)
}

// checkMapPrealloc detects maps created without a size hint that are then
// populated from a known-size source inside a for/range loop.
func checkMapPrealloc(filePath, oldContent, newContent string) []string {
	if !strings.HasSuffix(filePath, ".go") {
		return nil
	}
	if strings.HasSuffix(filePath, "_test.go") {
		return nil
	}
	if strings.TrimSpace(newContent) == "" {
		return nil
	}

	fset := token.NewFileSet()
	newAST, err := parser.ParseFile(fset, filePath, newContent, 0)
	if err != nil {
		return nil
	}

	newPatterns := findMissingMapPrealloc(newAST, fset)
	if len(newPatterns) == 0 {
		return nil
	}

	// Delta: subtract patterns already present in old content.
	if strings.TrimSpace(oldContent) != "" {
		oldAST, _ := parser.ParseFile(token.NewFileSet(), filePath, oldContent, 0)
		if oldAST != nil {
			oldPatterns := findMissingMapPrealloc(oldAST, token.NewFileSet())
			if len(oldPatterns) > 0 {
				oldSet := make(map[string]bool)
				for _, p := range oldPatterns {
					oldSet[p.String()] = true
				}
				var delta []mapPreallocWarning
				for _, p := range newPatterns {
					if !oldSet[p.String()] {
						delta = append(delta, p)
					}
				}
				newPatterns = delta
			}
		}
	}

	if len(newPatterns) == 0 {
		return nil
	}

	var warnings []string
	for i, p := range newPatterns {
		if i >= 3 {
			warnings = append(warnings, fmt.Sprintf("...and %d more map preallocation warning(s)", len(newPatterns)-3))
			break
		}
		warnings = append(warnings, fmt.Sprintf(
			"Map preallocation: %q created with make(map[K]V) (no size hint) and populated inside a loop at %s:%d. "+
				"Each growth triggers a full rehash (O(N) per rehash, ~log(N) rehashes for N entries). "+
				"Pre-allocate with make(map[K]V, %s) to eliminate intermediate rehashes and reduce GC pressure.",
			p.varName, filepath.Base(filePath), p.loopLine, p.sourceLen))
	}
	return warnings
}

// mapDeclInfo tracks a map variable declared without a size hint.
type mapDeclInfo struct {
	name string
	pos  token.Pos
}

// findMissingMapPrealloc scans the AST for map-population-in-loop patterns
// where the map lacks a size hint.
//
// #1103: analysis is performed per function unit (top-level FuncDecl and
// nested FuncLit). A unit only correlates loop writes against declarations
// reachable from that unit - its own locals - so declarations in sibling
// functions never satisfy a loop in another one.
// #1121: package-level var maps are excluded from candidates entirely;
// registry/cache accumulation semantics make a one-shot size hint wrong.
func findMissingMapPrealloc(file *ast.File, fset *token.FileSet) []mapPreallocWarning {
	var warnings []mapPreallocWarning

	// scanUnit checks one function body: declarations and loops are both
	// taken from the same lexical region.
	scanUnit := func(body *ast.BlockStmt, fnType *ast.FuncType) {
		// Visible declarations: this unit's own local make(map[K]V)
		// bindings only (#1121: package-level var maps are not warned).
		binds := make(map[string]*mapDeclInfo, 4)
		collectLocalMapBinds(body, binds)
		if fnType != nil {
			excludeShadowedBinds(fnType.Params, body, binds)
		} else {
			excludeShadowedBinds(nil, body, binds)
		}

		// One warning per variable name within a unit, mirroring the
		// historical per-file suppression.
		unitWarned := make(map[string]bool)

		onLoop := func(loopBody *ast.BlockStmt, loopPos token.Pos, sourceName string, knownRange bool) {
			for _, m := range scanForMapWrite(loopBody, loopPos, binds) {
				if unitWarned[m] {
					continue
				}
				unitWarned[m] = true
				hint := "expectedSize"
				if knownRange && sourceName != "" {
					hint = "len(" + sourceName + ")"
				}
				warnings = append(warnings, mapPreallocWarning{
					varName:   m,
					loopLine:  fset.Position(loopPos).Line,
					sourceLen: hint,
				})
			}
		}
		scanUnitLoops(body, onLoop)
	}

	ast.Inspect(file, func(n ast.Node) bool {
		switch fn := n.(type) {
		case *ast.FuncDecl:
			if fn.Body != nil {
				scanUnit(fn.Body, fn.Type)
			}
		case *ast.FuncLit:
			if fn.Body != nil {
				scanUnit(fn.Body, fn.Type)
			}
		}
		return true
	})

	return warnings
}

// collectLocalMapBinds adds hintless map bindings declared by short defines
// and var declarations directly inside a function unit body. Function
// literals within the body constitute their own units; descending into them
// would leak closure-internal names outward (issue #1103), so that branch is
// pruned.
func collectLocalMapBinds(body *ast.BlockStmt, binds map[string]*mapDeclInfo) {
	ast.Inspect(body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.FuncLit:
			return false // nested closure: analyzed as its own unit
		case *ast.GenDecl:
			if node.Tok != token.VAR {
				return true
			}
			for _, spec := range node.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, name := range vs.Names {
					if i >= len(vs.Values) {
						continue
					}
					if info := analyzeMapInit(name.Name, name.Pos(), vs.Values[i]); info != nil {
						binds[name.Name] = info
					}
				}
			}
		case *ast.AssignStmt:
			if node.Tok != token.DEFINE {
				return true
			}
			for i, lhs := range node.Lhs {
				ident, ok := lhs.(*ast.Ident)
				if !ok || i >= len(node.Rhs) {
					continue
				}
				if info := analyzeMapInit(ident.Name, ident.Pos(), node.Rhs[i]); info != nil {
					binds[ident.Name] = info
				}
			}
		}
		return true
	})
}

// excludeShadowedBinds drops candidate bindings whose bare name can refer to
// a different value inside the unit: a parameter shadows same-named bindings
// collected from inner blocks, and a same-name assignment of an already-sized
// make(map[K]V, n) means the write belongs to the sized variable (issue
// #1103 conservatism - under-detection is preferred over a harmful
// slice-to-map rewrite hint).
func excludeShadowedBinds(params *ast.FieldList, body *ast.BlockStmt, binds map[string]*mapDeclInfo) {
	if len(binds) == 0 {
		return
	}
	if params != nil {
		for _, field := range params.List {
			for _, pname := range field.Names {
				delete(binds, pname.Name)
			}
		}
	}
	if body == nil {
		return
	}
	for _, stmt := range body.List {
		assign, ok := stmt.(*ast.AssignStmt)
		if !ok {
			continue
		}
		for i, rhs := range assign.Rhs {
			call, ok := rhs.(*ast.CallExpr)
			if !ok || len(call.Args) != 2 {
				continue
			}
			mt, ok := call.Args[0].(*ast.MapType)
			if !ok || mt == nil {
				continue
			}
			fn, ok := call.Fun.(*ast.Ident)
			if !ok || fn.Name != "make" {
				continue
			}
			if i < len(assign.Lhs) {
				if ident, ok := assign.Lhs[i].(*ast.Ident); ok {
					delete(binds, ident.Name)
				}
			}
		}
	}
}

// analyzeMapInit checks if an initialization expression is make(map[K]V)
// without a size hint. Returns nil if not a hintless map.
func analyzeMapInit(name string, pos token.Pos, expr ast.Expr) *mapDeclInfo {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return nil
	}
	fun, ok := call.Fun.(*ast.Ident)
	if !ok || fun.Name != "make" {
		return nil
	}
	if len(call.Args) == 0 {
		return nil
	}
	// First arg must be a map type.
	mt, ok := call.Args[0].(*ast.MapType)
	if !ok || mt == nil {
		return nil
	}
	// make(map[K]V) with 1 arg = no hint.
	// make(map[K]V, n) with 2 args = has hint.
	if len(call.Args) == 1 {
		return &mapDeclInfo{name: name, pos: pos}
	}
	return nil
}

// getRangeSourceName extracts the variable name from a range expression.
// Returns "" for channels or complex expressions where the source size is unknown.
func getRangeSourceName(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		if x, ok := e.X.(*ast.Ident); ok {
			return x.Name + "." + e.Sel.Name
		}
	}
	return ""
}

// onLoopFn receives each loop discovered in a unit: its body block, position,
// rendered range source (empty when unknown), and whether the range source
// supports a reliable len() hint.
type onLoopFn func(loopBody *ast.BlockStmt, loopPos token.Pos, sourceName string, knownRange bool)

// scanUnitLoops invokes fn for every for/range loop lexically inside body,
// including loops located in nested closures (closure loops still consult
// the enclosing unit's visible declarations conservatively).
func scanUnitLoops(body *ast.BlockStmt, fn onLoopFn) {
	ast.Inspect(body, func(n ast.Node) bool {
		switch loop := n.(type) {
		case *ast.RangeStmt:
			if loop.Body == nil {
				return true
			}
			sourceName := getRangeSourceName(loop.X)
			fn(loop.Body, loop.Pos(), sourceName, true)
		case *ast.ForStmt:
			if loop.Body == nil {
				return true
			}
			// For C-style for loops we cannot know the source size
			// reliably; expectedSize is placeholder guidance.
			fn(loop.Body, loop.Pos(), "", false)
		}
		return true
	})
}

// scanForMapWrite searches a loop body for map write operations
// (map[key] = value) and returns the names of hintless maps that are written.
// A binding qualifies only when it was declared textually before the loop
// (#1103: declarations collected from the same function unit as the loop).
func scanForMapWrite(body *ast.BlockStmt, loopPos token.Pos, decls map[string]*mapDeclInfo) []string {
	var result []string
	found := make(map[string]bool)

	ast.Inspect(body, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for _, lhs := range assign.Lhs {
			idx, ok := lhs.(*ast.IndexExpr)
			if !ok {
				continue
			}
			ident, ok := idx.X.(*ast.Ident)
			if !ok {
				continue
			}
			info, exists := decls[ident.Name]
			if exists && info.pos < loopPos && !found[ident.Name] {
				// Check if the loop body has conditionals that might skip entries.
				// If we find an if statement at the top level of the body,
				// be conservative and skip this warning.
				if hasConditionalSkip(body) {
					continue
				}
				found[ident.Name] = true
				result = append(result, ident.Name)
			}
		}
		return true
	})

	return result
}

// hasConditionalSkip returns true if the loop body contains if/guard
// statements that might conditionally skip map entries. This is a
// conservative check to reduce false positives.
// #1126: recursion now matches scanForMapWrite - conditional writes can sit
// inside nested blocks/if branches, so only a full-depth traversal detects
// every shape that could skip entries. Function literals are skipped:
// their bodies execute in another scope and cannot gate loop iterations.
func hasConditionalSkip(body *ast.BlockStmt) bool {
	if body == nil {
		return false
	}
	skip := false
	ast.Inspect(body, func(n ast.Node) bool {
		if skip || n == nil {
			return false
		}
		switch n.(type) {
		case *ast.IfStmt, *ast.SwitchStmt, *ast.TypeSwitchStmt, *ast.SelectStmt:
			skip = true
			return false
		case *ast.FuncLit:
			return false
		}
		return true
	})
	return skip
}
