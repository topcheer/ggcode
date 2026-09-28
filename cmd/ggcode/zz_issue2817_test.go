package main_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// zz_issue2817_test.go - probe for #2817: the ExecRestart failure branch in
// runDaemon must return a non-nil error so the process exits non-zero and
// supervisors (systemd Restart=on-failure etc.) re-launch the daemon.
// Uses AST inspection instead of raw source matching to avoid comment
// false positives.
func TestIssue2817ExecRestartFailureReturnsError(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "daemon.go", nil, 0)
	if err != nil {
		t.Fatalf("parse daemon.go: %v", err)
	}

	found := false
	ast.Inspect(f, func(n ast.Node) bool {
		ifStmt, ok := n.(*ast.IfStmt)
		if !ok {
			return true
		}
		// Locate the if-block whose init assigns from restart.ExecRestart.
		hasExecRestart := false
		if init, ok := ifStmt.Init.(*ast.AssignStmt); ok {
			for _, rhs := range init.Rhs {
				if call, ok := rhs.(*ast.CallExpr); ok {
					if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "ExecRestart" {
						hasExecRestart = true
					}
				}
			}
		}
		if !hasExecRestart {
			return true
		}
		found = true
		// The branch body must contain a return statement with a non-nil
		// expression (an error), not a bare `return` or `return nil`.
		for _, stmt := range ifStmt.Body.List {
			ret, ok := stmt.(*ast.ReturnStmt)
			if !ok || len(ret.Results) == 0 {
				continue
			}
			if id, ok := ret.Results[0].(*ast.Ident); ok && id.Name == "nil" {
				continue
			}
			return false // error-return present - branch is correct
		}
		t.Errorf("ExecRestart failure branch at %s must return an error (non-nil) so the daemon exits non-zero", fset.Position(ifStmt.Pos()))
		return false
	})
	if !found {
		t.Fatal("ExecRestart failure branch not found in daemon.go - probe is stale")
	}
}
