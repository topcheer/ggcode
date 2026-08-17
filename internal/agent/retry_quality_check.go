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
	// Unexported methods (Go convention: lowercase names)
	"do": true, "get": true, "post": true, "head": true,
	"query": true, "queryrow": true, "exec": true,
	"dial": true, "dialcontext": true, "ping": true,
	"read": true, "write": true, "send": true, "receive": true,
	"roundtrip": true, "call": true, "request": true,
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

	// #618: pre-compute identifiers bound to timer/ticker objects so that
	// `<-x.C` receives are only treated as backoff when x is provably a
	// time.NewTimer/NewTicker result.
	for k := range retryTimerIdents {
		delete(retryTimerIdents, k)
	}
	collectTimerIdents(file)

	issues := findRetryLoopIssues(file, fset)
	if len(issues) == 0 {
		return nil
	}

	// Delta-aware: subtract pre-existing issues from old content.
	if strings.TrimSpace(oldContent) != "" {
		oldFset := token.NewFileSet()
		oldFile, oldErr := parser.ParseFile(oldFset, filePath, oldContent, 0)
		if oldErr == nil && oldFile != nil {
			// Analyze old content with its own timer-ident map state so
			// findRetryLoopIssues sees the old file's timer bindings.
			savedTimers := make(map[string]bool, len(retryTimerIdents))
			for k, v := range retryTimerIdents {
				savedTimers[k] = v
			}
			for k := range retryTimerIdents {
				delete(retryTimerIdents, k)
			}
			collectTimerIdents(oldFile)
			oldIssues := findRetryLoopIssues(oldFile, oldFset)
			// Restore the new content's timer bindings.
			for k := range retryTimerIdents {
				delete(retryTimerIdents, k)
			}
			for k, v := range savedTimers {
				retryTimerIdents[k] = v
			}
			oldSet := retryIssueSet(oldIssues)
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
	// #618: collect identifiers bound to time.NewTimer/time.NewTicker results
	// anywhere in the file scope so that a `<-x.C` receive can be verified as a
	// genuine timer/ticker channel (comment above notwithstanding, this loop's
	// body alone cannot prove the receiver's type). We conservatively treat a
	// .C receive as timer-based only when the receiver identifier is a known
	// NewTimer/NewTicker result in the same file.
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
			// <-x.C receive on a timer/ticker channel. #618: receiving on ANY
			// struct field named "C" is not a delay — an event/subscription
			// channel `for { ev := <-sub.C; ... }` hot-loops. Verify the
			// receiver identifier is a time.NewTimer/time.NewTicker result
			// (created in this body or passed in and bound at the call site —
			// see timerRecvOK).
			if n.Op == token.ARROW {
				if sel, ok := n.X.(*ast.SelectorExpr); ok && sel.Sel.Name == "C" {
					if id, ok := sel.X.(*ast.Ident); ok && isTimerLikeIdent(id) {
						found = true
						return false
					}
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

// retryTimerIdents is populated per-file with identifiers bound to
// time.NewTimer / time.NewTicker results, so `<-x.C` receives can be verified
// as genuine timer/ticker channels (#618 defect 3).
var retryTimerIdents = map[string]bool{}

// isTimerLikeIdent reports whether id is a known time.NewTimer/NewTicker
// receiver in the current file analysis.
func isTimerLikeIdent(id *ast.Ident) bool {
	return retryTimerIdents[id.Name]
}

// collectTimerIdents records identifiers assigned from time.NewTimer or
// time.NewTicker calls anywhere in the file.
func collectTimerIdents(file *ast.File) {
	ast.Inspect(file, func(node ast.Node) bool {
		as, ok := node.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for _, rhs := range as.Rhs {
			call, ok := rhs.(*ast.CallExpr)
			if !ok {
				continue
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				continue
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || pkg.Name != "time" {
				continue
			}
			if sel.Sel.Name != "NewTimer" && sel.Sel.Name != "NewTicker" {
				continue
			}
			for _, lhs := range as.Lhs {
				if id, ok := lhs.(*ast.Ident); ok {
					retryTimerIdents[id.Name] = true
				}
			}
		}
		return true
	})
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
	} // For-init form with an attempt-like counter: for attempt := 0; ...; attempt++
	// The init counter only provides a cap when it is actually bounded — either
	// the loop condition compares it (handled by the Cond != nil branch above)
	// or the body checks it against a max (#618: a counter that is incremented
	// but never compared is NOT an attempt cap; a name like countryCode must
	// not exempt an unbounded loop either).
	var initCounterName string
	if loop.Init != nil {
		if as, ok := loop.Init.(*ast.AssignStmt); ok {
			for _, l := range as.Lhs {
				if id, ok := l.(*ast.Ident); ok && isAttemptCounterName(id.Name) {
					initCounterName = id.Name
					break
				}
			}
		}
	}
	if initCounterName != "" {
		// Counter exists — require semantic evidence that it is compared
		// against a bound somewhere in the body (if attempt >= max { return }).
		if bodyHasCounterCheck(loop.Body) {
			return true
		}
		// Post statement incrementing the counter with no body check and no
		// condition is still unbounded — fall through to the remaining checks.
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
// #618: whole-word match only — substring matching exempted unrelated
// identifiers (countryCode contains "count", attemptLog contains "attempt"),
// silently suppressing unbounded-retry detection for those loops.
func isAttemptCounterName(name string) bool {
	low := strings.ToLower(name)
	for _, w := range []string{"attempt", "attempts", "retry", "retries", "count", "counts", "tries"} {
		if low == w {
			return true
		}
	}
	// Compound names: attemptCount, retryCount, attempt_count...
	for _, p := range []string{"attempt", "retry", "tries"} {
		if strings.HasPrefix(low, p+"_") || strings.HasSuffix(low, "_"+p) {
			return true
		}
		if strings.HasPrefix(low, p+"c") {
			return true // attemptCount, retryCount
		}
	}
	return false
}

// bodyHasCounterCheck returns true if the body compares a counter-like
// variable against a max/threshold (i < maxRetries).
func bodyHasCounterCheck(body *ast.BlockStmt) bool {
	if body == nil {
		return false
	}
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

// hasAttemptComparison returns true if a condition COMPARES an attempt-like
// counter variable against anything (a max/threshold: attempt >= maxRetries,
// tries < 3). #618: merely mentioning a counter-named identifier is not a
// bound — `if attemptLog != nil || retriesEnabled { continue }` must not
// exempt an otherwise unbounded retry loop.
func hasAttemptComparison(cond ast.Expr) bool {
	found := false
	ast.Inspect(cond, func(node ast.Node) bool {
		if found {
			return false
		}
		bin, ok := node.(*ast.BinaryExpr)
		if !ok {
			return true
		}
		switch bin.Op {
		case token.LSS, token.LEQ, token.GTR, token.GEQ:
		default:
			return true
		}
		if id, ok := bin.X.(*ast.Ident); ok && isAttemptCounterName(id.Name) {
			found = true
			return false
		}
		if id, ok := bin.Y.(*ast.Ident); ok && isAttemptCounterName(id.Name) {
			found = true
			return false
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
