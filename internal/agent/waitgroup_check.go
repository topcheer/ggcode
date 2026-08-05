package agent

// WaitGroup Misuse Detection in Go Code
//
// Problem: AI coding agents frequently produce Go code with sync.WaitGroup
// misuse patterns that cause runtime panics or deadlocks:
//
//  1. wg.Done() without defer: any early return or panic between Add() and
//     Done() skips the decrement, causing wg.Wait() to hang forever.
//
//  2. wg.Done() present but wg.Add() never called: the counter stays at 0,
//     so Wait() returns immediately (race) and Done() panics with
//     "sync: negative WaitGroup counter".
//
//  3. wg.Add(1) inside a goroutine literal: a race condition -- the goroutine
//     may not run before Wait() is called, so Wait() returns prematurely.
//     Add() must be called BEFORE the 'go' statement.
//
// The existing goroutine_leak_check.go detects goroutines WITHOUT any sync
// mechanism. This check detects INCORRECT WaitGroup usage in code that does
// use WaitGroups -- a complementary gap. go vet does NOT detect any of these
// patterns. staticcheck has no rule for WaitGroup misuse either.
//
// Competitor analysis:
//   - Claude Code: no detection (relies on agent judgment)
//   - Cursor: no detection (go vet doesn't catch WaitGroup misuse)
//   - Cline/OpenHands: reactive only -- caught by production incidents
//   - Aider: no detection
//   - Devin: no detection
//   - GitHub Copilot: sometimes suggests correct patterns but doesn't verify
//
// Approach: AST-based analysis. For each function body, collect WaitGroup
// method call statistics (Add/Done/Wait, deferred vs bare, inside goroutine)
// and check for the three misuse patterns. Delta-aware: only flags issues
// newly introduced by this edit. Zero LLM cost.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

// maxWGMisuseWarnings limits the number of warnings per write.
const maxWGMisuseWarnings = 3

// wgMisuseInfo records a single WaitGroup misuse pattern.
type wgMisuseInfo struct {
	pattern string // human-readable description of the misuse
}

// wgStats holds WaitGroup method call statistics for a function body.
type wgStats struct {
	addTotal  int // total Add() calls (including deferred and in-goroutine)
	doneBare  int // Done() as bare statement (no defer)
	doneDefer int // defer Done()
	waitTotal int // Wait() calls
	addInGo   int // Add() calls located inside goroutine literals
}

// checkWaitGroupMisuse detects sync.WaitGroup misuse in Go code.
// Returns warning strings. Only flags NEW issues introduced by this edit
// (delta-aware).
func checkWaitGroupMisuse(filePath, oldContent, newContent string) []string {
	if filepath.Ext(filePath) != ".go" {
		return nil
	}
	if strings.TrimSpace(newContent) == "" {
		return nil
	}
	if isTestFile(filePath) {
		return nil
	}

	oldIssues := findWaitGroupMisuse(oldContent)
	newIssues := findWaitGroupMisuse(newContent)

	if len(newIssues) <= len(oldIssues) {
		return nil
	}

	var warnings []string
	seen := make(map[string]bool)
	for _, issue := range newIssues {
		if seen[issue.pattern] {
			continue
		}
		seen[issue.pattern] = true
		warnings = append(warnings, issue.pattern)
		if len(warnings) >= maxWGMisuseWarnings {
			break
		}
	}
	return warnings
}

// findWaitGroupMisuse parses Go source and returns all WaitGroup misuse
// patterns found across all function declarations.
func findWaitGroupMisuse(src string) []wgMisuseInfo {
	if strings.TrimSpace(src) == "" {
		return nil
	}
	// Fast path: skip analysis if the source has no WaitGroup reference at all.
	if !strings.Contains(src, "WaitGroup") {
		return nil
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", src, 0)
	if err != nil {
		return nil
	}

	var issues []wgMisuseInfo
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		issues = append(issues, analyzeWGFunc(fn.Body)...)
	}
	return issues
}

// analyzeWGFunc checks a single function body for WaitGroup misuse patterns.
func analyzeWGFunc(body *ast.BlockStmt) []wgMisuseInfo {
	stats := collectWGStats(body)
	var issues []wgMisuseInfo

	// Pattern 1: Done() called without defer — early returns/panics skip it.
	if stats.doneBare > 0 && stats.doneDefer == 0 {
		issues = append(issues, wgMisuseInfo{
			pattern: fmt.Sprintf(
				"wg.Done() is called without defer (%d occurrence(s)). "+
					"Any early return or panic between Add() and Done() will skip "+
					"the decrement, causing wg.Wait() to hang forever. "+
					"Use 'defer wg.Done()' immediately after wg.Add(1).",
				stats.doneBare),
		})
	}

	// Pattern 2: Done() present but Add() never called in this function.
	donePresent := stats.doneBare > 0 || stats.doneDefer > 0
	if donePresent && stats.addTotal == 0 {
		issues = append(issues, wgMisuseInfo{
			pattern: "WaitGroup Done() is called but Add() is never called " +
				"in this function. Without Add(1) the counter stays at 0: " +
				"Wait() returns immediately (race) and Done() panics " +
				"('sync: negative WaitGroup counter'). Ensure Add(1) is called " +
				"before spawning each goroutine.",
		})
	}

	// Pattern 3: All Add() calls are inside goroutine literals (race condition).
	wgConfirmed := donePresent || stats.waitTotal > 0
	allAddInGo := stats.addInGo > 0 && stats.addTotal == stats.addInGo
	if wgConfirmed && allAddInGo {
		issues = append(issues, wgMisuseInfo{
			pattern: "wg.Add(1) is called inside a goroutine body (go func). " +
				"This is a race condition: the goroutine may not execute before " +
				"Wait() is called, so Wait() returns prematurely. " +
				"Move wg.Add(1) BEFORE the 'go' statement.",
		})
	}

	return issues
}

// collectWGStats walks a function body and collects WaitGroup method call
// statistics. Identifies bare Done() calls, deferred Done() calls, Add()
// calls (total and inside goroutines), and Wait() calls.
func collectWGStats(body *ast.BlockStmt) wgStats {
	var s wgStats
	var goStmts []*ast.GoStmt

	ast.Inspect(body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.GoStmt:
			goStmts = append(goStmts, node)
		case *ast.DeferStmt:
			switch wgMethodName(node.Call) {
			case "Done":
				s.doneDefer++
			case "Add":
				s.addTotal++
			}
		case *ast.ExprStmt:
			if call, ok := node.X.(*ast.CallExpr); ok {
				switch wgMethodName(call) {
				case "Add":
					s.addTotal++
				case "Done":
					s.doneBare++
				case "Wait":
					s.waitTotal++
				}
			}
		}
		return true
	})

	for _, gs := range goStmts {
		if goroutineHasWGAdd(gs) {
			s.addInGo++
		}
	}

	return s
}

// wgMethodName returns the selector method name of a call expression
// (e.g., "Done" for wg.Done()), or "" if the call is not a method call.
func wgMethodName(call *ast.CallExpr) string {
	if call == nil {
		return ""
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	return sel.Sel.Name
}

// goroutineHasWGAdd checks whether a GoStmt's function literal body contains
// an Add() method call, indicating Add was placed inside the goroutine.
func goroutineHasWGAdd(gs *ast.GoStmt) bool {
	if gs == nil || gs.Call == nil {
		return false
	}
	var found bool
	ast.Inspect(gs.Call, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if wgMethodName(call) == "Add" {
				found = true
				return false
			}
		}
		return true
	})
	return found
}
