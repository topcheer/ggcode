package tui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// zz_issue2844_test.go - probe for #2844: the onResult closure passed to
// provider.ProbeContextWindow runs on a background goroutine; it must not
// touch shared Model state (m.session, m.currentProbeKey, m.sessionStore,
// m.agent) directly - it may only forward via m.tuiSend. Application logic
// lives in applyProbeResult, invoked from the UI goroutine via the
// contextProbeResultMsg registration.
func TestIssue2844ProbeCallbackIsMessageOnly(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "model.go", nil, 0)
	if err != nil {
		t.Fatalf("parse model.go: %v", err)
	}

	foundClosure := false
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); !ok || sel.Sel.Name != "ProbeContextWindow" {
			return true
		}
		// The last argument is the callback closure.
		cl, ok := call.Args[len(call.Args)-1].(*ast.FuncLit)
		if !ok {
			return true
		}
		foundClosure = true
		banned := map[string]bool{
			"session": true, "sessionStore": true, "currentProbeKey": true, "agent": true, "config": true,
		}
		ast.Inspect(cl.Body, func(inner ast.Node) bool {
			sel, ok := inner.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == "m" && banned[sel.Sel.Name] {
				t.Errorf("#2844 probe callback still touches shared state m.%s at %s (background goroutine)", sel.Sel.Name, fset.Position(sel.Pos()))
			}
			return true
		})
		return true
	})
	if !foundClosure {
		t.Fatal("ProbeContextWindow call not found - probe is stale")
	}

	// applyProbeResult must exist and be referenced from the dispatch table.
	foundApply := false
	ast.Inspect(f, func(n ast.Node) bool {
		if fn, ok := n.(*ast.FuncDecl); ok && fn.Name.Name == "applyProbeResult" {
			foundApply = true
		}
		return true
	})
	if !foundApply {
		t.Error("#2844 applyProbeResult method missing from model.go")
	}
}

func TestIssue2844ProbeResultMsgRegistered(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "model_update_dispatch.go", nil, 0)
	if err != nil {
		t.Fatalf("parse model_update_dispatch.go: %v", err)
	}
	found := false
	ast.Inspect(f, func(n ast.Node) bool {
		ft, ok := n.(*ast.FuncType)
		if !ok || ft.Params == nil || len(ft.Params.List) != 2 {
			return true
		}
		if arr, ok := ft.Params.List[1].Type.(*ast.ArrayType); ok {
			if id, ok := arr.Elt.(*ast.Ident); ok && id.Name == "contextProbeResultMsg" {
				found = true
			}
		}
		if id, ok := ft.Params.List[1].Type.(*ast.Ident); ok && id.Name == "contextProbeResultMsg" {
			found = true
		}
		return true
	})
	if !found {
		t.Error("#2844 no dispatch handler registered for contextProbeResultMsg")
	}
}
