package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

func TestIssue2868NoShadowAndLockedReassign(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "daemon.go", nil, 0)
	if err != nil {
		t.Fatalf("parse daemon.go: %v", err)
	}

	foundHook := false
	foundReassign := false
	ast.Inspect(f, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncLit)
		if !ok {
			return true
		}

		paramNames := 0
		if fn.Type.Params != nil {
			for _, p := range fn.Type.Params.List {
				paramNames += len(p.Names)
			}
		}
		if paramNames != 3 {
			return true
		}
		callsActivate := false
		ast.Inspect(fn.Body, func(inner ast.Node) bool {
			if call, ok := inner.(*ast.CallExpr); ok {
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "ActivateCurrentSelection" {
					callsActivate = true
				}
			}
			return true
		})
		if !callsActivate {
			return true
		}
		foundHook = true

		for _, st := range fn.Body.List {
			if assign, ok := st.(*ast.AssignStmt); ok && assign.Tok == token.DEFINE {
				for _, lhs := range assign.Lhs {
					if id, ok := lhs.(*ast.Ident); ok && id.Name == "resolved" {
						t.Errorf("#2868 hook still declares shadowed `resolved` at %s", fset.Position(id.Pos()))
					}
				}
			}
		}

		ast.Inspect(fn.Body, func(inner ast.Node) bool {
			if assign, ok := inner.(*ast.AssignStmt); ok && assign.Tok == token.ASSIGN && len(assign.Lhs) == 1 {
				if id, ok := assign.Lhs[0].(*ast.Ident); ok && id.Name == "resolved" {
					foundReassign = true
				}
			}
			return true
		})
		return true
	})
	if !foundHook {
		t.Fatal("provider-switch hook closure not found - probe stale")
	}
	if !foundReassign {
		t.Error("#2868 hook never reassigns the outer `resolved` - tunnel paths stay stale")
	}
}

func TestIssue2868TunnelSnapshotsUseCurrentResolved(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "daemon.go", nil, 0)
	if err != nil {
		t.Fatalf("parse daemon.go: %v", err)
	}
	snapshotClosures := 0
	ast.Inspect(f, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncLit)
		if !ok {
			return true
		}
		usesDaemonSnapshot := false
		usesCurrentResolved := false
		usesBareResolved := false
		ast.Inspect(fn, func(inner ast.Node) bool {
			call, ok := inner.(*ast.CallExpr)
			if !ok {
				return true
			}
			if sel, ok := call.Fun.(*ast.Ident); ok && sel.Name == "daemonSnapshot" {
				usesDaemonSnapshot = true
				for _, arg := range call.Args {
					switch a := arg.(type) {
					case *ast.Ident:
						if a.Name == "resolved" {
							usesBareResolved = true
						}
					case *ast.CallExpr:
						if id, ok := a.Fun.(*ast.Ident); ok && id.Name == "currentResolved" {
							usesCurrentResolved = true
						}
					}
				}
			}
			return true
		})
		if usesDaemonSnapshot {
			snapshotClosures++
			if usesBareResolved {
				t.Errorf("#2868 snapshot closure at %s still captures the raw outer `resolved`", fset.Position(fn.Pos()))
			}
			if !usesCurrentResolved {
				t.Errorf("#2868 snapshot closure at %s does not read via currentResolved()", fset.Position(fn.Pos()))
			}
		}
		return true
	})
	if snapshotClosures < 2 {
		t.Errorf("#2868 expected both tunnel snapshot closures, found %d", snapshotClosures)
	}
}
