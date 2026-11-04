package codehealth

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// #5037: the if-branch early-return made the else traversal unreachable
// (maxNestingDepth always returns >= current), so else-side nesting was
// silently undercounted. Else-if chains and plain else blocks must both
// count their depth.
func issue5037Depth(t *testing.T, src string) int {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "x.go", "package p\n"+src, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range f.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok && fn.Body != nil {
			return maxNestingDepth(fn.Body, 0)
		}
	}
	t.Fatal("no func decl")
	return 0
}

func TestIssue5037ElseBlockNestingCounts(t *testing.T) {
	// The deepest nesting lives on the ELSE side: if(1) > else(1) > for(2) > if(3).
	src := `func f(x int) {
	if x > 0 {
		a()
	} else {
		for i := 0; i < x; i++ {
			if i > 2 {
				b()
			}
		}
	}
}
`
	if got := issue5037Depth(t, src); got != 3 {
		t.Fatalf("else-side nesting must count: got depth %d want 3", got)
	}
}

func TestIssue5037ElseIfChainCounts(t *testing.T) {
	// else-if chain: each chain link counts as one branch-nesting level
	// (mainstream linter convention; plain else blocks stay sibling-level).
	// outer if=1, else-if link=2, its for=3, its if=4.
	src := `func f(x int) {
	if x == 1 {
		a()
	} else if x == 2 {
		for i := 0; i < x; i++ {
			if i > 2 {
				b()
			}
		}
	}
}
`
	if got := issue5037Depth(t, src); got != 4 {
		t.Fatalf("else-if chain nesting must count: got depth %d want 4", got)
	}
}

func TestIssue5037BodySideStillCounts(t *testing.T) {
	// Regression guard: the pre-existing correct path (deep on the if side).
	src := `func f(x int) {
	if x > 0 {
		for i := 0; i < x; i++ {
			if i > 2 {
				b()
			}
		}
	}
}
`
	if got := issue5037Depth(t, src); got != 3 {
		t.Fatalf("if-side nesting unchanged: got depth %d want 3", got)
	}
}
