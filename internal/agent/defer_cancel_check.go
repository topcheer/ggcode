package agent

// Lost Cancel Detection for Go files (go vet "lostcancel" equivalent).
//
// Problem: AI coding agents frequently produce Go code that calls
// context.WithCancel(ctx), context.WithTimeout(ctx, d), or
// context.WithDeadline(ctx, t) but forgets to call the returned cancel
// function (or defer it). The cancel function MUST be called to release
// resources associated with the context: timers, goroutines that track
// the deadline, and parent-child context tree links. Omitting the cancel
// call causes:
//   - Timer leaks (WithTimeout/WithDeadline create internal timers)
//   - Goroutine leaks (the context package spawns a goroutine per derived
//     context to propagate cancellation)
//   - Memory growth in long-running services
//
// The Go standard library documentation explicitly states:
//   "Failing to call the Cancel function leaks the child and its subtree
//    until the parent is canceled or the timer fires."
//
// go vet includes a "lostcancel" analyzer for this, but it requires a
// separate vet cycle and is not always run. No AI coding agent (Claude
// Code, Cursor, Cline, Copilot, Aider, Windsurf, Devin) provides inline
// detection at write time.
//
// Approach: AST-based analysis. For each function, find assignments where
// the RHS is context.WithCancel/WithTimeout/WithDeadline. Extract the
// cancel variable name (second return value). Then search the function
// body for a defer or call to that cancel variable. If not found, warn.
//
// Handles these patterns:
//   ctx, cancel := context.WithCancel(parent)   // must defer cancel()
//   ctx, cancel := context.WithTimeout(parent, d)
//   ctx, cancel := context.WithDeadline(parent, t)
//
// Also detects when cancel is assigned to blank identifier (_) — the
// user is intentionally discarding it, which is still a leak for
// WithTimeout/WithDeadline (timer not released until fire).

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

// maxCancelLeakWarnings limits warnings per write.
const maxCancelLeakWarnings = 3

// contextCancelFuncs lists context derivation functions that return a
// cancel function as the second return value.
var contextCancelFuncs = map[string]bool{
	"WithCancel":   true,
	"WithTimeout":  true,
	"WithDeadline": true,
}

// checkLostCancel detects context.WithCancel/WithTimeout/WithDeadline
// calls where the returned cancel function is not called or deferred.
// This is the go vet "lostcancel" pattern, provided as zero-cost inline
// detection at write time.
//
// Only flags NEW occurrences (delta-aware vs old content).
func checkLostCancel(filePath, oldContent, newContent string) []string {
	if filepath.Ext(filePath) != ".go" {
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

	newLeaks := findLostCancels(fset, newAST)
	if len(newLeaks) == 0 {
		return nil
	}

	// Delta: check old content to avoid flagging pre-existing issues.
	if strings.TrimSpace(oldContent) != "" {
		oldFset := token.NewFileSet()
		oldAST, oldErr := parser.ParseFile(oldFset, filePath, oldContent, 0)
		if oldErr == nil {
			oldLeaks := findLostCancels(oldFset, oldAST)
			if len(oldLeaks) > 0 {
				oldLines := make(map[int]bool, len(oldLeaks))
				for _, l := range oldLeaks {
					oldLines[l.line] = true
				}
				var filtered []cancelLeak
				for _, l := range newLeaks {
					if !oldLines[l.line] {
						filtered = append(filtered, l)
					}
				}
				newLeaks = filtered
			}
		}
	}

	if len(newLeaks) == 0 {
		return nil
	}

	warnings := make([]string, 0, len(newLeaks))
	for i, leak := range newLeaks {
		if i >= maxCancelLeakWarnings {
			warnings = append(warnings, fmt.Sprintf(
				"... and %d more lost cancel function(s)", len(newLeaks)-maxCancelLeakWarnings))
			break
		}
		warnings = append(warnings, leak.message)
	}
	return warnings
}

// cancelLeak represents a detected lost-cancel occurrence.
type cancelLeak struct {
	line    int
	message string
}

// findLostCancels walks the AST and finds context.WithCancel/WithTimeout/
// WithDeadline calls where the cancel return value is not subsequently
// called or deferred within the same function.
func findLostCancels(fset *token.FileSet, file *ast.File) []cancelLeak {
	var leaks []cancelLeak

	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}

		// Find all cancel-producing assignments in this function.
		cancels := findCancelAssignments(fn.Body, fset)

		// Find all cancel calls (deferred or direct) in this function.
		calledCancels := findCancelCalls(fn.Body)

		for _, c := range cancels {
			if c.varName == "_" {
				// Blank identifier — cancel intentionally discarded.
				// Only warn for WithTimeout/WithDeadline (timer leak).
				if c.hasTimer {
					leaks = append(leaks, cancelLeak{
						line: c.line,
						message: fmt.Sprintf(
							"L%d: context.%s returns a cancel function assigned to "+
								"blank identifier (_). The internal timer is not released "+
								"until it fires. Assign the cancel and call `defer cancel()`"+
								" to release timer resources immediately.",
							c.line, c.funcName),
					})
				}
				continue
			}
			if calledCancels[c.varName] {
				continue
			}
			leaks = append(leaks, cancelLeak{
				line: c.line,
				message: fmt.Sprintf(
					"L%d: context.%s returns cancel function (%s) that is never "+
						"called. Add `defer %s()` immediately after the assignment "+
						"to prevent timer/goroutine/memory leaks. The cancel function "+
						"MUST be called to release resources associated with the "+
						"derived context.",
					c.line, c.funcName, c.varName, c.varName),
			})
		}
	}

	return leaks
}

