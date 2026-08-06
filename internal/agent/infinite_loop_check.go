package agent

// Infinite Loop Without Break Detection
//
// Problem: AI coding agents sometimes emit `for {}` loops with no exit path
// (no break, return, panic, or os.Exit inside the body). These compile cleanly
// but hang forever at runtime, causing deadlocks, goroutine leaks, or process
// hangs that are extremely hard to debug.
//
// Common variants:
//   - `for { ... }` with only logging or processing but no termination
//   - `for { select { ... } }` where none of the cases breaks out
//   - `for cond { ... }` where cond is never mutated inside the body
//
// This check focuses on the clearest case: `for {}` (no condition, no range,
// no ForClause) whose body contains no break, return, panic, os.Exit, or
// runtime.Goexit statement.
//
// Competitor analysis:
//   - Claude Code / Cursor / OpenHands / Aider: no write-time detection
//   - staticcheck: no infinite-loop check
//   - golangci-lint: no dedicated check for unbounded for {}
//   - go vet: does not detect this pattern
//
// Approach: AST-based. Parse the file, walk all ForStmt nodes that have no
// condition (ForStmt.Cond == nil and no range), inspect the body for exit
// statements. If none found, emit a warning.
//
// Zero LLM cost. No new external dependencies.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

const maxInfiniteLoopWarnings = 4

// checkInfiniteLoop detects `for {}` loops with no exit path in the body.
func checkInfiniteLoop(filePath, _, newContent string) []string {
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

	var warnings []string
	ast.Inspect(file, func(n ast.Node) bool {
		stmt, ok := n.(*ast.ForStmt)
		if !ok {
			return true
		}
		// Only flag loops with no condition (truly unconditional).
		// ForStmt with Cond != nil could still terminate when cond becomes false.
		if stmt.Cond != nil {
			return true
		}
		// Empty body `for {}` - definitely infinite and pointless.
		if stmt.Body == nil || len(stmt.Body.List) == 0 {
			ilEmitWarning(stmt, fset, &warnings, "empty body")
			return false
		}
		// Check if the body contains any exit statement.
		if ilHasExit(stmt.Body) {
			return false
		}
		ilEmitWarning(stmt, fset, &warnings, "no break/return/panic")
		return false
	})

	if len(warnings) > maxInfiniteLoopWarnings {
		trunc := fmt.Sprintf("... and %d more infinite-loop warning(s)",
			len(warnings)-maxInfiniteLoopWarnings)
		warnings = warnings[:maxInfiniteLoopWarnings]
		warnings = append(warnings, trunc)
	}
	return warnings
}

// ilEmitWarning appends an infinite-loop warning.
func ilEmitWarning(stmt *ast.ForStmt, fset *token.FileSet, warnings *[]string, detail string) {
	pos := fset.Position(stmt.Pos())
	*warnings = append(*warnings,
		fmt.Sprintf("%s:%d: for-loop with no condition has %s - "+
			"this loop will run forever. Add a break, return, or context-based exit condition.",
			pos.Filename, pos.Line, detail))
}

// ilHasExit returns true if the block contains ANY statement that can exit
// the loop or function at any nesting level: break, goto, return, panic,
// os.Exit, runtime.Goexit, log.Fatal, etc.
//
// Uses ast.Inspect for recursive search. A break inside a nested switch or
// inner for-loop would be a false positive (breaks the inner construct, not
// this loop), but we accept that tradeoff for simplicity and fewer missed
// detections.
func ilHasExit(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		if found {
			return false
		}
		switch s := n.(type) {
		case *ast.BranchStmt:
			if s.Tok == token.BREAK || s.Tok == token.GOTO {
				found = true
			}
		case *ast.ReturnStmt:
			found = true
		case *ast.CallExpr:
			if ilCallExits(s) {
				found = true
			}
		}
		return true
	})
	return found
}

// ilCallExits checks if a function call is a known exit function:
// panic, os.Exit, os.Exit, runtime.Goexit, log.Fatal.
func ilCallExits(call *ast.CallExpr) bool {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		return fn.Name == "panic"
	case *ast.SelectorExpr:
		// Check for os.Exit, runtime.Goexit, log.Fatal, log.Panic, etc.
		if pkg, ok := fn.X.(*ast.Ident); ok {
			return ilPkgFuncExits(pkg.Name, fn.Sel.Name)
		}
	}
	return false
}

// ilPkgFuncExits returns true for known package/function pairs that exit.
func ilPkgFuncExits(pkg, name string) bool {
	switch pkg {
	case "os":
		return name == "Exit"
	case "runtime":
		return name == "Goexit"
	case "log":
		switch name {
		case "Fatal", "Fatalf", "Fatalln", "Panic", "Panicf", "Panicln":
			return true
		}
	}
	return false
}
