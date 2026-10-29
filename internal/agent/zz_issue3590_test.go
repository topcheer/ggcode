package agent

// #3590 probe: the recordSearchResult call must sit OUTSIDE the
// extractFilePathsFromArgs readPaths block. The pre-fix call was inside,
// so a repo-wide grep with no explicit path argument (the most common
// form) never reached the detector - the call's own #1491-A comment
// claimed the opposite of what the code did. This pins the placement
// structurally against agent.go itself.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

func TestIssue3590_RecordSearchResultOutsideReadPathsBlock(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "agent.go", nil, 0)
	if err != nil {
		t.Skipf("agent.go not parseable from test dir: %v", err)
	}

	// Collect every IfStmt whose OWN Init/Cond (not its else-chain subtree -
	// scanning the subtree would drag the whole else-if chain head in and
	// flag legitimate later-branch calls) references extractFilePathsFromArgs.
	var readPathIfs []*ast.IfStmt
	ast.Inspect(file, func(n ast.Node) bool {
		if ifs, ok := n.(*ast.IfStmt); ok {
			refs := false
			for _, part := range []ast.Node{ifs.Init, ifs.Cond} {
				if part == nil {
					continue
				}
				ast.Inspect(part, func(m ast.Node) bool {
					if id, ok := m.(*ast.Ident); ok && id.Name == "extractFilePathsFromArgs" {
						refs = true
					}
					return !refs
				})
			}
			if refs {
				readPathIfs = append(readPathIfs, ifs)
			}
		}
		return true
	})
	if len(readPathIfs) == 0 {
		t.Fatal("no extractFilePathsFromArgs if-statement found - probe anchor drifted")
	}

	found := 0
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "recordSearchResult" {
			return true
		}
		found++
		for _, ifs := range readPathIfs {
			if ifs.Body.Pos() <= call.Pos() && call.End() <= ifs.Body.End() {
				t.Fatalf("recordSearchResult at %s is INSIDE the readPaths block - pathless searches starve the detector (#3590)",
					fset.Position(call.Pos()))
			}
		}
		return true
	})
	if found == 0 {
		t.Fatal("recordSearchResult call not found - wiring lost")
	}
}
