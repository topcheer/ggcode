package agent

import (
	"fmt"
	"go/ast"
	"go/token"
	"path/filepath"
	"strings"
)

const (
	exitPathMaxNesting  = 3
	exitPathMaxWarnings = 4
)

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

	newIssues = filterExitPathDelta(newIssues, oldContent, filePath)
	if len(newIssues) == 0 {
		return nil
	}
	return buildExitPathWarnings(newIssues)
}

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

type exitPathIssue struct {
	kind    string
	line    int
	message string
}

func findExitPathIssues(filename, src string) []exitPathIssue {
	file, fset, err := parseGoSource(filename, src, 0)
	if err != nil || file == nil {
		return nil
	}

	var issues []exitPathIssue

	for _, decl := range file.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Body == nil {
			continue
		}

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

func checkRedundantElse(stmt *ast.IfStmt, fset *token.FileSet) []exitPathIssue {
	if stmt.Else == nil {
		return nil
	}

	if !blockEndsTerminating(stmt.Body) {
		return nil
	}

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

func blockEndsTerminating(block *ast.BlockStmt) bool {
	if block == nil || len(block.List) == 0 {
		return false
	}
	return stmtIsTerminating(block.List[len(block.List)-1])
}

func stmtIsTerminating(s ast.Stmt) bool {
	switch v := s.(type) {
	case *ast.ReturnStmt:
		return true
	case *ast.BranchStmt:
		return true
	case *ast.BlockStmt:
		return blockEndsTerminating(v)
	case *ast.IfStmt:

		if v.Else == nil {
			return false
		}
		return ifBranchTerminates(v.Body) && elseBranchTerminates(v.Else)
	case *ast.SelectStmt:

		return selectTerminates(v)
	case *ast.SwitchStmt:
		return switchTerminates(v)
	case *ast.ExprStmt:
		// #5021: the Go spec lists a call to the builtin panic as a
		// terminating statement; the warning text already promises it.
		call, ok := v.X.(*ast.CallExpr)
		if !ok {
			return false
		}
		ident, ok := call.Fun.(*ast.Ident)
		return ok && ident.Name == "panic"
	}
	return false
}

func ifBranchTerminates(block *ast.BlockStmt) bool {
	return blockEndsTerminating(block)
}

func elseBranchTerminates(elseStmt ast.Stmt) bool {
	switch v := elseStmt.(type) {
	case *ast.BlockStmt:
		return blockEndsTerminating(v)
	case *ast.IfStmt:

		if v.Else == nil {
			return false
		}
		return ifBranchTerminates(v.Body) && elseBranchTerminates(v.Else)
	}
	return false
}

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

func switchTerminates(sw *ast.SwitchStmt) bool {
	if sw.Body == nil || len(sw.Body.List) == 0 {
		return false
	}
	hasDefault := false
	for _, cc := range sw.Body.List {
		clause, ok := cc.(*ast.CaseClause)
		if !ok || !caseClauseTerminates(clause) {
			return false
		}
		if clause.List == nil {
			hasDefault = true
		}
	}
	return hasDefault
}

func commClauseTerminates(cc *ast.CommClause) bool {
	if len(cc.Body) == 0 {
		return false
	}
	return stmtIsTerminating(cc.Body[len(cc.Body)-1])
}

func caseClauseTerminates(cc *ast.CaseClause) bool {
	if len(cc.Body) == 0 {
		return false
	}
	return stmtIsTerminating(cc.Body[len(cc.Body)-1])
}

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

func countIfChainDepth(stmt *ast.IfStmt) int {
	depth := 1
	if stmt.Body != nil {
		childDepth := ifNestingDepth(stmt.Body)
		if childDepth+1 > depth {
			depth = childDepth + 1
		}
	}

	if elseIf, ok := stmt.Else.(*ast.IfStmt); ok {
		elseDepth := countIfChainDepth(elseIf)
		if elseDepth > depth {
			depth = elseDepth
		}
	}
	return depth
}