// cancelAssignment records a cancel function assignment.
type cancelAssignment struct {
	varName  string // cancel variable name (or "_")
	funcName string // context function name (WithCancel, etc.)
	hasTimer bool   // true for WithTimeout/WithDeadline (timer leak)
	line     int
}

// findCancelAssignments finds assignments from context.WithCancel etc.
func findCancelAssignments(body *ast.BlockStmt, fset *token.FileSet) []cancelAssignment {
	var results []cancelAssignment

	ast.Inspect(body, func(node ast.Node) bool {
		assign, ok := node.(*ast.AssignStmt)
		if !ok {
			return true
		}

		for _, rhs := range assign.Rhs {
			call, ok := rhs.(*ast.CallExpr)
			if !ok {
				continue
			}

			funcName := matchContextCancelCall(call)
			if funcName == "" {
				continue
			}

			// Cancel is the second return value (index 1).
			if len(assign.Lhs) < 2 {
				continue
			}

			cancelVar := extractCancelVarName(assign.Lhs[1])
			if cancelVar == "" {
				continue
			}

			results = append(results, cancelAssignment{
				varName:  cancelVar,
				funcName: funcName,
				hasTimer: funcName == "WithTimeout" || funcName == "WithDeadline",
				line:     fset.Position(call.Pos()).Line,
			})
		}
		return true
	})

	return results
}

// matchContextCancelCall returns the function name if the call is
// context.WithCancel/WithTimeout/WithDeadline, else "".
func matchContextCancelCall(call *ast.CallExpr) string {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	ident, ok := sel.X.(*ast.Ident)
	if !ok || ident.Name != "context" {
		return ""
	}
	if contextCancelFuncs[sel.Sel.Name] {
		return sel.Sel.Name
	}
	return ""
}

// extractCancelVarName gets the variable name from the cancel LHS position.
func extractCancelVarName(expr ast.Expr) string {
	ident, ok := expr.(*ast.Ident)
	if !ok {
		return ""
	}
	return ident.Name
}

// findCancelCalls returns a set of cancel variable names that are called
// (either via defer or direct call) within the function body.
func findCancelCalls(body *ast.BlockStmt) map[string]bool {
	called := make(map[string]bool)

	ast.Inspect(body, func(node ast.Node) bool {
		var call *ast.CallExpr

		if d, ok := node.(*ast.DeferStmt); ok {
			call = d.Call
		} else if c, ok := node.(*ast.CallExpr); ok {
			call = c
		} else {
			return true
		}

		ident, ok := call.Fun.(*ast.Ident)
		if !ok {
			return true
		}

		called[ident.Name] = true
		return true
	})

	return called
}
