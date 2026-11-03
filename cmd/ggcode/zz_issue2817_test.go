package main_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

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

		for _, stmt := range ifStmt.Body.List {
			ret, ok := stmt.(*ast.ReturnStmt)
			if !ok || len(ret.Results) == 0 {
				continue
			}
			if id, ok := ret.Results[0].(*ast.Ident); ok && id.Name == "nil" {
				continue
			}
			return false
		}
		t.Errorf("ExecRestart failure branch at %s must return an error (non-nil) so the daemon exits non-zero", fset.Position(ifStmt.Pos()))
		return false
	})
	if !found {
		t.Fatal("ExecRestart failure branch not found in daemon.go - probe is stale")
	}
}
