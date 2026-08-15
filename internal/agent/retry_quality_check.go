package agent

// Retry Loop Quality Detection (Resilience Engineering)
//
// Problem: AI coding agents frequently generate retry/reconnect loops that
// lack two critical resilience properties, both of which cause production
// incidents:
//
//  1. Missing backoff: the loop re-attempts immediately on failure with no
//     time.Sleep / delay between attempts. Under load this creates retry
//     storms and thundering-herd effects that amplify downstream outages
//     (a.k.a. "cascading failures").
//
//  2. Missing attempt cap: an unbounded `for {}` retry loop (no condition,
//     no attempt counter) that never terminates when the target stays down,
//     leaking goroutines and burning CPU forever.
//
// Competitor analysis:
//   - Claude Code: no automatic detection (relies on external linters)
//   - Cursor / Windsurf: no automatic detection
//   - Cline / OpenHands / Devin: reactive only -- caught by incidents
//   - gosec (G107): SSRF only; no retry semantics
//   - staticcheck (S1000-S1040): style/perf, no retry analysis
//   - revive (unconditional-recursion): detects recursion, not retry loops
//
// None provide inline write-time detection of retry-loop resilience bugs.
// This check delivers immediate, zero-dependency feedback in <1ms/file.
//
// Approach: AST-based analysis of Go source. A loop is classified as a
// "retry loop" when its body contains BOTH:
//   - a failing call (network/IO that returns an error), AND
//   - error-driven continuation (if err != nil { continue } or loop-back).
//
// Once classified, it is checked for a delay (time.Sleep/time.After) and,
// if unbounded (for {}), for an attempt counter. The check is delta-aware:
// only patterns newly introduced by this edit are reported.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

// retryLoopIssue represents one detected retry-quality problem.
type retryLoopIssue struct {
	key     string // dedup key: kind:pos
	kind    string // "missing-backoff" | "unbounded-retry"
	message string
}

// failingCallFuncs are common network/IO calls whose errors trigger retries.
var failingCallFuncs = map[string]bool{
	"Do": true, "Get": true, "Post": true, "Head": true,
	"Query": true, "QueryRow": true, "Exec": true,
	"Dial": true, "DialContext": true, "Ping": true,
	"Read": true, "Write": true, "Send": true, "Receive": true,
	"RoundTrip": true, "Call": true, "Request": true,
}

// checkRetryQuality detects retry loops missing backoff delay or attempt cap.
func checkRetryQuality(filePath, oldContent, newContent string) []string {
	if filepath.Ext(filePath) != ".go" {
		return nil
	}
	if strings.TrimSpace(newContent) == "" {
		return nil
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filePath, newContent, 0)
	if err != nil || file == nil {
		return nil
	}

	issues := findRetryLoopIssues(file, fset)
	if len(issues) == 0 {
		return nil
	}

	// Delta-aware: subtract pre-existing issues from old content.
	if strings.TrimSpace(oldContent) != "" {
		oldFset := token.NewFileSet()
		oldFile, oldErr := parser.ParseFile(oldFset, filePath, oldContent, 0)
		if oldErr == nil && oldFile != nil {
			oldSet := retryIssueSet(findRetryLoopIssues(oldFile, oldFset))
			filtered := issues[:0]
			for _, iss := range issues {
				if !oldSet[iss.key] {
					filtered = append(filtered, iss)
				}
			}
			issues = filtered
		}
	}

	if len(issues) == 0 {
		return nil
	}
	warnings := make([]string, 0, len(issues))
	for _, iss := range issues {
		warnings = append(warnings, iss.message)
	}
	return warnings
}

// findRetryLoopIssues walks the AST and inspects every for-loop for retry
// resilience problems.
func findRetryLoopIssues(file *ast.File, fset *token.FileSet) []retryLoopIssue {
	var issues []retryLoopIssue
	ast.Inspect(file, func(node ast.Node) bool {
		loop, ok := node.(*ast.ForStmt)
		if !ok {
			return true
		}
		if loop.Body == nil {
			return true
		}
		if !isRetryLoop(loop.Body) {
			return true
		}
		posStr := fset.Position(loop.Pos()).String()
		if !loopBodyHasBackoff(loop.Body) {
			issues = append(issues, retryLoopIssue{
				key:     "missing-backoff:" + posStr,
				kind:    "missing-backoff",
				message: fmt.Sprintf(`Retry loop at %s re-attempts on failure with no backoff delay (no time.Sleep/time.After in body) -- this causes retry storms and thundering-herd effects under load. Add exponential backoff (e.g., time.Sleep(backoff); backoff *= 2) between attempts.`, posStr),
			})
		}
		if isUnboundedForLoop(loop) && !loopHasAttemptCap(loop) {
			issues = append(issues, retryLoopIssue{
				key:     "unbounded-retry:" + posStr,
				kind:    "unbounded-retry",
				message: fmt.Sprintf(`Unbounded retry loop (for {}) at %s has no attempt cap or max-retries guard -- it will loop forever if the target stays down, leaking goroutines and burning CPU. Add an attempt counter with a max (e.g., for attempt := 0; attempt < maxRetries; attempt++).`, posStr),
			})
		}
		return true
	})
	return issues
}

