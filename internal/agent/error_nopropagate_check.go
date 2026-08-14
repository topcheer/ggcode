package agent

// Error Not Propagated Detection in Go Code
//
// Problem: AI coding agents frequently write error handling that acknowledges
// the error (logs it, performs side effects, increments metrics) but fails to
// return it to the caller. This is distinct from the two patterns detected by
// error_swallow_check.go:
//   - error-swallow catches: empty body (`if err != nil { }`)
//   - error-swallow catches: bare return (`if err != nil { return }`)
//   - THIS check catches: non-empty body with side effects but NO return
//     statement. The error is logged or handled, but execution continues
//     with potentially invalid state, and the function eventually returns nil.
//
// Example of the anti-pattern:
//
//	func processData() error {
//	    data, err := fetch()
//	    if err != nil {
//	        log.Printf("fetch failed: %v", err) // logs but doesn't return!
//	    }
//	    // continues with potentially nil/invalid data
//	    return nil // returns success despite the error
//	}
//
// This is the most common error-handling bug in AI-generated Go code. In a
// function that returns error, every `if err != nil` block must either:
//   - return the error (or a wrapped version),
//   - exit the loop (continue/break), or
//   - terminate the program (panic, log.Fatal, os.Exit).
// Anything else silently swallows the error.
//
// Competitor analysis:
//   - Claude Code: no write-time detection
//   - Cursor: no detection (relies on external linters)
//   - Cline/OpenHands: no detection
//   - Aider: no detection
//   - GitHub Copilot: no detection
//
// ggcode's approach: AST-based analysis. For each `if err != nil` block in an
// error-returning function, check if the body has side effects but lacks any
// return, loop-exit, or fatal-terminator statement. Delta-aware: only flags
// NEW instances introduced by this edit.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

// errorNoPropagateInstance represents a detected error-not-propagated pattern.
type errorNoPropagateInstance struct {
	errName string
	line    int
}

// terminatorMethods are function/method names that terminate execution,
// making it acceptable to not return after them. Covers:
//   - log.Fatal/Fatalf/Fatalln (program exit)
//   - testing.T.Fatal/Fatalf/Fatalln (test goroutine exit)
//   - testing.T.Skip/Skipf/SkipNow (test skip)
//   - os.Exit (program exit)
//   - runtime.Goexit (goroutine exit)
var terminatorMethods = map[string]bool{
	"Fatal":   true,
	"Fatalf":  true,
	"Fatalln": true,
	"Skip":    true,
	"Skipf":   true,
	"SkipNow": true,
	"Exit":    true,
	"Goexit":  true,
}

// checkErrorNoPropagate detects error handlers with side effects but no
// return statement in error-returning functions. Returns warnings. Delta-aware:
// only flags instances INTRODUCED by this edit.
func checkErrorNoPropagate(filePath, oldContent, newContent string) []string {
	if filepath.Ext(filePath) != ".go" {
		return nil
	}
	if strings.TrimSpace(newContent) == "" {
		return nil
	}

	newInstances := findErrorNoPropagateInstances(newContent)
	if len(newInstances) == 0 {
		return nil
	}

	// Delta check: compare against old content line numbers (fix #142).
	var oldLines map[int]bool
	if strings.TrimSpace(oldContent) != "" {
		for _, iss := range findErrorNoPropagateInstances(oldContent) {
			if oldLines == nil {
				oldLines = make(map[int]bool)
			}
			oldLines[iss.line] = true
		}
	}

	const noPropagateFmt = "Error not propagated: `if %s != nil` at line %d has a non-empty body " +
		"(side effects detected) but no `return` statement in a function that " +
		"returns error. The error is acknowledged but not returned to the caller. " +
		"Add `return %s` (or `return fmt.Errorf(\"...: %%w\", %s)`) to propagate it."

	var warnings []string
	for _, inst := range newInstances {
		if oldLines != nil && oldLines[inst.line] {
			continue
		}
		warnings = append(warnings, fmt.Sprintf(noPropagateFmt,
			inst.errName, inst.line, inst.errName, inst.errName))
	}
	return warnings
}

