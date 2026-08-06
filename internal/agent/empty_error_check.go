package agent

// Empty Error Check Body Detection
//
// Research basis: AI coding agents frequently generate placeholder error
// handling: `if err != nil { }` or `if err != nil { /* TODO */ }`. This
// silently swallows errors while giving the appearance of proper handling.
// Unlike error-swallowing (_ = fn()) and error-nopropagate (missing return),
// this catches the case where the error IS checked but nothing is DONE about it.
//
// OWASP relevance: Silent error suppression is a root cause of many security
// incidents - failed auth checks, failed input validation, failed crypto
// operations that proceed as if successful.
//
// Competitor analysis:
//   - Claude Code: no write-time detection
//   - Cursor: no detection (relies on external linters)
//   - Cline/OpenHands: no detection
//   - GitHub Copilot: no detection
//   - Aider: no detection
//   - staticcheck: does NOT detect this (SA series focuses on other patterns)
//
// ggcode's approach: AST-based detection of if-statements whose condition
// tests an error against nil, but whose body contains zero statements.
// Delta-aware: only flags patterns INTRODUCED by this edit.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
)

// checkEmptyErrorBody detects if-statements that check error against nil
// but have an empty body (no statements, or only empty/comment-only).
func checkEmptyErrorBody(_ string, oldContent, newContent string) []string {
	if strings.TrimSpace(newContent) == "" {
		return nil
	}

	oldCount := countEmptyErrorBodies(oldContent)
	newIssues := findEmptyErrorBodies(newContent)

	// Delta: allow as many as already existed.
	if len(newIssues) <= oldCount {
		return nil
	}

	// Only report the surplus (newly introduced) instances, up to max.
	maxReport := 3
	reported := 0
	startIdx := oldCount // skip pre-existing
	if startIdx < 0 {
		startIdx = 0
	}

	var warnings []string
	for i := startIdx; i < len(newIssues) && reported < maxReport; i++ {
		warnings = append(warnings, fmt.Sprintf(
			"[Empty Error Check] if err != nil with empty body at line %d: "+
				"error is checked but nothing is done about it. "+
				"Either handle the error (return/log/panic) or explicitly suppress with a comment explaining why.",
			newIssues[i].line))
		reported++
	}
	return warnings
}

// emptyErrorInfo records the location of an empty error check.
type emptyErrorInfo struct {
	line int
}

// findEmptyErrorBodies parses Go source and returns all instances of
// if-statements with error-nil checks that have empty bodies.
func findEmptyErrorBodies(content string) []emptyErrorInfo {
	fset := token.NewFileSet()
	tree, err := parser.ParseFile(fset, "", content, 0)
	if err != nil || tree == nil {
		return nil
	}

	var result []emptyErrorInfo
	ast.Inspect(tree, func(n ast.Node) bool {
		ifStmt, ok := n.(*ast.IfStmt)
		if !ok {
			return true
		}

		// Check if the condition tests an error against nil.
		if !eeIsErrorNilCheck(ifStmt.Cond) {
			return true
		}

		// Check if the body has zero statements.
		if ifStmt.Body == nil || len(ifStmt.Body.List) == 0 {
			result = append(result, emptyErrorInfo{
				line: fset.Position(ifStmt.Pos()).Line,
			})
			return true
		}

		// Check if body has only empty statements (e.g., just a semicolon).
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
			})
		}

		return true
	})

	return result
}

// countEmptyErrorBodies returns the count of empty error check bodies
// in the given content. Used for delta comparison.
func countEmptyErrorBodies(content string) int {
	return len(findEmptyErrorBodies(content))
}

// eeIsErrorNilCheck returns true if the expression is an error-vs-nil comparison.
// Matches patterns like: err != nil, err == nil, nil != err, nil == err
func eeIsErrorNilCheck(expr ast.Expr) bool {
	binExpr, ok := expr.(*ast.BinaryExpr)
	if !ok {
		return false
	}

	if binExpr.Op != token.NEQ && binExpr.Op != token.EQL {
		return false
	}

	// Check both orderings: "err != nil" and "nil != err"
	return eeIsErrIdent(binExpr.X) && eeIsNilIdent(binExpr.Y) ||
		eeIsNilIdent(binExpr.X) && eeIsErrIdent(binExpr.Y)
}

// eeIsErrIdent checks if the expression is an identifier that looks like an error.
func eeIsErrIdent(expr ast.Expr) bool {
	ident, ok := expr.(*ast.Ident)
	if !ok {
		return false
	}
	name := strings.ToLower(ident.Name)
	return name == "err" || name == "e" || strings.HasPrefix(name, "err") ||
		strings.HasSuffix(name, "err") || strings.HasSuffix(name, "error")
}

// eeIsNilIdent checks if the expression is the nil identifier.
func eeIsNilIdent(expr ast.Expr) bool {
	ident, ok := expr.(*ast.Ident)
	return ok && ident.Name == "nil"
}
