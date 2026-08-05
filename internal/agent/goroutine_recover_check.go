package agent

// Unrecovered Goroutine Detection in Go Code
//
// Problem: When a goroutine panics without recover(), the ENTIRE process crashes
// immediately -- there is no caller to catch the panic. This is one of the most
// dangerous and common Go bugs. Unlike errors returned from functions, panics in
// goroutines propagate up only within that goroutine's stack; if unrecovered, the
// runtime terminates the whole program.
//
// Common LLM failure modes this check catches:
//  1. `go func() { result := doWork() }()` -- if doWork panics, process dies
//  2. `go func() { ch <- value }()` -- panics if ch is closed elsewhere
//  3. `go func() { m[key] = value }()` -- panics on concurrent map write
//  4. Inline goroutines with type assertions, nil dereferences, or index operations
//
// The fix is simple: add a deferred recover guard at the top of the goroutine body:
//
//	go func() {
//	    defer func() {
//	        if r := recover(); r != nil {
//	            log.Printf("goroutine panic: %v", r)
//	        }
//	    }()
//	    // ... actual work ...
//	}()
//
// Relationship to panic_safety_check.go:
//   - panic_safety detects bare `panic()` calls in library code
//   - THIS check detects goroutines that LACK recover() protection
//   - Together they provide defense-in-depth: catch both the source (panic)
//     and the missing guard (no recover in goroutine)
//
// Competitor analysis:
//   - Claude Code: no write-time detection
//   - Cursor: no detection (relies on agent judgment)
//   - Cline/OpenHands: no detection
//   - Aider: no detection
//   - Devin: no detection
//   - go vet / staticcheck: do NOT detect missing recover in goroutines
//
// Approach: AST-based analysis. Walk the tree for *ast.GoStmt nodes whose Call.Fun
// is *ast.FuncLit (inline goroutine literal). Check if the body contains any
// recover() call. If not, flag it. Delta-aware: only flags newly introduced
// patterns. Zero LLM cost -- pure AST traversal.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

// goroutineRecoverInstance represents a detected unrecovered goroutine.
type goroutineRecoverInstance struct {
	posStr string // position string for the go statement
}

// checkGoroutineRecover detects inline goroutine literals without recover().
// Returns warning strings. Delta-aware: only flags NEW instances.
func checkGoroutineRecover(filePath, oldContent, newContent string) []string {
	if filepath.Ext(filePath) != ".go" {
		return nil
	}
	if strings.HasSuffix(filePath, "_test.go") {
		return nil
	}
	if strings.TrimSpace(newContent) == "" {
		return nil
	}

	oldCount := len(findUnrecoveredGoroutines(oldContent))
	newInstances := findUnrecoveredGoroutines(newContent)

	if len(newInstances) <= oldCount {
		return nil
	}

	newCount := len(newInstances) - oldCount
	var warnings []string
	for i := 0; i < newCount && i+oldCount < len(newInstances); i++ {
		inst := newInstances[oldCount+i]
		warnings = append(warnings, formatGoroutineRecoverWarning(inst))
	}

	if len(warnings) > 3 {
		warnings = warnings[:3]
	}
	return warnings
}

// findUnrecoveredGoroutines parses Go source and returns all inline goroutine
// literals that lack recover() in their body.
func findUnrecoveredGoroutines(src string) []goroutineRecoverInstance {
	if strings.TrimSpace(src) == "" {
		return nil
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", src, 0)
	if err != nil || file == nil {
		return nil
	}

	var instances []goroutineRecoverInstance
	ast.Inspect(file, func(node ast.Node) bool {
		goStmt, ok := node.(*ast.GoStmt)
		if !ok || goStmt.Call == nil {
			return true
		}
		funcLit, ok := goStmt.Call.Fun.(*ast.FuncLit)
		if !ok || funcLit.Body == nil {
			return true
		}
		if hasRecoverCall(funcLit.Body) {
			return true
		}
		pos := fset.Position(goStmt.Pos())
		instances = append(instances, goroutineRecoverInstance{
			posStr: fmt.Sprintf("line %d", pos.Line),
		})
		return true
	})

	return instances
}

// hasRecoverCall returns true if the block contains a recover() call anywhere
// in its AST subtree. This covers both direct calls and deferred recover wrappers.
func hasRecoverCall(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(node ast.Node) bool {
		if found {
			return false
		}
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == "recover" {
			found = true
			return false
		}
		return true
	})
	return found
}

// formatGoroutineRecoverWarning produces the warning message for an unrecovered
// goroutine instance.
func formatGoroutineRecoverWarning(inst goroutineRecoverInstance) string {
	return fmt.Sprintf(
		"Unrecovered goroutine at %s: inline `go func() { ... }()` without recover(). "+
			"If this goroutine panics, the ENTIRE process crashes -- unlike errors, "+
			"panics in goroutines have no caller to catch them. "+
			"Add a deferred recover guard at the top of the goroutine body:\n"+
			"  defer func() {\n"+
			"      if r := recover(); r != nil {\n"+
			"          log.Printf(\"goroutine panic: %%v\", r)\n"+
			"      }\n"+
			"  }()",
		inst.posStr)
}
