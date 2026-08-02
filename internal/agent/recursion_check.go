package agent

// Unbounded recursion detection for Go files.
//
// Problem: AI coding agents frequently write recursive functions but forget
// the base case (termination condition). The classic example:
//
//	func factorial(n int) int {
//	    return n * factorial(n-1) // missing: if n <= 1 { return 1 }
//	}
//
// This causes a stack overflow panic at runtime -- the program crashes with no
// compile-time warning. Go's compiler does not detect unbounded recursion.
//
// Competitor analysis:
//   - Claude Code: no inline detection (relies on agent self-judgment)
//   - Cursor: lint-on-save doesn't catch missing base cases
//   - Cline/OpenHands: reactive only -- caught by tests or production crashes
//   - Aider: no detection
//   - Devin: no inline detection
//
// External tools: staticcheck has no rule for this. go vet doesn't detect it.
// There is no widely-used Go linter that catches missing base cases.
//
// Approach: AST-based analysis. For each function that calls itself directly,
// we check whether EVERY execution path through the function body includes a
// recursive call. If so, the function has no termination path and will always
// overflow the stack. This is a guaranteed runtime panic with near-zero false
// positives -- a function where every path recurses unconditionally is always
// a bug.
//
// Delta-aware: only flags NEW unbounded recursion introduced by this edit.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

// recursionInfo records a single unbounded recursion occurrence.
type recursionInfo struct {
	funcName string
	line     int
}

// checkUnboundedRecursion detects Go functions that call themselves on every
// execution path (no base case / termination condition). Returns warning
// strings. Only flags NEW occurrences (delta-aware).
func checkUnboundedRecursion(filePath, oldContent, newContent string) []string {
	if filepath.Ext(filePath) != ".go" {
		return nil
	}
	if strings.TrimSpace(newContent) == "" {
		return nil
	}
	if isTestFile(filePath) {
		return nil
	}

	oldIssues := findUnboundedRecursion(filePath, oldContent)
	newIssues := findUnboundedRecursion(filePath, newContent)

	// Collect function names that are NEW (not present in oldIssues).
	oldNames := make(map[string]bool)
	for _, o := range oldIssues {
		oldNames[o.funcName] = true
	}

	var names []string
	for _, n := range newIssues {
		if !oldNames[n.funcName] {
			names = append(names, n.funcName)
		}
	}

	if len(names) == 0 {
		return nil
	}

	funcList := strings.Join(names, ", ")
	return []string{
		fmt.Sprintf(
			"Introduced unbounded recursion in function(s): %s. "+
				"Every execution path through these functions calls itself with no "+
				"termination condition (base case), which causes a guaranteed stack "+
				"overflow panic at runtime. Add a base case that returns without "+
				"recursing (e.g., if n <= 0 { return default }) before the recursive call.",
			funcList),
	}
}

// findUnboundedRecursion parses Go source and returns all functions where
// every path through the body includes a self-call (no base case).
func findUnboundedRecursion(filename, src string) []recursionInfo {
	if strings.TrimSpace(src) == "" {
		return nil
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, src, 0)
	if err != nil {
		return nil
	}

	var results []recursionInfo

	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil || fn.Name == nil {
			continue
		}

		funcName := fn.Name.Name
		if funcName == "" {
			continue
		}

		if !callsSelf(fn.Body, funcName) {
			continue
		}

		// A function is "always recursing" if every possible path through its
		// body leads to a self-call. We model this with three path categories.
		if !hasNonRecursivePath(fn.Body, funcName) {
			pos := fset.Position(fn.Pos())
			results = append(results, recursionInfo{
				funcName: funcName,
				line:     pos.Line,
			})
		}
	}

	return results
}

// callsSelf returns true if the body contains at least one direct self-call.
func callsSelf(body *ast.BlockStmt, funcName string) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == funcName {
				found = true
				return false
			}
		}
		return true
	})
	return found
}

// pathCategory classifies the behavior of a statement list (or block).
type pathCategory int

const (
	// pathAlwaysRecurses: every path through this code includes a self-call.
	pathAlwaysRecurses pathCategory = iota
	// pathEscapes: at least one path exits the function WITHOUT a self-call
	// (via a bare return, or a return with non-recursive expressions).
	pathEscapes
	// pathFallsThrough: at least one path reaches the END of the code without
	// a self-call, continuing to whatever follows (which may or may not
	// include recursion).
	pathFallsThrough
)

// hasNonRecursivePath returns true if there exists at least one path through
// the body that avoids self-calls entirely.
func hasNonRecursivePath(body *ast.BlockStmt, funcName string) bool {
	return analyzeStmtList(body.List, funcName) != pathAlwaysRecurses
}