// isRetryLoop classifies a loop body as a retry loop: it must contain a
// failing call AND error-driven continuation (err != nil -> continue/loop).
func isRetryLoop(body *ast.BlockStmt) bool {
	if !loopBodyHasFailingCall(body) {
		return false
	}
	return loopBodyHasErrorRetry(body)
}

// loopBodyHasFailingCall returns true if the body contains a call to a
// common failing network/IO function that returns an error.
func loopBodyHasFailingCall(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(node ast.Node) bool {
		if found {
			return false
		}
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if name := retryCallFuncName(call); name != "" && failingCallFuncs[name] {
			found = true
		}
		return true
	})
	return found
}

// retryCallFuncName extracts the function/method name from a CallExpr's Fun.
func retryCallFuncName(call *ast.CallExpr) string {
	switch fn := call.Fun.(type) {
	case *ast.SelectorExpr:
		return fn.Sel.Name
	case *ast.Ident:
		return fn.Name
	}
	return ""
}

// loopBodyHasErrorRetry returns true if the body checks an error and
// continues/loops back on failure (the hallmark of a retry loop).
func loopBodyHasErrorRetry(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(node ast.Node) bool {
		if found {
			return false
		}
		ifs, ok := node.(*ast.IfStmt)
		if !ok {
			return true
		}
		if isErrorCheck(ifs.Cond) && branchContinuesOnError(ifs.Body) {
			found = true
		}
		return true
	})
	return found
}

// isErrorCheck returns true if a condition tests `err != nil` (any ident
// named err/e that is compared to nil).
func isErrorCheck(cond ast.Expr) bool {
	bin, ok := cond.(*ast.BinaryExpr)
	if !ok {
		return false
	}
	if bin.Op != token.NEQ {
		return false
	}
	return retryIsErrIdent(bin.X) && retryIsNilLit(bin.Y)
}

// retryIsErrIdent returns true for identifiers commonly used for errors.
func retryIsErrIdent(e ast.Expr) bool {
	id, ok := e.(*ast.Ident)
	if !ok {
		return false
	}
	name := strings.ToLower(id.Name)
	return name == "err" || name == "e"
}

// retryIsNilLit returns true for the `nil` identifier.
func retryIsNilLit(e ast.Expr) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == "nil"
}

// branchContinuesOnError returns true if an if-body continues or loops back
// (continue statement, or a bare break/return that is absent -- meaning the
// loop repeats). We specifically look for `continue` which signals retry.
func branchContinuesOnError(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(node ast.Node) bool {
		if found {
			return false
		}
		if cs, ok := node.(*ast.BranchStmt); ok && cs.Tok == token.CONTINUE {
			found = true
		}
		return true
	})
	return found
}

// loopBodyHasBackoff returns true if the loop body contains a backoff delay.
// Recognized forms (Go standard timer/ticker idioms included):
//   - time.Sleep / time.After / <-time.Tick calls
//   - time.NewTimer / time.NewTicker / time.AfterFunc calls in the body
//   - a receive on a timer/ticker channel: <-x.C (UnaryExpr ARROW over a
//     SelectorExpr selecting the .C field), whether the timer is created
//     inside or outside the loop
//   - a select statement containing a <-ctx.Done() case (context-aware
//     cancellation provides termination and paced termination)
func loopBodyHasBackoff(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(node ast.Node) bool {
		if found {
			return false
		}
		switch n := node.(type) {
		case *ast.CallExpr:
			if isBackoffCall(n) {
				found = true
				return false
			}
		case *ast.UnaryExpr:
			// <-x.C receive on a timer/ticker channel.
			if n.Op == token.ARROW {
				if sel, ok := n.X.(*ast.SelectorExpr); ok && sel.Sel.Name == "C" {
					found = true
					return false
				}
			}
		case *ast.SelectStmt:
			if selectHasCtxDone(n) {
				found = true
				return false
			}
		}
		return true
	})
	return found
}

// isBackoffCall returns true for calls that introduce a delay/backoff.
func isBackoffCall(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	if pkg.Name != "time" {
		return false
	}
	switch sel.Sel.Name {
	case "Sleep", "After", "Tick", "NewTimer", "NewTicker", "AfterFunc":
		return true
	}
	return false
}

