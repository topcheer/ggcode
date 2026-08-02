package agent

// Defer-in-loop detection for Go files.
//
// Problem: AI coding agents frequently produce Go code that acquires resources
// (files, HTTP bodies, mutexes) inside a for/range loop and defers cleanup with
// `defer f.Close()`. While this looks correct, defer statements accumulate and
// only execute when the FUNCTION returns, not when the LOOP ITERATION ends.
// In a loop processing N items, this holds N file descriptors (or N mutex
// locks) simultaneously, causing fd exhaustion, deadlocks, or memory pressure.
//
// Correct pattern: call Close() explicitly at the end of each iteration, or
// extract the loop body into a helper function so defer runs per-iteration.
//
// Competitor analysis:
//   - Claude Code: no inline detection (caught by golangci-lint if installed)
//   - Cursor: lint-on-save may catch via go vet, but not at write time
//   - Cline/OpenHands: reactive only -- caught by tests or production incidents
//   - Aider: no automatic detection
//   - Devin: no inline detection
//
// External linters (staticcheck SA4016, golangci-lint) can catch some cases but
// require a separate lint cycle and are not always installed. This check
// provides immediate, zero-dependency feedback at write time.
//
// Approach: AST-based analysis. Walk every for/range loop body (recursively,
// through nested if/switch/select blocks) looking for DeferStmt. Count defer-
// in-loop occurrences and compare old vs new content for delta-aware detection.
// Only flags NEW defer-in-loop patterns introduced by this edit.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

// deferLoopInfo records a single defer-in-loop occurrence with location info.
type deferLoopInfo struct {
	line int // 1-based line number (best-effort from AST positions)
}

// checkDeferInLoop detects defer statements inside for/range loops in Go code.
// Returns warning strings. Only flags NEW defer-in-loop patterns (delta-aware).
func checkDeferInLoop(filePath, oldContent, newContent string) []string {
	if filepath.Ext(filePath) != ".go" {
		return nil
	}
	if strings.TrimSpace(newContent) == "" {
		return nil
	}
	// Skip test files -- defer-in-loop is common in test cleanup helpers.
	if isTestFile(filePath) {
		return nil
	}

	oldDefers := findDeferInLoops(filePath, oldContent)
	newDefers := findDeferInLoops(filePath, newContent)

	// Delta: only flag if NEW has more defer-in-loop occurrences than OLD.
	if len(newDefers) <= len(oldDefers) {
		return nil
	}

	introduced := len(newDefers) - len(oldDefers)
	noun := "defer statement"
	if introduced > 1 {
		noun = "defer statements"
	}

	return []string{
		fmt.Sprintf(
			"Introduced %d %s inside a for/range loop. Defer runs when the "+
				"FUNCTION returns, not when the loop iteration ends - resources "+
				"accumulate across iterations (file descriptors, mutex locks, memory). "+
				"Extract the loop body into a helper function so defer runs per-"+
				"iteration, or call Close()/Unlock() explicitly at the end of each iteration.",
			introduced, noun),
	}
}

// findDeferInLoops parses Go source and returns all defer-in-loop occurrences.
// A "defer-in-loop" is any DeferStmt found within the body of a ForStmt or
// RangeStmt, at any nesting level (inside if/switch/select within the loop).
func findDeferInLoops(filename, src string) []deferLoopInfo {
	if strings.TrimSpace(src) == "" {
		return nil
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, src, 0)
	if err != nil {
		return nil
	}

	var results []deferLoopInfo

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

		// Walk the loop body looking for defer statements at any depth.
		ast.Inspect(body, func(inner ast.Node) bool {
			if d, ok := inner.(*ast.DeferStmt); ok {
				pos := fset.Position(d.Pos())
				results = append(results, deferLoopInfo{line: pos.Line})
			}
			return true
		})
		return true
	})

	return results
}
