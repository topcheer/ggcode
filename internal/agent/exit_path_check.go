package agent

// Exit Path & Early Return Intelligence (Check #57)
//
// Problem: AI coding agents frequently produce Go code with control flow
// anti-patterns that hurt readability and maintainability. The two most
// common patterns are:
//
//  1. Redundant else after return/break/continue/panic (guard clause violation):
//     When an if-block's body ends with a terminating statement (return, break,
//     continue, goto, panic), the else clause is unnecessary. The else body
//     can be de-indented to the outer scope.
//
//  2. Deep nesting and guard clause opportunity:
//     When if-statements are nested 3+ levels deep, the function becomes hard
//     to read. Go idiom prefers early returns (guard clauses) to flatten
//     nesting.
//
// Competitor analysis:
//   - staticcheck: does not check redundant else or deep nesting
//   - gocritic: has `elseif` (suggests if-elseif chains) but NOT redundant-else-after-return
//   - go vet: no check for either pattern
//   - Claude Code/Cursor/Aider: no write-time detection
//   - CodeRabbit: may suggest in review comments, but not at write time
//
// Approach: AST-based analysis, zero LLM cost. Delta-aware: only flags
// patterns newly introduced by this edit.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

const (
	exitPathMaxNesting  = 3 // flag if nesting depth >= this
	exitPathMaxWarnings = 4
)

// checkExitPath detects redundant-else-after-termination and deep nesting
// guard-clause opportunities in Go code. Returns warning strings. Delta-aware.
func checkExitPath(filePath, oldContent, newContent string) []string {
	if filepath.Ext(filePath) != ".go" {
		return nil
	}
	if strings.TrimSpace(newContent) == "" {
		return nil
	}
	if isTestFile(filePath) {
		return nil
	}

	newIssues := findExitPathIssues(filePath, newContent)
	if len(newIssues) == 0 {
		return nil
	}

	// Delta-aware: subtract issues present in old content.
	newIssues = filterExitPathDelta(newIssues, oldContent, filePath)
	if len(newIssues) == 0 {
		return nil
	}
	return buildExitPathWarnings(newIssues)
}

// filterExitPathDelta removes issues that already existed in oldContent.
func filterExitPathDelta(newIssues []exitPathIssue, oldContent, filePath string) []exitPathIssue {
	if strings.TrimSpace(oldContent) == "" {
		return newIssues
	}
	oldIssues := findExitPathIssues(filePath, oldContent)
	if len(oldIssues) == 0 {
		return newIssues
	}
	oldSet := make(map[string]bool, len(oldIssues))
	for _, oi := range oldIssues {
		oldSet[oi.message] = true
	}
	filtered := newIssues[:0]
	for _, ni := range newIssues {
		if oldSet[ni.message] {
			continue
		}
		filtered = append(filtered, ni)
	}
	return filtered
}

// buildExitPathWarnings converts issues into human-readable warning strings.
func buildExitPathWarnings(issues []exitPathIssue) []string {
	redundantCount := 0
	deepNestCount := 0
	for _, issue := range issues {
		switch issue.kind {
		case "redundant-else":
			redundantCount++
		case "deep-nesting":
			deepNestCount++
		}
	}
	var warnings []string
	if redundantCount > 0 {
		warnings = append(warnings, fmt.Sprintf(
			"Found %d redundant else clause(s) after return/break/continue/panic. "+
				"When the if-body ends with a terminating statement, the else is unnecessary -- "+
				"remove the else and de-indent its body to improve readability.",
			redundantCount))
	}
	if deepNestCount > 0 {
		warnings = append(warnings, fmt.Sprintf(
			"Found %d deeply nested if-block(s) (depth >= %d). "+
				"Consider converting to guard clauses (early returns) to flatten the control flow "+
				"and improve readability.",
			deepNestCount, exitPathMaxNesting))
	}
	if len(warnings) > exitPathMaxWarnings {
		warnings = warnings[:exitPathMaxWarnings]
	}
	return warnings
}

// exitPathIssue records a single exit-path quality issue.
type exitPathIssue struct {
	kind    string // "redundant-else" or "deep-nesting"
	line    int
	message string
}

// findExitPathIssues parses Go source and finds all exit-path quality issues.
func findExitPathIssues(filename, src string) []exitPathIssue {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, src, 0)
	if err != nil || file == nil {
		return nil
	}

	var issues []exitPathIssue

	for _, decl := range file.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Body == nil {
			continue
		}
		// Skip main/init -- bootstrap functions have different conventions.
		if fd.Name.Name == "main" || fd.Name.Name == "init" {
			continue
		}
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			if rs, ok := n.(*ast.IfStmt); ok {
				issues = append(issues, checkRedundantElse(rs, fset)...)
				issues = append(issues, checkDeepNesting(rs, fset)...)
			}
			return true
		})
	}

	return issues
}

// checkRedundantElse detects if-blocks that end with a terminating statement
// but have an else clause.
func checkRedundantElse(stmt *ast.IfStmt, fset *token.FileSet) []exitPathIssue {
	if stmt.Else == nil {
		return nil
	}
	// Only flag if the if-body's last statement is terminating.
	if !blockEndsTerminating(stmt.Body) {
		return nil
	}

	// Skip else-if chains: if the else is another IfStmt, it's a chain,
	// not a standalone else block. These are acceptable for readability.
	if _, ok := stmt.Else.(*ast.IfStmt); ok {
		return nil
	}

	pos := fset.Position(stmt.Else.Pos())
	msg := fmt.Sprintf("redundant-else@L%d", pos.Line)
	return []exitPathIssue{{
		kind:    "redundant-else",
		line:    pos.Line,
		message: msg,
	}}
}

