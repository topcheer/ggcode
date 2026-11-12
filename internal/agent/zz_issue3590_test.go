package agent

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"testing"
)

func TestIssue3590_RecordSearchResultOutsideReadPathsBlock(t *testing.T) {
	// #5543: the tool-call idx loop moved out of agent.go; parse the
	// concatenated stream-loop source (shape-agnostic file targeting).
	// Parsing the concatenation keeps AST probes working across the split
	// as long as every file is package agent with no import block clash:
	// parser.ParseFile on each file separately and merge the findings.
	srcA, err := os.ReadFile("agent.go")
	if err != nil {
		t.Skipf("agent.go not readable from test dir: %v", err)
	}
	srcB, err := os.ReadFile("run_tool_call_idx.go")
	if err != nil {
		t.Skipf("run_tool_call_idx.go not readable from test dir: %v", err)
	}
	fset := token.NewFileSet()
	fileA, errA := parser.ParseFile(fset, "agent.go", srcA, 0)
	fileB, errB := parser.ParseFile(fset, "run_tool_call_idx.go", srcB, 0)
	if errA != nil || errB != nil {
		t.Skipf("stream-loop sources not parseable from test dir: %v / %v", errA, errB)
	}
	// recordSearchResult call must sit outside any extractFilePathsFromArgs
	// if-block, in either file.
	check := func(file *ast.File) bool {
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
			return true // no anchor in this file; keep looking
		}
		found := 0
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "recordSearchResult" {
				found++
				for _, ifs := range readPathIfs {
					if ifs.Pos() <= call.Pos() && call.Pos() < ifs.End() {
						t.Fatalf("recordSearchResult at %s sits inside the extractFilePathsFromArgs block (#3590 regression)", fset.Position(call.Pos()))
					}
				}
			}
			return true
		})
		if found > 0 {
			return false // anchor satisfied in this file
		}
		return true
	}
	done := false
	for _, f := range []*ast.File{fileA, fileB} {
		if !check(f) {
			done = true
			break
		}
	}
	if !done {
		t.Fatal("no extractFilePathsFromArgs if-statement found - probe anchor drifted")
	}
}
