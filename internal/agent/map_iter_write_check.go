package agent

// Map Write During Iteration Detection (Check #55)
//
// Problem: AI coding agents frequently generate Go code that writes to (or
// deletes from) a map while iterating over it with `range`. The Go spec
// explicitly states that if a map entry that has not yet been reached is
// removed during iteration, the corresponding iteration value will not be
// produced. If a map entry is created during iteration, it may be produced
// or may be skipped. The choice may vary for the same map across iterations.
//
// This means code like:
//
//	for k, v := range m {
//	    if v.expired {
//	        delete(m, k)        // unsafe: may skip entries
//	    }
//	}
//	m[newKey] = newValue         // unsafe: may or may not be visited
//
// produces non-deterministic, hard-to-debug results.
//
// LLM failure modes this check catches:
//  1. Delete during range: `for k := range m { delete(m, k) }`
//  2. Assignment during range: `for k := range m { m[k] = newVal }`
//  3. Struct field map: `for k := range s.items { s.items[k] = 0 }`
//
// Competitor analysis:
//   - Claude Code: no write-time detection
//   - Cursor: staticcheck does NOT detect this
//   - Cline/OpenHands: no detection
//   - Aider: no detection
//   - go vet: does NOT detect map write during iteration
//
// staticcheck rule SA9001 was proposed but never merged. No standard tool
// catches this at write time.
//
// Approach: AST-based analysis with type resolution. Since Go AST does not
// carry type information, we collect identifiers known to be maps from:
//   - Function parameters with map types
//   - make(map[...]) calls
//   - Composite map literals
// Then for each RangeStmt over a known map, walk the loop body looking for
// delete() calls or index assignments targeting the same map variable.
//
// Delta-aware: only flags patterns newly introduced by this edit.
// Zero LLM cost, no external dependencies.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

// mapIterWriteInfo records a single map-write-during-iteration occurrence.
type mapIterWriteInfo struct {
	line    int    // 1-based line number
	mapName string // the map variable being modified
	op      string // "delete" or "assign"
}

// checkMapIterWrite detects writes/deletes to a map during range iteration.
// Returns warning strings. Only flags NEW occurrences (delta-aware).
func checkMapIterWrite(filePath, oldContent, newContent string) []string {
	if filepath.Ext(filePath) != ".go" {
		return nil
	}
	if strings.TrimSpace(newContent) == "" {
		return nil
	}

	oldWrites := findMapIterWrites(filePath, oldContent)
	newWrites := findMapIterWrites(filePath, newContent)

	if len(newWrites) <= len(oldWrites) {
		return nil
	}

	introduced := len(newWrites) - len(oldWrites)
	return []string{
		fmt.Sprintf(
			"Introduced %d map write/delete operation(s) during range iteration. "+
				"The Go spec states that adding or removing map entries during "+
				"iteration produces non-deterministic results: entries may be "+
				"skipped or visited multiple times. Collect keys to modify in a "+
				"slice during iteration, then apply changes after the loop.",
			introduced),
	}
}

// findMapIterWrites parses Go source and returns all map-write-during-
// iteration occurrences.
func findMapIterWrites(filename, src string) []mapIterWriteInfo {
	if strings.TrimSpace(src) == "" {
		return nil
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, src, 0)
	if err != nil {
		return nil
	}

	mapNames := collectMapNames(file)

	var results []mapIterWriteInfo

	ast.Inspect(file, func(n ast.Node) bool {
		rng, ok := n.(*ast.RangeStmt)
		if !ok {
			return true
		}

		mapName := rangeExprName(rng.X)
		if mapName == "" || !isKnownMap(mapName, mapNames) {
			return true
		}
		if rng.Body == nil {
			return true
		}

		ast.Inspect(rng.Body, func(inner ast.Node) bool {
			if inner == nil {
				return true
			}
			if call, ok := inner.(*ast.CallExpr); ok {
				if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "delete" {
					if len(call.Args) > 0 {
						if argName := exprName(call.Args[0]); argName == mapName {
							pos := fset.Position(call.Pos())
							results = append(results, mapIterWriteInfo{
								line: pos.Line, mapName: mapName, op: "delete",
							})
						}
					}
				}
			}
			if assign, ok := inner.(*ast.AssignStmt); ok {
				for _, lhs := range assign.Lhs {
					if idx, ok := lhs.(*ast.IndexExpr); ok {
						if targetName := exprName(idx.X); targetName == mapName {
							pos := fset.Position(assign.Pos())
							results = append(results, mapIterWriteInfo{
								line: pos.Line, mapName: mapName, op: "assign",
							})
						}
					}
				}
			}
			return true
		})
		return true
	})

	return results
}

// collectMapNames scans the file and returns a set of identifiers that are
// known to be map-typed. Sources: function parameters, make(map[...]) calls,
// composite map literals, and struct fields with map types.
func collectMapNames(file *ast.File) map[string]bool {
	names := make(map[string]bool)

	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.FuncDecl:
			if node.Type != nil && node.Type.Params != nil {
				for _, field := range node.Type.Params.List {
					if _, ok := field.Type.(*ast.MapType); ok {
						for _, name := range field.Names {
							names[name.Name] = true
						}
					}
				}
			}
		case *ast.StructType:
			if node.Fields != nil {
				for _, field := range node.Fields.List {
					if _, ok := field.Type.(*ast.MapType); ok {
						for _, name := range field.Names {
							names[name.Name] = true
						}
					}
				}
			}
		case *ast.AssignStmt:
			// m := make(map[K]V, ...) or m := map[K]V{...}
			for i, lhs := range node.Lhs {
				if id, ok := lhs.(*ast.Ident); ok && i < len(node.Rhs) {
					rhs := node.Rhs[i]
					// make(map[...]) call
					if call, ok := rhs.(*ast.CallExpr); ok {
						if fnId, ok := call.Fun.(*ast.Ident); ok && fnId.Name == "make" {
							if len(call.Args) > 0 {
								if _, ok := call.Args[0].(*ast.MapType); ok {
									names[id.Name] = true
								}
							}
						}
					}
					// m := map[K]V{...} composite literal
					if cl, ok := rhs.(*ast.CompositeLit); ok {
						if _, isMap := cl.Type.(*ast.MapType); isMap {
							names[id.Name] = true
						}
					}
				}
			}
		}
		return true
	})

	return names
}

// isKnownMap checks if a name refers to a known map-typed variable. Handles
// both simple identifiers ("m") and selector expressions ("s.items") by
// matching against the field name portion.
func isKnownMap(name string, known map[string]bool) bool {
	if known[name] {
		return true
	}
	// For selector "s.items", check "items" against struct field names.
	if idx := strings.LastIndex(name, "."); idx >= 0 {
		field := name[idx+1:]
		return known[field]
	}
	return false
}

// rangeExprName extracts the variable name from a range expression.
func rangeExprName(expr ast.Expr) string {
	if expr == nil {
		return ""
	}
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		base := exprName(e.X)
		if base != "" {
			return base + "." + e.Sel.Name
		}
	}
	return ""
}

// exprName extracts a simple name from common AST expressions.
func exprName(expr ast.Expr) string {
	if expr == nil {
		return ""
	}
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		base := exprName(e.X)
		if base != "" {
			return base + "." + e.Sel.Name
		}
	}
	return ""
}
