package agent

// Select-Loop time.After Timer Leak Detection in Go Code
//
// Problem: AI coding agents (and human developers) frequently produce Go code
// that uses time.After inside a select statement within a for/range loop for
// periodic operations or timeout handling. While time.After is convenient, it
// creates a new timer each call that is not garbage collected until it fires.
// In a loop iterating at high frequency (e.g., every 100ms), this causes
// timers to accumulate on the heap faster than they expire, leading to memory
// growth and eventual OOM in long-running services.
//
// The fix is to use time.NewTimer (or time.Tick/time.Ticker) instead, which
// allows explicit Stop() to cancel the timer immediately and release resources.
//
// Example of the leaking pattern:
//
//	for {
//	    select {
//	    case <-ch:
//	        // handle
//	    case <-time.After(100 * time.Millisecond):
//	        // timeout
//	    }
//	}
//
// Correct pattern using time.NewTimer:
//
//	timer := time.NewTimer(100 * time.Millisecond)
//	defer timer.Stop()
//	for {
//	    timer.Reset(100 * time.Millisecond)
//	    select {
//	    case <-ch:
//	        // handle
//	    case <-timer.C:
//	        // timeout
//	    }
//	}
//
// Competitor analysis:
//   - Claude Code: no automatic detection (relies on external linters)
//   - Cursor: no automatic detection (lint-on-save may catch via go vet)
//   - Cline/OpenHands: reactive only -- caught by tests or production incidents
//   - Aider: no automatic detection
//   - Windsurf: no automatic detection
//
// External linters (staticcheck SA1015, golangci-lint govet) can catch some
// cases but require a separate lint cycle and are not always installed. go vet
// itself does not flag this pattern. This check provides immediate, zero-
// dependency feedback at write time using Go's standard library AST parser.
//
// Approach: AST-based analysis. Walk every ForStmt or RangeStmt body looking
// for CommClause (select case) statements whose communication expression
// contains a time.After call. Only flags NEW occurrences introduced by this
// edit (delta-aware). Test files are skipped -- this pattern is common in
// test helpers and mock servers.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

// timerLeakInfo records a single time.After-in-select-in-loop occurrence.
type timerLeakInfo struct {
	line int // 1-based line number (best-effort from AST positions)
}

// checkSelectTimerLeak detects time.After inside select statements within
// for/range loops in Go code. Returns warning strings. Only flags NEW
// occurrences introduced by this edit (delta-aware).
func checkSelectTimerLeak(filePath, oldContent, newContent string) []string {
	if filepath.Ext(filePath) != ".go" {
		return nil
	}
	if strings.TrimSpace(newContent) == "" {
		return nil
	}
	// Skip test files -- time.After in select-in-loop is common in test helpers.
	if isTestFile(filePath) {
		return nil
	}

	oldLeaks := findTimerLeaks(filePath, oldContent)
	newLeaks := findTimerLeaks(filePath, newContent)

	// Delta: only flag if NEW has more timer leak occurrences than OLD.
	if len(newLeaks) <= len(oldLeaks) {
		return nil
	}

	introduced := len(newLeaks) - len(oldLeaks)
	noun := "time.After timer leak"
	if introduced > 1 {
		noun = "time.After timer leaks"
	}

	return []string{
		fmt.Sprintf(
			"Introduced %d %s: time.After() inside a select within a for/range "+
				"loop. Each call creates a timer that is not garbage collected until "+
				"it fires, causing memory growth in long-running services. "+
				"Use time.NewTimer (or time.Ticker for periodic events) instead "+
				"and call Stop()/Reset() to release resources each iteration.",
			introduced, noun),
	}
}

// findTimerLeaks parses Go source and returns all time.After-in-select-in-loop
// occurrences. A "timer leak" is a CallExpr to time.After found within a
// CommClause (select case) that is inside a ForStmt or RangeStmt body, at any
// nesting level.
func findTimerLeaks(filename, src string) []timerLeakInfo {
	if strings.TrimSpace(src) == "" {
		return nil
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, src, 0)
	if err != nil || file == nil {
		return nil
	}

	var results []timerLeakInfo

	// Walk all for/range loops.
	ast.Inspect(file, func(n ast.Node) bool {
		var body *ast.BlockStmt
		switch node := n.(type) {
		case *ast.ForStmt:
			body = node.Body
		case *ast.RangeStmt:
			body = node.Body
		default:
			return true
		}
		if body == nil {
			return true
		}

		// Walk the loop body looking for CommClause (select case) containing
		// time.After calls.
		ast.Inspect(body, func(inner ast.Node) bool {
			comm, ok := inner.(*ast.CommClause)
			if !ok {
				return true
			}
			// The Comm field is the communication statement (SendStmt,
			// AssignStmt with unary <-, or ExprStmt with unary <-).
			// We need to check the Comm expression AND the case body for
			// time.After calls in the channel position.
			if comm.Comm != nil {
				if call := extractTimeAfterCall(comm.Comm); call != nil {
					pos := fset.Position(call.Pos())
					results = append(results, timerLeakInfo{line: pos.Line})
					return true
				}
			}
			return true
		})
		return true
	})

	return results
}

// extractTimeAfterCall looks for a time.After() call expression within the
// communication statement of a select case. The Comm field can be:
//   - *ast.AssignStmt: x := <-time.After(d) or x = <-time.After(d)
//   - *ast.ExprStmt: <-time.After(d)
//   - *ast.SendStmt: ch <- v (no time.After here, but check anyway)
//
// Returns the time.After CallExpr if found, nil otherwise.
func extractTimeAfterCall(comm ast.Stmt) *ast.CallExpr {
	switch s := comm.(type) {
	case *ast.AssignStmt:
		// x := <-time.After(d) -- RHS[0] is UnaryExpr(<-, CallExpr)
		for _, rhs := range s.Rhs {
			if call := findTimeAfterInExpr(rhs); call != nil {
				return call
			}
		}
	case *ast.ExprStmt:
		// <-time.After(d)
		if call := findTimeAfterInExpr(s.X); call != nil {
			return call
		}
	}
	return nil
}

// findTimeAfterInExpr recursively searches an expression tree for a
// time.After call. Handles UnaryExpr (<-operand), where the operand may be
// the time.After call.
func findTimeAfterInExpr(expr ast.Expr) *ast.CallExpr {
	switch e := expr.(type) {
	case *ast.UnaryExpr:
		// <-operand where operand is time.After(d)
		if call, ok := e.X.(*ast.CallExpr); ok {
			if isTimeAfterCall(call) {
				return call
			}
		}
		// Recurse into the operand in case of nested expressions.
		return findTimeAfterInExpr(e.X)
	case *ast.CallExpr:
		if isTimeAfterCall(e) {
			return e
		}
	}
	return nil
}

// isTimeAfterCall returns true if the CallExpr is a call to time.After.
func isTimeAfterCall(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	ident, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	return ident.Name == "time" && sel.Sel.Name == "After"
}
