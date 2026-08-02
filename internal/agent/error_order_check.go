package agent

// Result-Used-Before-Error-Check Detection in Go Code
//
// Problem: AI coding agents frequently produce Go code that uses the result of
// an error-returning function call before checking the error. The classic
// pattern is:
//
//	resp, err := http.Get(url)
//	defer resp.Body.Close()  // BUG: if err != nil, resp is nil -> nil pointer panic
//	if err != nil {
//	    return err
//	}
//
// When err is non-nil, the result variable (resp) is typically nil or in an
// invalid state. Using it before the error check causes nil pointer dereference
// panics. The defer statement executes at function return regardless of error,
// so even after the error check returns, the deferred call still runs on nil.
//
// Competitor analysis:
//   - Claude Code: no automatic detection (relies on external linters)
//   - Cursor: no automatic detection (lint-on-save may catch via staticcheck)
//   - Cline/OpenHands: reactive only -- caught by tests or production incidents
//   - Aider: no automatic detection
//   - Windsurf: no automatic detection
//
// External tools like staticcheck (SA1019 nil check) and go vet can catch some
// of these, but require a separate lint cycle and are not always installed.
// This check provides immediate, zero-dependency feedback at write time using
// Go's standard library AST parser.
//
// Approach: AST-based analysis of Go function bodies. For each assignment that
// matches the pattern `result, err := f(...)` or `result, err = f(...)`, scan
// statements between the assignment and the first subsequent error check
// (if err != nil). If the result variable is used in those intervening
// statements (including defer), emit a warning. Delta-aware: only flags NEW
// instances introduced by this edit.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

// errorOrderInstance represents a detected result-used-before-error-check.
type errorOrderInstance struct {
	varName string // the result variable used before error check
	errName string // the error variable
	line    int    // line number of the assignment
}

// checkErrorOrder detects result variables used before their error is checked.
// Returns warning strings. Only flags NEW instances introduced by this edit
// (delta-aware).
func checkErrorOrder(filePath, oldContent, newContent string) []string {
	if filepath.Ext(filePath) != ".go" {
		return nil
	}
	if strings.TrimSpace(newContent) == "" {
		return nil
	}

	oldCount := countErrorOrderIssues(oldContent)

	newInstances := findErrorOrderIssues(newContent)
	if len(newInstances) <= oldCount {
		return nil
	}

	newCount := len(newInstances) - oldCount
	var warnings []string
	for i := 0; i < newCount && i+oldCount < len(newInstances); i++ {
		inst := newInstances[oldCount+i]
		warnings = append(warnings, fmt.Sprintf(
			"`%s` may be used before its error `%s` is checked (line %d). "+
				"If %s is non-nil, %s is typically nil/invalid and using it causes a panic. "+
				"Move the `if %s != nil` check immediately after the assignment, before any use of %s "+
				"(including defer statements, which run even after an error return).",
			inst.varName, inst.errName, inst.line,
			inst.errName, inst.varName,
			inst.errName, inst.varName))
	}

	return warnings
}

// countErrorOrderIssues returns the number of error-order issues in source.
func countErrorOrderIssues(src string) int {
	return len(findErrorOrderIssues(src))
}

// findErrorOrderIssues parses Go source and returns all result-used-before-
// error-check instances found, ordered by position.
func findErrorOrderIssues(src string) []errorOrderInstance {
	if strings.TrimSpace(src) == "" {
		return nil
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", src, 0)
	if err != nil || file == nil {
		return nil
	}

	var instances []errorOrderInstance

	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}

		instances = append(instances, findErrorOrderInBlock(fset, fn.Body)...)
	}

	return instances
}

