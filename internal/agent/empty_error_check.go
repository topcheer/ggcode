package agent

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
)

func checkEmptyErrorBody(_ string, oldContent, newContent string) []string {
	if strings.TrimSpace(newContent) == "" {
		return nil
	}

	oldIssues := findEmptyErrorBodies(oldContent)
	newIssues := findEmptyErrorBodies(newContent)

	if strings.TrimSpace(oldContent) != "" && len(oldIssues) > 0 {
		oldSet := make(map[string]int, len(oldIssues))
		for _, oi := range oldIssues {
			oldSet[oi.fp]++
		}
		remaining := make([]emptyErrorInfo, 0, len(newIssues))
		for _, ni := range newIssues {
			if oldSet[ni.fp] > 0 {
				oldSet[ni.fp]--
			} else {
				remaining = append(remaining, ni)
			}
		}
		newIssues = remaining
	}
	if len(newIssues) == 0 {
		return nil
	}

	maxReport := 3
	if len(newIssues) > maxReport {
		newIssues = newIssues[:maxReport]
	}

	var warnings []string
	for _, ni := range newIssues {
		warnings = append(warnings, fmt.Sprintf(
			"[Empty Error Check] if err != nil with empty body at line %d: "+
				"error is checked but nothing is done about it. "+
				"Either handle the error (return/log/panic) or explicitly suppress with a comment explaining why.",
			ni.line))
	}
	return warnings
}

type emptyErrorInfo struct {
	line int
	fp   string
}

func findEmptyErrorBodies(content string) []emptyErrorInfo {
	fset := token.NewFileSet()

	tree, err := parser.ParseFile(fset, "", content, parser.ParseComments)
	if err != nil || tree == nil {
		return nil
	}
	commentRanges := make([][2]token.Pos, 0, len(tree.Comments))
	for _, cg := range tree.Comments {
		commentRanges = append(commentRanges, [2]token.Pos{cg.Pos(), cg.End()})
	}
	hasCommentInside := func(n ast.Node) bool {
		for _, r := range commentRanges {
			if r[0] >= n.Pos() && r[1] <= n.End() {
				return true
			}
		}
		return false
	}

	var result []emptyErrorInfo
	var curFunc string
	ast.Inspect(tree, func(n ast.Node) bool {
		if fd, ok := n.(*ast.FuncDecl); ok {
			curFunc = fd.Name.Name
		}
		ifStmt, ok := n.(*ast.IfStmt)
		if !ok {
			return true
		}

		if !eeIsErrorNilCheck(ifStmt.Cond) {
			return true
		}

		if ifStmt.Body == nil || (len(ifStmt.Body.List) == 0 && !hasCommentInside(ifStmt.Body)) {
			result = append(result, emptyErrorInfo{
				line: fset.Position(ifStmt.Pos()).Line,
				fp:   curFunc + "|" + exprText(ifStmt.Cond),
			})
			return true
		}

		if len(ifStmt.Body.List) == 0 {
			return true
		}

		allEmpty := true
		for _, stmt := range ifStmt.Body.List {
			if _, isEmpty := stmt.(*ast.EmptyStmt); !isEmpty {
				allEmpty = false
				break
			}
		}
		if allEmpty {
			result = append(result, emptyErrorInfo{
				line: fset.Position(ifStmt.Pos()).Line,
				fp:   curFunc + "|" + exprText(ifStmt.Cond),
			})
		}

		return true
	})

	return result
}

func eeIsErrorNilCheck(expr ast.Expr) bool {
	binExpr, ok := expr.(*ast.BinaryExpr)
	if !ok {
		return false
	}

	if binExpr.Op != token.NEQ {
		return false
	}

	return eeIsErrIdent(binExpr.X) && eeIsNilIdent(binExpr.Y) ||
		eeIsNilIdent(binExpr.X) && eeIsErrIdent(binExpr.Y)
}

func eeIsErrIdent(expr ast.Expr) bool {
	ident, ok := expr.(*ast.Ident)
	if !ok {
		return false
	}
	name := strings.ToLower(ident.Name)
	return name == "err" || name == "e" || strings.HasPrefix(name, "err") ||
		strings.HasSuffix(name, "err") || strings.HasSuffix(name, "error")
}

func eeIsNilIdent(expr ast.Expr) bool {
	ident, ok := expr.(*ast.Ident)
	return ok && ident.Name == "nil"
}