// analyzeStmtList determines whether every execution path through a list of
// statements includes a self-call.
//
// Returns:
//   - pathAlwaysRecurses: every path hits a self-call
//   - pathEscapes: some path exits the function without recursion
//   - pathFallsThrough: some path reaches the end without recursion
func analyzeStmtList(stmts []ast.Stmt, funcName string) pathCategory {
	canEscape := false     // at least one path exits without recursion
	canFallThrough := true // at least one path falls through to the end

	for _, stmt := range stmts {
		if !canFallThrough {
			break // previous statement guaranteed exit or recursion
		}

		cat := analyzeStmt(stmt, funcName)

		switch cat {
		case pathAlwaysRecurses:
			// This statement forces recursion on the fall-through path.
			// The remaining statements on this path are irrelevant.
			canFallThrough = false
			// But we don't set canEscape -- the forced recursion path is bad.
		case pathEscapes:
			// This statement guarantees the function returns without recursion.
			// Statements after it are unreachable on this path.
			canEscape = true
			canFallThrough = false
		case pathFallsThrough:
			// This statement can pass through without recursion.
			// Continue checking subsequent statements.
		}
	}

	if canEscape {
		return pathEscapes
	}
	if canFallThrough {
		return pathFallsThrough
	}
	return pathAlwaysRecurses
}

// analyzeStmt classifies a single statement.
func analyzeStmt(stmt ast.Stmt, funcName string) pathCategory {
	switch s := stmt.(type) {
	case *ast.ReturnStmt:
		for _, expr := range s.Results {
			if exprCallsSelf(expr, funcName) {
				return pathAlwaysRecurses
			}
		}
		return pathEscapes

	case *ast.IfStmt:
		// Analyze the "then" branch.
		thenCat := analyzeStmtList(s.Body.List, funcName)

		// Analyze the "else" branch (if any).
		var elseCat pathCategory
		if s.Else == nil {
			// No else: the "else" path skips the if and falls through to
			// subsequent statements.
			elseCat = pathFallsThrough
		} else {
			elseStmt, ok := s.Else.(*ast.BlockStmt)
			if ok {
				elseCat = analyzeStmtList(elseStmt.List, funcName)
			} else {
				// else-if: wrap the single statement.
				elseCat = analyzeStmt(s.Else, funcName)
			}
		}

		return combinePaths(thenCat, elseCat)

	case *ast.SwitchStmt:
		if s.Body == nil || len(s.Body.List) == 0 {
			return pathFallsThrough
		}

		// A switch always falls through if there's no default case and none of
		// the cases match -- that path avoids recursion.
		// If all cases always recurse AND there's a default that always recurses,
		// then the switch always recurses.
		// Otherwise, if any case escapes or falls through, the switch has a
		// non-recursive path.

		hasDefault := false
		for _, cc := range s.Body.List {
			caseClause, ok := cc.(*ast.CaseClause)
			if !ok {
				continue
			}
			cat := analyzeStmtList(caseClause.Body, funcName)
			if cat != pathAlwaysRecurses {
				// This case has a non-recursive path. But we need to be careful:
				// in Go, switch cases fall through to the next case unless there's
				// a break/return. For simplicity, if any case can escape or
				// fall through, the switch has a non-recursive path.
				return pathFallsThrough
			}
			if caseClause.List == nil {
				hasDefault = true
			}
		}

		// All cases always recurse.
		if hasDefault {
			return pathAlwaysRecurses
		}
		// No default: the switch can be skipped entirely (no case matches).
		return pathFallsThrough

	case *ast.ForStmt, *ast.RangeStmt:
		// A loop can be skipped if its condition is initially false.
		// The skipping path falls through without recursion.
		return pathFallsThrough

	case *ast.ExprStmt:
		if exprCallsSelf(s.X, funcName) {
			return pathAlwaysRecurses
		}
		return pathFallsThrough

	case *ast.AssignStmt:
		for _, expr := range s.Rhs {
			if exprCallsSelf(expr, funcName) {
				return pathAlwaysRecurses
			}
		}
		return pathFallsThrough

	case *ast.BlockStmt:
		return analyzeStmtList(s.List, funcName)

	case *ast.LabeledStmt:
		return analyzeStmt(s.Stmt, funcName)

	case *ast.BranchStmt:
		// break/continue/goto -- exits the current flow without recursion.
		return pathEscapes

	case *ast.DeferStmt:
		// defer f() -- does not execute immediately, falls through.
		return pathFallsThrough

	case *ast.GoStmt:
		return pathFallsThrough

	case *ast.SendStmt, *ast.IncDecStmt, *ast.DeclStmt:
		return pathFallsThrough

	default:
		return pathFallsThrough
	}
}

// combinePaths merges the behavior of two branches (e.g., if/else).
// The combined result reflects the worst case across all branches.
func combinePaths(a, b pathCategory) pathCategory {
	// If EITHER branch can escape or fall through, the combined result is
	// not "always recurses" -- we can take that branch.
	//
	// pathEscapes > pathFallsThrough > pathAlwaysRecurses
	// in terms of "how safe" the path is.
	//
	// We want the MOST non-recursive path available.
	if a == pathEscapes || b == pathEscapes {
		return pathEscapes
	}
	if a == pathFallsThrough || b == pathFallsThrough {
		return pathFallsThrough
	}
	return pathAlwaysRecurses
}

// exprCallsSelf checks if an expression contains a direct self-call.
func exprCallsSelf(expr ast.Expr, funcName string) bool {
	found := false
	ast.Inspect(expr, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == funcName {
				found = true
				return false
			}
		}
		return true
	})
	return found
}
