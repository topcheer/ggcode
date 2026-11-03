package agent

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

func TestIssue3799_IndentedNumberedVerdictRecoversNum(t *testing.T) {
	for _, tc := range []struct {
		line string
		want int
	}{
		{"  1. [not done] evidence tail", 1},
		{"1. [not done] x", 1},
		{"[not done] x", 0},
	} {
		m := verdictLineRe.FindStringSubmatch(tc.line)
		if len(m) == 0 {
			t.Fatalf("verdict line should match: %q", tc.line)
		}
		if got := numberedVerdictPrefix("", m[0]); got != tc.want {
			t.Fatalf("%q: got %d, want %d", tc.line, got, tc.want)
		}
	}
}

func vrmParseExpr(t *testing.T, src string) ast.Expr {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "x.go", "package p\nvar _ = "+src, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range f.Decls {
		if gd, ok := d.(*ast.GenDecl); ok && gd.Specs != nil {
			if vs, ok := gd.Specs[0].(*ast.ValueSpec); ok && vs.Values != nil {
				return vs.Values[0]
			}
		}
	}
	t.Fatal("no expr parsed")
	return nil
}

func TestIssue3799_NestedSelectorExempt(t *testing.T) {

	if got := vrmExtractRecvField(vrmParseExpr(t, "c.state.ready"), "c"); got != "" {
		t.Fatalf("nested selector chain must be exempt, got %q", got)
	}

	if got := vrmExtractRecvField(vrmParseExpr(t, "c.top"), "c"); got != "top" {
		t.Fatalf("direct selector must still extract 'top', got %q", got)
	}
}