// findErrorOrderInBlock analyzes a block statement for result-used-before-
// error-check patterns. It recursively descends into nested blocks.
func findErrorOrderInBlock(fset *token.FileSet, body *ast.BlockStmt) []errorOrderInstance {
	var instances []errorOrderInstance

	stmts := body.List
	for i := 0; i < len(stmts); i++ {
		// Check if this statement is an assignment with an error return.
		resultName, errName := extractAssignWithErr(stmts[i])
		if resultName == "" || errName == "" {
			// Still recurse into nested blocks.
			if nested := getNestedBlock(stmts[i]); nested != nil {
				instances = append(instances, findErrorOrderInBlock(fset, nested)...)
			}
			continue
		}

		// Check if the error is ever checked in the remaining statements
		// of this block. If not, we don't flag -- the error might be checked
		// in a caller or handled differently.
		errCheckedLater := false
		for j := i + 1; j < len(stmts); j++ {
			if isErrorCheckFor(stmts[j], errName) {
				errCheckedLater = true
				break
			}
		}
		if !errCheckedLater {
			if nested := getNestedBlock(stmts[i]); nested != nil {
				instances = append(instances, findErrorOrderInBlock(fset, nested)...)
			}
			continue
		}

		// Error IS checked later -- scan forward to see if result is used
		// before the check. If so, flag it.
		for j := i + 1; j < len(stmts); j++ {
			if isErrorCheckFor(stmts[j], errName) {
				break // error checked before any use -- good
			}

			// Check if resultName is used in this statement.
			if stmtUsesIdent(stmts[j], resultName) {
				pos := fset.Position(stmts[i].Pos())
				instances = append(instances, errorOrderInstance{
					varName: resultName,
					errName: errName,
					line:    pos.Line,
				})
				break
			}
		}

		// Recurse into nested blocks regardless.
		if nested := getNestedBlock(stmts[i]); nested != nil {
			instances = append(instances, findErrorOrderInBlock(fset, nested)...)
		}
	}

	return instances
}

// extractAssignWithErr checks if a statement is an assignment producing both
// a result and an error variable (the `result, err := f()` pattern).
// Returns (resultName, errName) or ("", "") if not applicable.
func extractAssignWithErr(stmt ast.Stmt) (string, string) {
	assign, ok := stmt.(*ast.AssignStmt)
	if !ok {
		return "", ""
	}

	// Only short declarations (:=) or multi-assign (=) with 2+ LHS.
	if len(assign.Lhs) < 2 {
		return "", ""
	}

	// RHS must be a function call (single call producing multiple values).
	if len(assign.Rhs) != 1 {
		return "", ""
	}
	if _, ok := assign.Rhs[0].(*ast.CallExpr); !ok {
		return "", ""
	}

	// Find the result (non-error) and error variables.
	// The error is typically the last LHS that looks like an error variable.
	var resultName, errName string
	for idx, lhs := range assign.Lhs {
		ident, ok := lhs.(*ast.Ident)
		if !ok {
			continue
		}
		if idx == len(assign.Lhs)-1 && looksLikeErrorVar(ident.Name) {
			errName = ident.Name
		} else if resultName == "" && !strings.HasPrefix(ident.Name, "_") {
			resultName = ident.Name
		}
	}

	return resultName, errName
}

// isErrorCheckFor returns true if stmt is `if <errName> != nil { ... }`.
func isErrorCheckFor(stmt ast.Stmt, errName string) bool {
	ifStmt, ok := stmt.(*ast.IfStmt)
	if !ok {
		return false
	}

	return extractErrorCheckName(ifStmt.Cond) == errName
}

// stmtUsesIdent returns true if the statement uses the given identifier name
// anywhere in its AST subtree.
func stmtUsesIdent(stmt ast.Stmt, name string) bool {
	found := false
	ast.Inspect(stmt, func(node ast.Node) bool {
		if found {
			return false
		}
		ident, ok := node.(*ast.Ident)
		if ok && ident.Name == name {
			found = true
			return false
		}
		return true
	})
	return found
}

// getNestedBlock extracts the primary nested block from control flow statements
// (if/for/range/switch/select). Returns nil if the statement has no nested block.
func getNestedBlock(stmt ast.Stmt) *ast.BlockStmt {
	switch s := stmt.(type) {
	case *ast.IfStmt:
		return s.Body
	case *ast.ForStmt:
		return s.Body
	case *ast.RangeStmt:
		return s.Body
	case *ast.SwitchStmt:
		return s.Body
	case *ast.SelectStmt:
		return s.Body
	case *ast.BlockStmt:
		return s
	default:
		return nil
	}
}
