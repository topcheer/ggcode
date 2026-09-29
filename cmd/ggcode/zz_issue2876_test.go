package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// zz_issue2876_test.go - probe for #2876: the daemon provider-switch hook
// must call lanchatHub.SetModel after a successful switch so LAN peers see
// the new model (and any stale quota-degraded status is cleared). TUI
// (internal/tui/model.go) and Desktop (desktop/wailskit/chat.go) already do
// this on their switch paths; the daemon hook was the only one missing it.
func TestIssue2876HookUpdatesLanchatModel(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "daemon.go", nil, 0)
	if err != nil {
		t.Fatalf("parse daemon.go: %v", err)
	}

	foundHook := false
	foundSetModel := false
	nilGuarded := false
	ast.Inspect(f, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncLit)
		if !ok {
			return true
		}
		// Heuristic (same as zz_issue2868_test.go): the provider-switch hook
		// is the 3-param FuncLit that calls agentruntime.ActivateCurrentSelection.
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

		// The hook must invoke lanchatHub.SetModel(...) on the fresh resolved
		// model. Accept any argument shape but require the call, and require
		// it to be guarded against a nil hub (A2A disabled path).
		ast.Inspect(fn.Body, func(inner ast.Node) bool {
			ifs, ok := inner.(*ast.IfStmt)
			if !ok {
				return true
			}
			// nil guard shape: `if lanchatHub != nil { ... }`
			bin, ok := ifs.Cond.(*ast.BinaryExpr)
			if !ok || bin.Op != token.NEQ {
				return true
			}
			id, ok := bin.X.(*ast.Ident)
			if !ok || id.Name != "lanchatHub" {
				return true
			}
			if lit, ok := bin.Y.(*ast.Ident); !ok || lit.Name != "nil" {
				return true
			}
			ast.Inspect(ifs.Body, func(inner2 ast.Node) bool {
				if call, ok := inner2.(*ast.CallExpr); ok {
					if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "SetModel" {
						if base, ok := sel.X.(*ast.Ident); ok && base.Name == "lanchatHub" {
							foundSetModel = true
							nilGuarded = true
						}
					}
				}
				return true
			})
			return true
		})
		return true
	})
	if !foundHook {
		t.Fatal("provider-switch hook closure not found in daemon.go")
	}
	if !foundSetModel {
		t.Fatal("#2876: provider-switch hook does not call lanchatHub.SetModel after a switch - LAN peers will keep seeing the startup model forever")
	}
	if !nilGuarded {
		t.Fatal("#2876: lanchatHub.SetModel call must be nil-guarded (hub stays nil when A2A is disabled)")
	}
}
