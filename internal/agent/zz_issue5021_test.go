package agent

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// #5021: a call to the builtin panic is a terminating statement per the Go
// spec, and the warning text already says so ("after return/break/continue/
// panic"), but stmtIsTerminating had no ExprStmt branch - redundant else
// after panic(err) was silently missed and the miss propagated through
// if-chains.

func mustParseExprStmt(t *testing.T, src string) ast.Stmt {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "x.go", "package p\nvar _ = func() { "+src+" }", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok || len(vs.Values) == 0 {
				continue
			}
			if fl, ok := vs.Values[0].(*ast.FuncLit); ok {
				return fl.Body.List[0]
			}
		}
	}
	t.Fatal("no statement parsed")
	return nil
}

func TestIssue5021PanicIsTerminating(t *testing.T) {
	if !stmtIsTerminating(mustParseExprStmt(t, "panic(err)")) {
		t.Fatal("panic(err) must be a terminating statement (#5021)")
	}
	if stmtIsTerminating(mustParseExprStmt(t, "fmt.Println(err)")) {
		t.Fatal("ordinary call must NOT be terminating")
	}
	if stmtIsTerminating(mustParseExprStmt(t, "x.panic()")) {
		t.Fatal("selector method named panic must NOT count (not the builtin)")
	}
}

func TestIssue5021RedundantElseAfterPanicDetected(t *testing.T) {
	src := `package p

func f(err error) {
	if err != nil {
		panic(err)
	} else {
		fallback()
	}
}
`
	warnings := checkExitPath("/tmp/foo5021.go", "", src)
	if len(warnings) == 0 {
		t.Fatal("redundant else after panic must be detected (#5021 false negative)")
	}
}

func TestIssue5021PanicTerminatesElseChain(t *testing.T) {
	src := `package p

func f(err error, other func(error) bool) {
	if err != nil {
		panic(err)
	} else if other(err) {
		panic(err)
	} else {
		fallback()
	}
}
`
	warnings := checkExitPath("/tmp/foo5021b.go", "", src)
	if len(warnings) == 0 {
		t.Fatal("terminating panic must propagate through if-chains")
	}
}
