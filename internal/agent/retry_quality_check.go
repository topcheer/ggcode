package agent

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

type retryLoopIssue struct {
	key     string
	kind    string
	message string
}

var failingCallFuncs = map[string]bool{
	"Do": true, "Get": true, "Post": true, "Head": true,
	"Query": true, "QueryRow": true, "Exec": true,
	"Dial": true, "DialContext": true, "Ping": true,
	"Read": true, "Write": true, "Send": true, "Receive": true,
	"RoundTrip": true, "Call": true, "Request": true,

	"do": true, "get": true, "post": true, "head": true,
	"query": true, "queryrow": true, "exec": true,
	"dial": true, "dialcontext": true, "ping": true,
	"read": true, "write": true, "send": true, "receive": true,
	"roundtrip": true, "call": true, "request": true,
}

func checkRetryQuality(filePath, oldContent, newContent string) []string {
	if filepath.Ext(filePath) != ".go" {
		return nil
	}
	if strings.TrimSpace(newContent) == "" {
		return nil
	}

	file, fset, err := parseGoSource(filePath, newContent, 0)
	if err != nil || file == nil {
		return nil
	}

	timers := collectTimerIdents(file)

	issues := findRetryLoopIssues(file, fset, timers)
	if len(issues) == 0 {
		return nil
	}

	if strings.TrimSpace(oldContent) != "" {
		oldFset := token.NewFileSet()
		oldFile, oldErr := parser.ParseFile(oldFset, filePath, oldContent, 0)
		if oldErr == nil && oldFile != nil {
			oldIssues := func() []retryLoopIssue {

				return findRetryLoopIssues(oldFile, oldFset, collectTimerIdents(oldFile))
			}()
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

func findRetryLoopIssues(file *ast.File, fset *token.FileSet, timers map[string]bool) []retryLoopIssue {
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
		if !loopBodyHasBackoff(loop.Body, timers) {
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

func isRetryLoop(body *ast.BlockStmt) bool {
	if !loopBodyHasFailingCall(body) {
		return false
	}
	return loopBodyHasErrorRetry(body)
}

func rnpNestedLoopBarrier(node ast.Node) bool {
	switch node.(type) {
	case *ast.ForStmt, *ast.RangeStmt:
		return false
	}
	return true
}

func loopBodyHasFailingCall(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(node ast.Node) bool {
		if found {
			return false
		}
		if !rnpNestedLoopBarrier(node) {
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

func retryCallFuncName(call *ast.CallExpr) string {
	switch fn := call.Fun.(type) {
	case *ast.SelectorExpr:
		return fn.Sel.Name
	case *ast.Ident:
		return fn.Name
	}
	return ""
}

func loopBodyHasErrorRetry(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(node ast.Node) bool {
		if found {
			return false
		}
		switch node.(type) {
		case *ast.ForStmt, *ast.RangeStmt:

			return false
		default:
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

func retryIsErrIdent(e ast.Expr) bool {
	id, ok := e.(*ast.Ident)
	if !ok {
		return false
	}
	name := strings.ToLower(id.Name)
	return name == "err" || name == "e"
}

func retryIsNilLit(e ast.Expr) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == "nil"
}

func branchContinuesOnError(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(node ast.Node) bool {
		if found {
			return false
		}
		if !rnpNestedLoopBarrier(node) {
			return false
		}
		if cs, ok := node.(*ast.BranchStmt); ok && cs.Tok == token.CONTINUE {
			found = true
		}
		return true
	})
	return found
}

func loopBodyHasBackoff(body *ast.BlockStmt, timers map[string]bool) bool {
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

			if n.Op == token.ARROW {
				if sel, ok := n.X.(*ast.SelectorExpr); ok && sel.Sel.Name == "C" {
					if id, ok := sel.X.(*ast.Ident); ok && timers[id.Name] {
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

func collectTimerIdents(file *ast.File) map[string]bool {
	timers := make(map[string]bool)
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
					timers[id.Name] = true
				}
			}
		}
		return true
	})
	return timers
}

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

func isUnboundedForLoop(loop *ast.ForStmt) bool {
	return loop.Cond == nil
}

func loopHasAttemptCap(loop *ast.ForStmt) bool {

	if loop.Cond != nil {
		return true
	}
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

		if bodyHasCounterCheck(loop.Body) {
			return true
		}

	}

	if bodyHasCtxDoneExit(loop.Body) {
		return true
	}

	return bodyHasCounterCheck(loop.Body)
}

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

func isAttemptCounterName(name string) bool {
	low := strings.ToLower(name)
	for _, w := range []string{"attempt", "attempts", "retry", "retries", "count", "counts", "tries"} {
		if low == w {
			return true
		}
	}

	for _, p := range []string{"attempt", "retry", "tries"} {
		if strings.HasPrefix(low, p+"_") || strings.HasSuffix(low, "_"+p) {
			return true
		}
		if strings.HasPrefix(low, p+"c") {
			return true
		}
	}
	return false
}

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

func retryIssueSet(issues []retryLoopIssue) map[string]bool {
	set := make(map[string]bool, len(issues))
	for _, iss := range issues {
		set[iss.key] = true
	}
	return set
}