// checkDeepNesting detects if-statements nested at depth >= exitPathMaxNesting.
func checkDeepNesting(stmt *ast.IfStmt, fset *token.FileSet) []exitPathIssue {
	depth := 1 + ifNestingDepth(stmt.Body)
	if depth < exitPathMaxNesting {
		return nil
	}
	pos := fset.Position(stmt.Pos())
	msg := fmt.Sprintf("deep-nesting@L%d", pos.Line)
	return []exitPathIssue{{
		kind:    "deep-nesting",
		line:    pos.Line,
		message: msg,
	}}
}

// blockEndsTerminating returns true if the block's last statement is a
// terminating statement: return, break, continue, goto, panic, or
// a block/if that itself ends terminating.
func blockEndsTerminating(block *ast.BlockStmt) bool {
	if block == nil || len(block.List) == 0 {
		return false
	}
	return stmtIsTerminating(block.List[len(block.List)-1])
}

// stmtIsTerminating checks whether a statement unconditionally transfers
// control flow (making subsequent code and else clauses unreachable).
func stmtIsTerminating(s ast.Stmt) bool {
	switch v := s.(type) {
	case *ast.ReturnStmt:
		return true
	case *ast.BranchStmt: // break, continue, goto
		return true
	case *ast.BlockStmt:
		return blockEndsTerminating(v)
	case *ast.IfStmt:
		// if-else where BOTH branches terminate is terminating.
		if v.Else == nil {
			return false
		}
		return ifBranchTerminates(v.Body) && elseBranchTerminates(v.Else)
	case *ast.SelectStmt:
		// Select with all cases having break/return is terminating.
		// Conservative: only flag if all cases end terminating.
		return selectTerminates(v)
	case *ast.SwitchStmt:
		return switchTerminates(v)
	}
	return false
}

// ifBranchTerminates checks if an if-body block ends with terminating stmt.
func ifBranchTerminates(block *ast.BlockStmt) bool {
	return blockEndsTerminating(block)
}

// elseBranchTerminates checks if the else branch (could be IfStmt or BlockStmt)
// terminates.
func elseBranchTerminates(elseStmt ast.Stmt) bool {
	switch v := elseStmt.(type) {
	case *ast.BlockStmt:
		return blockEndsTerminating(v)
	case *ast.IfStmt:
		// else-if: recursively check
		if v.Else == nil {
			return false
		}
		return ifBranchTerminates(v.Body) && elseBranchTerminates(v.Else)
	}
	return false
}

// selectTerminates returns true if all select cases end with terminating stmt.
func selectTerminates(sel *ast.SelectStmt) bool {
	if sel.Body == nil || len(sel.Body.List) == 0 {
		return false
	}
	for _, cc := range sel.Body.List {
		clause, ok := cc.(*ast.CommClause)
		if !ok || !commClauseTerminates(clause) {
			return false
		}
	}
	return true
}

// switchTerminates returns true if all switch cases end with terminating stmt.
func switchTerminates(sw *ast.SwitchStmt) bool {
	if sw.Body == nil || len(sw.Body.List) == 0 {
		return false
	}
	for _, cc := range sw.Body.List {
		clause, ok := cc.(*ast.CaseClause)
		if !ok || !caseClauseTerminates(clause) {
			return false
		}
	}
	return true
}

// commClauseTerminates checks if a select CommClause ends terminating.
func commClauseTerminates(cc *ast.CommClause) bool {
	if len(cc.Body) == 0 {
		return false
	}
	return stmtIsTerminating(cc.Body[len(cc.Body)-1])
}

// caseClauseTerminates checks if a switch CaseClause ends terminating.
func caseClauseTerminates(cc *ast.CaseClause) bool {
	if len(cc.Body) == 0 {
		return false
	}
	return stmtIsTerminating(cc.Body[len(cc.Body)-1])
}

// ifNestingDepth computes the maximum depth of nested if-statements within
// the given block. Returns 1 for a single if with no nested ifs.
func ifNestingDepth(block *ast.BlockStmt) int {
	if block == nil {
		return 0
	}
	maxDepth := 0
	for _, stmt := range block.List {
		depth := stmtIfDepth(stmt)
		if depth > maxDepth {
			maxDepth = depth
		}
	}
	return maxDepth
}

// stmtIfDepth returns the nesting depth of if-statements within a statement.
func stmtIfDepth(s ast.Stmt) int {
	if s == nil {
		return 0
	}
	maxDepth := 0
	ast.Inspect(s, func(n ast.Node) bool {
		if ifStmt, ok := n.(*ast.IfStmt); ok {
			d := countIfChainDepth(ifStmt)
			if d > maxDepth {
				maxDepth = d
			}
		}
		return true
	})
	return maxDepth
}

// countIfChainDepth recursively counts how deep if-statements are nested.
func countIfChainDepth(stmt *ast.IfStmt) int {
	depth := 1
	if stmt.Body != nil {
		childDepth := ifNestingDepth(stmt.Body)
		if childDepth+1 > depth {
			depth = childDepth + 1
		}
	}
	// Check else-if chain depth
	if elseIf, ok := stmt.Else.(*ast.IfStmt); ok {
		elseDepth := countIfChainDepth(elseIf)
		if elseDepth > depth {
			depth = elseDepth
		}
	}
	return depth
}
