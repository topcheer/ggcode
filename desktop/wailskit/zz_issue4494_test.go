package wailskit

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// #4494: SendHiddenText used to bump runGeneration a SECOND time (after the
// potentially slow InitAgent), re-claiming ownership after a racing Cancel
// had already finished the run against the OLD generation - double run_done,
// and the real run's persistRunMessages skipped (#1181 pattern violation).
//
// The fix mirrors SendContent's #1181 shape: claim ONCE at entry, and after
// InitAgent only refresh persistSession under `if b.activeRunGen == runGen`.
// This test pins that structure on the AST (same hermetic approach as the
// #1201 test in this package - SendHiddenText cannot be driven end-to-end
// without a live agent/session).
func TestIssue4494_SingleEntryBumpAndOwnershipGuardedPersist(t *testing.T) {
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, "chat.go", nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse chat.go: %v", err)
	}

	var fn *ast.FuncDecl
	for _, d := range node.Decls {
		if f, ok := d.(*ast.FuncDecl); ok && f.Name.Name == "SendHiddenText" {
			fn = f
			break
		}
	}
	if fn == nil {
		t.Fatal("SendHiddenText not found in chat.go")
	}

	bumps := 0
	bumpPos := token.NoPos
	initAgentPos := token.NoPos
	guardedPersist := false
	persistAssign := false

	ast.Inspect(fn, func(n ast.Node) bool {
		switch st := n.(type) {
		case *ast.IncDecStmt:
			if sel, ok := st.X.(*ast.SelectorExpr); ok {
				if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == "b" && sel.Sel.Name == "runGeneration" && st.Tok == token.INC {
					bumps++
					if bumpPos == token.NoPos {
						bumpPos = st.Pos()
					}
				}
			}
		case *ast.CallExpr:
			if sel, ok := st.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "InitAgent" && initAgentPos == token.NoPos {
				initAgentPos = st.Pos()
			}
		case *ast.IfStmt:
			// Guard of interest: b.activeRunGen == runGen
			if bin, ok := st.Cond.(*ast.BinaryExpr); ok && bin.Op == token.EQL {
				l, lok := bin.X.(*ast.SelectorExpr)
				r, rok := bin.Y.(*ast.Ident)
				if lok && rok && l.Sel.Name == "activeRunGen" && r.Name == "runGen" {
					// Its body must assign b.persistSession
					ast.Inspect(st.Body, func(m ast.Node) bool {
						if as, ok := m.(*ast.AssignStmt); ok {
							for _, lhs := range as.Lhs {
								if sel, ok := lhs.(*ast.SelectorExpr); ok {
									if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == "b" && sel.Sel.Name == "persistSession" {
										guardedPersist = true
									}
								}
							}
						}
						return true
					})
				}
			}
		case *ast.AssignStmt:
			for _, lhs := range st.Lhs {
				if sel, ok := lhs.(*ast.SelectorExpr); ok {
					if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == "b" && sel.Sel.Name == "persistSession" {
						persistAssign = true
					}
				}
			}
		}
		return true
	})

	if bumps != 1 {
		t.Fatalf("#4494: SendHiddenText must bump runGeneration exactly ONCE (at entry); found %d", bumps)
	}
	if initAgentPos != token.NoPos && bumpPos > initAgentPos {
		t.Fatal("#4494: the single runGeneration bump must be at entry, BEFORE the InitAgent call")
	}
	if !guardedPersist {
		t.Fatal("#4494: persistSession refresh must be guarded by `if b.activeRunGen == runGen` (ownership check)")
	}
	if !persistAssign {
		t.Fatal("#4494: persistSession must still be assigned somewhere in SendHiddenText (#594 persist binding must survive the fix)")
	}
}

// Control: SendContent (the sanctioned #1181 pattern) satisfies the same
// invariants - proves the test measures the right shape, not an accident.
func TestIssue4494_ControlSendContentSameShape(t *testing.T) {
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, "chat.go", nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse chat.go: %v", err)
	}
	var fn *ast.FuncDecl
	for _, d := range node.Decls {
		if f, ok := d.(*ast.FuncDecl); ok && f.Name.Name == "SendContent" {
			fn = f
			break
		}
	}
	if fn == nil {
		t.Fatal("SendContent not found")
	}
	bumps := 0
	ast.Inspect(fn, func(n ast.Node) bool {
		if st, ok := n.(*ast.IncDecStmt); ok {
			if sel, ok := st.X.(*ast.SelectorExpr); ok {
				if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == "b" && sel.Sel.Name == "runGeneration" && st.Tok == token.INC {
					bumps++
				}
			}
		}
		return true
	})
	if bumps != 1 {
		t.Fatalf("control: SendContent should also have exactly one bump, found %d", bumps)
	}
}