// countErrorNoPropagate returns the number of error-not-propagated patterns
// in the source. Used for delta-based detection.
func countErrorNoPropagate(src string) int {
	return len(findErrorNoPropagateInstances(src))
}

// findErrorNoPropagateInstances parses Go source and returns all instances
// where an error handler has a non-empty body but no return/exit in an
// error-returning function, ordered by position.
func findErrorNoPropagateInstances(src string) []errorNoPropagateInstance {
	if strings.TrimSpace(src) == "" {
		return nil
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", src, 0)
	if err != nil || file == nil {
		return nil
	}

	var instances []errorNoPropagateInstance

	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		if !funcReturnsError(fn.Type) {
			continue
		}
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			return findNoPropagateInNode(node, fset, &instances)
		})
	}

	return instances
}

// findNoPropagateInNode checks a single AST node for the error-not-propagated
// pattern. Returns false to stop traversal, true to continue.
func findNoPropagateInNode(node ast.Node, fset *token.FileSet, instances *[]errorNoPropagateInstance) bool {
	if node == nil {
		return false
	}
	ifStmt, ok := node.(*ast.IfStmt)
	if !ok {
		return true
	}

	errName := extractErrorCheckName(ifStmt.Cond)
	if errName == "" || isEmptyBody(ifStmt.Body) {
		return true
	}
	if hasAnyReturn(ifStmt.Body) {
		return true
	}
	if hasLoopExit(ifStmt.Body) {
		return true
	}
	if hasFatalTerminator(ifStmt.Body) {
		return true
	}

	pos := fset.Position(ifStmt.Pos())
	*instances = append(*instances, errorNoPropagateInstance{
		errName: errName,
		line:    pos.Line,
	})
	return true
}

// hasAnyReturn returns true if the block contains any return statement
// (bare or with values). Does not recurse into function literals (closures).
func hasAnyReturn(body *ast.BlockStmt) bool {
	if body == nil {
		return false
	}
	found := false
	ast.Inspect(body, func(node ast.Node) bool {
		if found || node == nil {
			return false
		}
		if _, ok := node.(*ast.FuncLit); ok {
			return false // closures have their own return scope
		}
		if _, ok := node.(*ast.ReturnStmt); ok {
			found = true
			return false
		}
		return true
	})
	return found
}

// hasLoopExit returns true if the block contains a `continue` or `break`
// statement. Does not recurse into function literals.
func hasLoopExit(body *ast.BlockStmt) bool {
	if body == nil {
		return false
	}
	found := false
	ast.Inspect(body, func(node ast.Node) bool {
		if found || node == nil {
			return false
		}
		if _, ok := node.(*ast.FuncLit); ok {
			return false
		}
		branch, ok := node.(*ast.BranchStmt)
		if !ok {
			return true
		}
		if branch.Tok == token.CONTINUE || branch.Tok == token.BREAK {
			found = true
			return false
		}
		return true // goto/fallthrough: keep searching
	})
	return found
}

// hasFatalTerminator returns true if the block contains a call that terminates
// execution (panic, log.Fatal, os.Exit, testing.T.Fatal, etc.).
func hasFatalTerminator(body *ast.BlockStmt) bool {
	if body == nil {
		return false
	}
	found := false
	ast.Inspect(body, func(node ast.Node) bool {
		if found || node == nil {
			return false
		}
		if _, ok := node.(*ast.FuncLit); ok {
			return false
		}
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if isFatalOrExitCall(call) {
			found = true
			return false
		}
		return true
	})
	return found
}

// isFatalOrExitCall returns true if the call expression terminates execution.
func isFatalOrExitCall(call *ast.CallExpr) bool {
	if ident, ok := call.Fun.(*ast.Ident); ok {
		return ident.Name == "panic"
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	return terminatorMethods[sel.Sel.Name]
}
