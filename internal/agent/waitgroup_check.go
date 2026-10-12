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
	"path/filepath"
	"strings"
)

// maxWGMisuseWarnings limits the number of warnings per write.
const maxWGMisuseWarnings = 3

// wgMisuseInfo records a single WaitGroup misuse pattern.
type wgMisuseInfo struct {
	pattern string // human-readable description of the misuse
	// #3777 A: occurrence count for display only. It must NEVER be part of
	// the delta fingerprint: Pattern 1 used to embed "(%d occurrence(s))"
	// into pattern, so fixing 1 of 2 occurrences changed the fingerprint and
	// the REMAINING pre-existing issue was re-reported as newOnly - punishing
	// partial fixes with noise that invites over-rewriting correct code.
	count int
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

	// Delta: use content fingerprints, not count comparison.
	// An agent can fix one WG misuse and introduce another — count stays the same
	// but the new misuse is still a problem.
	oldSet := make(map[string]bool)
	for _, iss := range oldIssues {
		oldSet[iss.pattern] = true
	}
	var newOnly []wgMisuseInfo
	for _, iss := range newIssues {
		if !oldSet[iss.pattern] {
			newOnly = append(newOnly, iss)
		}
	}
	if len(newOnly) == 0 {
		return nil
	}

	var warnings []string
	seen := make(map[string]bool)
	for _, issue := range newOnly {
		if seen[issue.pattern] {
			continue
		}
		seen[issue.pattern] = true
		if issue.count > 1 {
			// #3777 A: count lives in the RENDERED warning only, never in the
			// fingerprint compared above.
			warnings = append(warnings, fmt.Sprintf("%s (%d occurrences)", issue.pattern, issue.count))
		} else {
			warnings = append(warnings, issue.pattern)
		}
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

	file, _, err := parseGoSource("", src, 0)
	if err != nil {
		return nil
	}

	var issues []wgMisuseInfo
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		issues = append(issues, analyzeWGFunc(fn)...)
	}
	return issues
}

// wgParamType reports whether the function receives a *sync.WaitGroup
// (or a type whose name ends in WaitGroup) as a parameter. #938: the
// canonical worker pattern (pkg.go.dev/sync#WaitGroup) takes wg as a
// parameter and calls only Done() in the worker - Add() lives in the
// spawner. Function-granularity analysis cannot see the caller, so this
// shape must not be flagged.
func wgParamType(fn *ast.FuncDecl) bool {
	// #1527 case A: the receiver shape (`func (s *Server) worker()` with
	// `s.wg.Done()`) is the same canonical spawner-splits-Add pattern -
	// wgParamType only looked at fn.Type.Params, so the idiomatic
	// struct-field form was flagged "Done() panics", and an agent following
	// the advice added a redundant Add (counter 2, one Done) leaving Wait()
	// deadlocked. Function-granularity cannot see the spawner here either.
	if fn.Recv != nil {
		// #3777 B: the *...Server suffix gate that used to sit here was
		// removed - it returned early and exempted the ENTIRE class,
		// including *Server methods using a LOCAL wg variable (a genuine
		// bare-Done misuse), contradicting the #2987 contract that
		// local-wg methods stay checked. The shape check below is the
		// correct FP-free gate: it exempts exactly the methods whose wg
		// calls go through the receiver's own struct field (`s.wg.Done()`),
		// where Add() lives in the spawner and function-granularity
		// analysis cannot see it - whatever the receiver type is named.
		if recvFieldWGDone(fn) {
			return true
		}
	}
	if fn.Type == nil || fn.Type.Params == nil {
		return false
	}
	for _, p := range fn.Type.Params.List {
		// *sync.WaitGroup / sync.WaitGroup / *MyWaitGroup
		if star, ok := p.Type.(*ast.StarExpr); ok {
			if sel, ok := star.X.(*ast.SelectorExpr); ok && strings.HasSuffix(sel.Sel.Name, "WaitGroup") {
				return true
			}
			if id, ok := star.X.(*ast.Ident); ok && strings.HasSuffix(id.Name, "WaitGroup") {
				return true
			}
		}
		if sel, ok := p.Type.(*ast.SelectorExpr); ok && strings.HasSuffix(sel.Sel.Name, "WaitGroup") {
			return true
		}
	}
	return false
}

// recvFieldWGDone reports whether fn is a method whose WaitGroup calls go
// through the RECEIVER'S OWN struct field (`p.wg.Done()` / `defer r.wg.Done()`)
// (#2987). That shape pins the wg to the enclosing struct: Add() necessarily
// lives in the spawner method of the same type, invisible to function-
// granularity analysis - flagging it yields the misleading "call Add first"
// advice that #1527 case A proved agents follow into a deadlock.
func recvFieldWGDone(fn *ast.FuncDecl) bool {
	if fn.Recv == nil || fn.Body == nil {
		return false
	}
	recvNames := make(map[string]bool)
	for _, p := range fn.Recv.List {
		for _, n := range p.Names {
			recvNames[n.Name] = true
		}
	}
	if len(recvNames) == 0 {
		return false
	}
	found := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Done" {
			return true
		}
		field, ok := sel.X.(*ast.SelectorExpr) // recv.field
		if !ok {
			return true
		}
		if id, ok := field.X.(*ast.Ident); ok && recvNames[id.Name] {
			found = true
			return false
		}
		return true
	})
	return found
}

// analyzeWGFunc checks a single function for WaitGroup misuse patterns.
func analyzeWGFunc(fn *ast.FuncDecl) []wgMisuseInfo {
	body := fn.Body
	stats := collectWGStats(body)
	var issues []wgMisuseInfo

	// Pattern 1: Done() called without defer — early returns/panics skip it.
	// #3777 A: pattern is a CONSTANT fingerprint (count is carried in the
	// count field and rendered separately) so a partial fix (2 bare Done → 1)
	// does not change the fingerprint and re-report the remaining issue.
	if stats.doneBare > 0 && stats.doneDefer == 0 {
		issues = append(issues, wgMisuseInfo{
			pattern: "wg.Done() is called without defer. " +
				"Any early return or panic between Add() and Done() will skip " +
				"the decrement, causing wg.Wait() to hang forever. " +
				"Use 'defer wg.Done()' immediately after wg.Add(1).",
			count: stats.doneBare,
		})
	}

	// Pattern 2: Done() present but Add() never called in this function.
	// #938: exempt the canonical worker shape - a function RECEIVING a
	// *sync.WaitGroup parameter only calls Done(); Add() lives in the
	// spawner, which function-granularity analysis cannot see. This exact
	// shape is the pkg.go.dev/sync#WaitGroup documented example.
	donePresent := stats.doneBare > 0 || stats.doneDefer > 0
	if donePresent && stats.addTotal == 0 && !wgParamType(fn) {
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
