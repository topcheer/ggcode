package agent

// Regression tests for issue #1171: the excessive-parameter-count detector
// must flag function literals wherever they appear - var declarations,
// call arguments, go and defer statements - not only direct RHS elements
// of `:=` assignments.

import (
	"strings"
	"testing"
)

const issue1171Src = `package sample

type S struct{}

func (s *S) Reg(f func() error) error { return f() }

// var declaration form
var f1171 = func(a, b, c, d, e, g int) {}

// call-argument form
func Reg1171(s *S) {
	s.Reg(func() error { return nil })
	_ = s
}

func Args1171(s *S) {
	s.Reg(func() error { return nil })
}

// go statement form
func Go1171() {
	go func(a, b, c, d, e, g int) {}()
}

// defer statement form
func Defer1171() {
	defer func(a, b, c, d, e, g int) {}()
}

// := assignment form must still be detected (pre-existing coverage)
func Assign1171() {
	h := func(a, b, c, d, e, g int) {}
	_ = h
}
`

// All six-param literal shapes are detected exactly once each.
func TestIssue1171_FuncLiteralFormsDetected(t *testing.T) {
	insts := findExcessiveParams(issue1171Src, false)
	var forms []string
	for _, inst := range insts {
		forms = append(forms, strings.Join(inst.params, ","))
	}
	want := 4 // var f1171, go, defer, := assignment
	if len(insts) != want {
		t.Fatalf("got %d instances, want %d:\n%s", len(insts), want, strings.Join(forms, "\n"))
	}
	for _, inst := range insts {
		if inst.funcName != "<anonymous>" || inst.count != 6 {
			t.Fatalf("unexpected instance: %+v", inst)
		}
	}
}

// A short (5-param) literal in any form must not be flagged.
func TestIssue1171_UnderThresholdLiteralsNotFlagged(t *testing.T) {
	src := `package sample

var f = func(a, b, c, d, e int) {}

func Go() {
	go func(a, b, c, d, e int) {}()
	defer func(a, b, c, d, e int) {}()
}
`
	if got := countExcessiveParams(src, false); got != 0 {
		t.Fatalf("got %d instances, want 0", got)
	}
}

// Nested function literals are each counted exactly once (no double
// counting after the AssignStmt branch was removed, issue #1171).
func TestIssue1171_NestedLiteralsCountedOnce(t *testing.T) {
	src := `package sample

func Outer() {
	h := func(a, b, c, d, e, g int) {
		inner := func(p, q, r, s2, u, v2 int) {}
		_ = inner
	}
	_ = h
}
`
	if got := countExcessiveParams(src, false); got != 2 {
		t.Fatalf("got %d instances, want 2", got)
	}
}
