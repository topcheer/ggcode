package agent

// Deferred Call Argument Evaluation Detection (Check #77)
//
// Problem: In Go, `defer f(args)` evaluates args IMMEDIATELY at the defer
// statement, but calls f later. This is a common source of subtle bugs:
//
//	defer log.Printf("took %v", time.Since(start))  // BUG: time.Since(start) evaluated NOW, not at defer time
//	defer fmt.Println(getMessage())                  // BUG: getMessage() called immediately
//
// The fix is to wrap in a closure:
//
//	defer func() { log.Printf("took %v", time.Since(start)) }()
//
// AI coding agents frequently write defer with function-call arguments without
// realizing the arguments are evaluated eagerly.
//
// Competitor analysis:
//   - Claude Code: no write-time detection
//   - Cursor: no detection (staticcheck does not flag this)
//   - OpenHands/Cline: no detection
//   - Aider: no detection
//   - golangci-lint: does not flag defer arg evaluation
//
// Approach: AST-based analysis. For each defer statement:
//  1. Check if it's a direct defer call (not a closure: defer func(){}())
//  2. For each argument, check if it contains a function call expression
//  3. Flag it as a potential eager-evaluation bug
//
// Zero LLM cost -- pure AST pattern matching with Go's standard library.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

const maxDeferArgWarnings = 5

// checkDeferArgEval detects defer statements where arguments contain
// function calls that are evaluated immediately rather than at defer time.
func checkDeferArgEval(filePath, _, newContent string) []string {
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
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			ds, ok := n.(*ast.DeferStmt)
			if !ok {
				return true
			}
			if daeShouldWarn(ds, fset, &warnings) {
				return true
			}
			return true
		})
	}

	daeAppendTruncation(warnings, &warnings)
	return warnings
}

// daeShouldWarn checks a defer statement for eager argument evaluation.
func daeShouldWarn(ds *ast.DeferStmt, fset *token.FileSet, warnings *[]string) bool {
	if _, ok := ds.Call.Fun.(*ast.FuncLit); ok {
		return false // defer func(){}() -- closure, safe
	}
	for _, arg := range ds.Call.Args {
		if daeContainsCall(arg) {
			if len(*warnings) < maxDeferArgWarnings {
				pos := fset.Position(ds.Pos())
				*warnings = append(*warnings, fmt.Sprintf(
					"%s:%d: defer argument evaluated immediately -- "+
						"in `defer f(g())`, g() runs NOW, not at defer time; "+
						"wrap in a closure: `defer func() { f(g()) }()`",
					filepath.Base(pos.Filename), pos.Line,
				))
			}
			return true
		}
	}
	return false
}

// daeAppendTruncation adds truncation notice if warnings were capped.
func daeAppendTruncation(_ []string, warnings *[]string) {
	if len(*warnings) >= maxDeferArgWarnings {
		*warnings = append(*warnings, fmt.Sprintf("... (%d defer argument warnings truncated)", maxDeferArgWarnings))
	}
}

// daeContainsCall checks whether an expression contains a function call.
func daeContainsCall(expr ast.Expr) bool {
	found := false
	ast.Inspect(expr, func(n ast.Node) bool {
		if _, ok := n.(*ast.CallExpr); ok {
			found = true
			return false
		}
		return true
	})
	return found
}
