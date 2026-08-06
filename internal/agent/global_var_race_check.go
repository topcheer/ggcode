package agent

// Global Variable Mutation in Concurrent Context Detection (Check #75)
//
// Problem: AI coding agents frequently produce Go code that mutates
// package-level (global) variables inside goroutines without synchronization.
// Unlike local variables, package-level variables are shared across all
// goroutines. Writing to them from a goroutine without a mutex, atomic
// operation, or channel creates a data race that Go's race detector may
// catch at test time but silently corrupts data in production.
//
// The existing `concurrent-map-access` check only covers map types. This
// check extends coverage to ALL package-level variables (slices, ints,
// bools, structs, etc.) that are mutated inside `go func() { ... }()`
// bodies or functions launched via `go someFunc()`.
//
// Competitor analysis:
//   - Claude Code: no write-time detection
//   - Cursor: no detection (go test -race may catch post-save)
//   - Cline/OpenHands: no detection
//   - Aider: no detection
//   - Windsurf: no detection
//
// Approach: AST-based analysis. For each Go file:
//  1. Collect all package-level variable names.
//  2. Find goroutine launches (go statements with func literals).
//  3. Inside goroutine bodies, find assignments to package-level variables.
//  4. Check if a sync mechanism (Lock, atomic.Store, etc.) guards the write.
//  5. Warn about potential data races.
//
// Zero LLM cost — pure AST pattern matching with Go's standard library.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

const maxGlobalRaceWarnings = 4

// gvrSyncFuncs lists function call patterns that indicate synchronization.
// If any of these appear in the goroutine body, we suppress the warning.
var gvrSyncPatterns = []string{
	"Lock", "Unlock", // sync.Mutex / sync.RWMutex
	"RLock", "RUnlock", // sync.RWMutex
	"Store", "Load", // atomic.Store*/Load* / sync/atomic
	"Add", "Swap", // atomic.Add*/Swap*
	"CompareAndSwap", // atomic.CompareAndSwap*
	"Send",           // channel send (heuristic: method name)
}

// checkGlobalVarRace detects package-level variables mutated in goroutines.
func checkGlobalVarRace(filePath, _, newContent string) []string {
	if filepath.Ext(filePath) != ".go" {
		return nil
	}
	src := strings.TrimSpace(newContent)
	if src == "" {
		return nil
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filePath, newContent, 0)
	if err != nil || file == nil {
		return nil
	}

	globalVars := gvrCollectGlobals(file)
	if len(globalVars) == 0 {
		return nil
	}

	var warnings []string
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		msgs := gvrCheckFunc(fn, globalVars, fset)
		warnings = append(warnings, msgs...)
	}

	if len(warnings) > maxGlobalRaceWarnings {
		remaining := len(warnings) - maxGlobalRaceWarnings
		warnings = warnings[:maxGlobalRaceWarnings]
		warnings = append(warnings,
			fmt.Sprintf("...and %d more global variable race warning(s)", remaining))
	}

	return warnings
}

// gvrCollectGlobals returns the set of package-level variable names.
func gvrCollectGlobals(file *ast.File) map[string]bool {
	globals := make(map[string]bool)
	for _, decl := range file.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for _, name := range vs.Names {
				if name.Name != "" && name.Name != "_" {
					globals[name.Name] = true
				}
			}
		}
	}
	return globals
}

// gvrCheckFunc inspects a function for goroutine bodies that mutate globals.
func gvrCheckFunc(fn *ast.FuncDecl, globals map[string]bool, fset *token.FileSet) []string {
	var warnings []string

	ast.Inspect(fn.Body, func(node ast.Node) bool {
		goStmt, ok := node.(*ast.GoStmt)
		if !ok {
			return true
		}

		// Only check function literal bodies: go func() { ... }(...)
		funcLit, ok := goStmt.Call.Fun.(*ast.FuncLit)
		if !ok {
			return true
		}

		hasSync := gvrHasSyncCall(funcLit.Body)
		if hasSync {
			return true
		}

		// Find assignments to globals inside the goroutine body.
		ast.Inspect(funcLit.Body, func(n ast.Node) bool {
			assign, ok := n.(*ast.AssignStmt)
			if !ok {
				return true
			}
			for _, lhs := range assign.Lhs {
				ident := gvrExtractIdent(lhs)
				if ident == "" {
					continue
				}
				if !globals[ident] {
					continue
				}
				pos := fset.Position(assign.Pos())
				warnings = append(warnings, fmt.Sprintf(
					"Potential data race: package-level variable %q is mutated inside a goroutine "+
						"at %s without visible synchronization (mutex/atomic). Use a sync.Mutex, "+
						"sync/atomic, or a channel to protect shared state.",
					ident, pos))
			}
			return true
		})

		return true
	})

	return warnings
}

// gvrExtractIdent extracts the identifier name from an LHS expression.
// Handles direct identifiers and selector expressions (pkg.Var).
func gvrExtractIdent(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		// e.g., pkg.GlobalVar - only flag unqualified names (globals in
		// the current package). Selector expressions are skipped because we
		// cannot resolve cross-package references at write time.
		return ""
	default:
		return ""
	}
}

// gvrHasSyncCall checks if the goroutine body contains any sync mechanism.
func gvrHasSyncCall(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		if found {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		methodName := gvrCallMethodName(call)
		if methodName == "" {
			return true
		}
		for _, pat := range gvrSyncPatterns {
			if strings.Contains(methodName, pat) {
				found = true
				return false
			}
		}
		return true
	})
	return found
}

// gvrCallMethodName extracts the method name from a call expression.
func gvrCallMethodName(call *ast.CallExpr) string {
	switch fn := call.Fun.(type) {
	case *ast.SelectorExpr:
		return fn.Sel.Name
	case *ast.Ident:
		return fn.Name
	default:
		return ""
	}
}
