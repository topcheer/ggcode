package agent

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// #1677 case 1a: composite-condition guards (LANDAND/LOR) must enter the
// guard table with correct polarity. Includes the selector dual-condition
// shape the incremental review called out (`p != nil && p.Field != nil`).
func rnpParseFunc(t *testing.T, src string) *ast.FuncDecl {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "x.go", "package p\n"+src, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range f.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok {
			return fd
		}
	}
	t.Fatal("no func")
	return nil
}

func TestIssue1677CompositeAndGuardRecognized(t *testing.T) {
	fd := rnpParseFunc(t, `func f(x *T) {
	if x != nil && x.F != "" {
		for range *x {}
	}
}`)
	g := rnpCollectNilGuards(fd.Body)
	if len(g["x"]) == 0 {
		t.Fatal("composite `x != nil && ...` guard not collected (#1677-1a)")
	}
	gd := g["x"][0]
	if !gd.negated {
		t.Fatal("AND-context guard must record negated=true (then proves non-nil)")
	}
}

func TestIssue1677CompositeAndEqNilNoGuard(t *testing.T) {
	// `x == nil && y`: the then path does NOT prove x non-nil - must not
	// be recorded as a then-guard.
	fd := rnpParseFunc(t, `func f(x *T, y bool) {
	if x == nil && y {
		for range *x {}
	}
}`)
	g := rnpCollectNilGuards(fd.Body)
	for _, gd := range g["x"] {
		if gd.negated {
			t.Fatalf("wrong polarity recorded: %+v", gd)
		}
	}
}

func TestIssue1677CompositeOrEarlyReturn(t *testing.T) {
	// `if x == nil || y { return }` - surviving code has x non-nil: the
	// EQL arm under OR must be collected (negated=false, terminating).
	fd := rnpParseFunc(t, `func f(x *T, y bool) {
	if x == nil || y {
		return
	}
	for range *x {}
}`)
	g := rnpCollectNilGuards(fd.Body)
	guards := g["x"]
	if len(guards) == 0 {
		t.Fatal("OR-context `x == nil` arm not collected (#1677-1a)")
	}
	if guards[0].negated || !guards[0].terminates {
		t.Fatalf("OR EQL arm must be negated=false terminating: %+v", guards[0])
	}
}

func TestIssue1677CompositeOrNeqNilNoGuard(t *testing.T) {
	// `x != nil || y`: the then path can have x nil (y carried it) - the
	// NEQ arm under OR proves nothing and must not be collected.
	fd := rnpParseFunc(t, `func f(x *T, y bool) {
	if x != nil || y {
		for range *x {}
	}
}`)
	g := rnpCollectNilGuards(fd.Body)
	for _, gd := range g["x"] {
		if gd.negated {
			t.Fatalf("OR-context NEQ arm must not be a then-guard: %+v", gd)
		}
	}
}

func TestIssue1677SelectorDualCondition(t *testing.T) {
	// The exact shape from the #1483 incremental review: the most
	// idiomatic selector guard with a second condition must be guarded.
	fd := rnpParseFunc(t, `func f(p *P) {
	if p != nil && p.Field != nil {
		for range *p.Field {}
	}
}`)
	g := rnpCollectNilGuards(fd.Body)
	if len(g["p"]) == 0 {
		t.Fatal("selector dual-condition guard not collected (#1483 shape)")
	}
}

func TestIssue1677MixedSubtreeYieldsNothing(t *testing.T) {
	// `(a != nil || b) && c != nil`: the OR subtree proves nothing about
	// a; only c qualifies under AND. a must NOT be recorded.
	fd := rnpParseFunc(t, `func f(a, b, c *T) {
	if (a != nil || b != nil) && c != nil {
		for range *a {}
	}
}`)
	g := rnpCollectNilGuards(fd.Body)
	for _, gd := range g["a"] {
		if gd.negated {
			t.Fatalf("a inside OR subtree under AND must not be a then-guard: %+v", gd)
		}
	}
	if len(g["c"]) == 0 {
		t.Fatal("c != nil leaf under AND must be collected")
	}
}

// sa-174 follow-up: the first-hit-only implementation left the second
// arm's variable unguarded - the plain-first #1483 ordering
// (`p != nil && p.Field != nil { for range *p.Field }`) still misfired
// end-to-end even though g["p"] was populated. These pins go through
// checkRangeNilPtr itself.
func TestIssue1677AllLeavesCollectedEndToEnd(t *testing.T) {
	cases := []struct{ name, src string }{
		{"selector second arm", `package main

type P struct{ Field []int }

func f(p *P) {
	if p != nil && p.Field != nil {
		for range *p.Field {
		}
	}
}`},
		{"two idents second arm", `package main

type T struct{}

func f(a, b *T) {
	if a != nil && b != nil {
		for range *b {
		}
	}
}`},
		{"or early-return second arm", `package main

type T struct{}

func f(a, b *T) {
	if a == nil || b == nil {
		return
	}
	for range *b {
	}
}`},
	}
	for _, c := range cases {
		if w := checkRangeNilPtr("test.go", "", c.src); w != "" {
			t.Fatalf("%s: guarded range still warned: %s", c.name, w)
		}
	}
}

// sa-179 (#2220 follow-up): parenthesized same-operator chains and a
// parenthesized whole condition both dropped guards entirely.
func TestIssue1677ParenthesizedChainAllLeaves(t *testing.T) {
	cases := []struct{ name, src string }{
		{"parenthesized nested AND", `package main

type P struct{ A, B []int }

func f(p *P) {
	if p != nil && (p.A != nil && p.B != nil) {
		for range *p.A {
		}
	}
}`},
		{"parenthesized single guard", `package main

type T struct{}

func f(p *T) {
	if (p != nil) {
		for range *p {
		}
	}
}`},
		{"parenthesized nested OR early-return", `package main

type T struct{}

func f(a, b *T) {
	if (a == nil || b == nil) {
		return
	}
	for range *b {
	}
}`},
	}
	for _, c := range cases {
		if w := checkRangeNilPtr("test.go", "", c.src); w != "" {
			t.Fatalf("%s: guarded range still warned: %s", c.name, w)
		}
	}
}

// The empty-string filter must keep garbage leaves out of the guard table.
func TestIssue1677NoEmptyGuardRecords(t *testing.T) {
	fd := rnpParseFunc(t, `func f(a, b *T) {
	if a != nil && b == nil {
		for range *a {
		}
	}
}`)
	g := rnpCollectNilGuards(fd.Body)
	if _, ok := g[""]; ok {
		t.Fatal("empty-name guard record leaked into the table")
	}
	// mixed-polarity AND: only the a leaf qualifies
	if _, ok := g["b"]; ok {
		t.Fatal("b == nil leaf under AND must not be a then-guard")
	}
	if len(g["a"]) == 0 {
		t.Fatal("a != nil leaf must be collected")
	}
}
