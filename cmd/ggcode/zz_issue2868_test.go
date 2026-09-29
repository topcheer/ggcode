package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// zz_issue2868_test.go - probe for #2868: the provider-switch hook closure
// must not shadow the outer `resolved` (its := left the daemon's tunnel
// SessionInfo/BrokerSnapshot reporting the startup model forever); it must
// reassign the outer variable under resolvedMu, and both tunnel snapshot
// paths must read via currentResolved().
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
		// Heuristic: the provider-switch hook is the FuncLit whose params
		// are exactly (vendor, endpoint, model string) - note Go groups
		// them into ONE ast.Field with three Names - and that assigns from
		// agentruntime.ActivateCurrentSelection. The vision-turn hook (1
		// param) also calls Activate with an intentional turn-scoped shadow
		// - it must NOT write the session identity, so it is out of scope.
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
		// (a) no `resolved` declared inside the closure (shadow shape gone)
		for _, st := range fn.Body.List {
			if assign, ok := st.(*ast.AssignStmt); ok && assign.Tok == token.DEFINE {
				for _, lhs := range assign.Lhs {
					if id, ok := lhs.(*ast.Ident); ok && id.Name == "resolved" {
						t.Errorf("#2868 hook still declares shadowed `resolved` at %s", fset.Position(id.Pos()))
					}
				}
			}
		}
		// (b) plain assignment `resolved = <ident>` present (outer reassign)
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