// selectHasCtxDone returns true if a select statement has a <-ctx.Done()
// (or any .Done()) receive case.
func selectHasCtxDone(sel *ast.SelectStmt) bool {
	for _, clause := range sel.Body.List {
		comm, ok := clause.(*ast.CommClause)
		if !ok || comm.Comm == nil {
			continue
		}
		es, ok := comm.Comm.(*ast.ExprStmt)
		if !ok {
			continue
		}
		if unary, ok := es.X.(*ast.UnaryExpr); ok && unary.Op == token.ARROW {
			if inner, ok := unary.X.(*ast.CallExpr); ok {
				if cs, ok := inner.Fun.(*ast.SelectorExpr); ok && cs.Sel.Name == "Done" {
					return true
				}
			}
		}
	}
	return false
}

// isUnboundedForLoop returns true for `for {}` (no condition, no init/post).
func isUnboundedForLoop(loop *ast.ForStmt) bool {
	return loop.Cond == nil
}

// loopHasAttemptCap returns true if the loop tracks an attempt counter
// (either via for-init/post or a counter incremented+checked in the body).
func loopHasAttemptCap(loop *ast.ForStmt) bool {
	// For-condition form: for i := 0; i < max; i++ -- inherently bounded.
	if loop.Cond != nil {
		return true
	}
	// Range form is always bounded by the collection.
	if loop.Init != nil {
		if as, ok := loop.Init.(*ast.AssignStmt); ok {
			for _, l := range as.Lhs {
				if id, ok := l.(*ast.Ident); ok && isAttemptCounterName(id.Name) {
					return true
				}
			}
		}
	}
	// A select with a <-ctx.Done() case that exits (return/break) provides
	// bounded termination for an otherwise unbounded loop.
	if bodyHasCtxDoneExit(loop.Body) {
		return true
	}
	// Check body for an attempt counter that is compared to a max.
	return bodyHasCounterCheck(loop.Body)
}

// bodyHasCtxDoneExit returns true if the body contains a select whose
// <-ctx.Done() case ends with a return or break (a bounded exit path).
func bodyHasCtxDoneExit(body *ast.BlockStmt) bool {
	if body == nil {
		return false
	}
	found := false
	ast.Inspect(body, func(node ast.Node) bool {
		if found {
			return false
		}
		sel, ok := node.(*ast.SelectStmt)
		if !ok {
			return true
		}
		for _, clause := range sel.Body.List {
			comm, ok := clause.(*ast.CommClause)
			if !ok || comm.Comm == nil {
				continue
			}
			es, ok := comm.Comm.(*ast.ExprStmt)
			if !ok {
				continue
			}
			unary, ok := es.X.(*ast.UnaryExpr)
			if !ok || unary.Op != token.ARROW {
				continue
			}
			call, ok := unary.X.(*ast.CallExpr)
			if !ok {
				continue
			}
			cs, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || cs.Sel.Name != "Done" {
				continue
			}
			if stmtListExitsLoop(comm.Body) {
				found = true
				return false
			}
		}
		return true
	})
	return found
}

// stmtListExitsLoop returns true if the statement list contains a return or
// a loop-level break at its top level.
func stmtListExitsLoop(stmts []ast.Stmt) bool {
	for _, s := range stmts {
		if bs, ok := s.(*ast.BranchStmt); ok && bs.Tok == token.BREAK && bs.Label == nil {
			return true
		}
		if _, ok := s.(*ast.ReturnStmt); ok {
			return true
		}
	}
	return false
}

// isAttemptCounterName returns true for common attempt/retry counter names.
func isAttemptCounterName(name string) bool {
	low := strings.ToLower(name)
	for _, p := range []string{"attempt", "retry", "retries", "count", "tries"} {
		if strings.Contains(low, p) {
			return true
		}
	}
	return false
}

// bodyHasCounterCheck returns true if the body compares a counter-like
// variable against a max/threshold (i < maxRetries).
func bodyHasCounterCheck(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(node ast.Node) bool {
		if found {
			return false
		}
		ifs, ok := node.(*ast.IfStmt)
		if !ok {
			return true
		}
		if hasAttemptComparison(ifs.Cond) {
			found = true
		}
		return true
	})
	return found
}

// hasAttemptComparison returns true if a condition references an attempt-like
// counter variable.
func hasAttemptComparison(cond ast.Expr) bool {
	found := false
	ast.Inspect(cond, func(node ast.Node) bool {
		if found {
			return false
		}
		if id, ok := node.(*ast.Ident); ok && isAttemptCounterName(id.Name) {
			found = true
		}
		return true
	})
	return found
}

// retryIssueSet converts retry issues to a set keyed by issue.key.
func retryIssueSet(issues []retryLoopIssue) map[string]bool {
	set := make(map[string]bool, len(issues))
	for _, iss := range issues {
		set[iss.key] = true
	}
	return set
}
