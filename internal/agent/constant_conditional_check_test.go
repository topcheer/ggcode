package agent

import (
	"strconv"
	"strings"
	"testing"
)

func TestCheckConstantConditional_NonGo(t *testing.T) {
	w := checkConstantConditional("foo.py", "", "if true: pass\n")
	if len(w) != 0 {
		t.Fatalf("expected no warnings for non-Go file, got %v", w)
	}
}

func TestCheckConstantConditional_Empty(t *testing.T) {
	w := checkConstantConditional("x.go", "", "")
	if len(w) != 0 {
		t.Fatalf("expected no warnings for empty content, got %v", w)
	}
}

func TestCheckConstantConditional_NoConstant(t *testing.T) {
	src := `package x
func f(b bool) int {
	if b {
		return 1
	}
	if b && !b {
		return 2
	}
	return 0
}
`
	w := checkConstantConditional("x.go", "", src)
	if len(w) != 0 {
		t.Fatalf("expected no warnings for variable conditions, got %v", w)
	}
}

func TestCheckConstantConditional_IfTrue(t *testing.T) {
	src := `package x
func f() int {
	if true {
		return 1
	}
	return 0
}
`
	w := checkConstantConditional("x.go", "", src)
	if len(w) != 1 {
		t.Fatalf("expected 1 warning for if true, got %d: %v", len(w), w)
	}
	if !strings.Contains(w[0], "always-true") {
		t.Fatalf("expected always-true message, got %s", w[0])
	}
}

func TestCheckConstantConditional_IfFalse(t *testing.T) {
	src := `package x
func f() int {
	if false {
		return 1
	}
	return 0
}
`
	w := checkConstantConditional("x.go", "", src)
	if len(w) != 1 {
		t.Fatalf("expected 1 warning for if false, got %d: %v", len(w), w)
	}
	if !strings.Contains(w[0], "always-false") {
		t.Fatalf("expected always-false message, got %s", w[0])
	}
}

func TestCheckConstantConditional_Negation(t *testing.T) {
	src := `package x
func f() int {
	if !true {
		return 1
	}
	if !false {
		return 2
	}
	return 0
}
`
	w := checkConstantConditional("x.go", "", src)
	if len(w) != 2 {
		t.Fatalf("expected 2 warnings for !true and !false, got %d: %v", len(w), w)
	}
}

func TestCheckConstantConditional_LogicalAnd(t *testing.T) {
	src := `package x
func f() int {
	if true && false {
		return 1
	}
	if true || false {
		return 2
	}
	return 0
}
`
	w := checkConstantConditional("x.go", "", src)
	if len(w) != 2 {
		t.Fatalf("expected 2 warnings for logical constants, got %d: %v", len(w), w)
	}
}

func TestCheckConstantConditional_NumericComparison(t *testing.T) {
	src := `package x
func f() int {
	if 1 == 1 {
		return 1
	}
	if 2 > 3 {
		return 2
	}
	if 5 <= 5 {
		return 3
	}
	return 0
}
`
	w := checkConstantConditional("x.go", "", src)
	if len(w) != 3 {
		t.Fatalf("expected 3 warnings for numeric comparisons, got %d: %v", len(w), w)
	}
}

func TestCheckConstantConditional_BoolComparison(t *testing.T) {
	src := `package x
func f() int {
	if true == false {
		return 1
	}
	if true != false {
		return 2
	}
	return 0
}
`
	w := checkConstantConditional("x.go", "", src)
	if len(w) != 2 {
		t.Fatalf("expected 2 warnings for bool comparisons, got %d: %v", len(w), w)
	}
}

func TestCheckConstantConditional_ParensAndUnary(t *testing.T) {
	src := `package x
func f() int {
	if (-1) > 2 {
		return 1
	}
	if (true) {
		return 2
	}
	return 0
}
`
	w := checkConstantConditional("x.go", "", src)
	if len(w) != 2 {
		t.Fatalf("expected 2 warnings for paren/unary constants, got %d: %v", len(w), w)
	}
}

func TestCheckConstantConditional_NoDescendIntoDeadBody(t *testing.T) {
	// The inner if false is inside an always-true outer if; we should not
	// descend into the dead body, so only 1 warning (the outer).
	src := `package x
func f() int {
	if true {
		if false {
			return 99
		}
		return 1
	}
	return 0
}
`
	w := checkConstantConditional("x.go", "", src)
	if len(w) != 1 {
		t.Fatalf("expected 1 warning (no descent into dead body), got %d: %v", len(w), w)
	}
}

func TestCheckConstantConditional_SyntaxError(t *testing.T) {
	src := `package x
func f() {
	if true {
`
	w := checkConstantConditional("x.go", "", src)
	if len(w) != 0 {
		t.Fatalf("expected no warnings for unparseable code, got %v", w)
	}
}

func TestCheckConstantConditional_Truncation(t *testing.T) {
	var stmts string
	for i := 0; i < 8; i++ {
		stmts += "if true {\n    _ = " + strconv.Itoa(i) + "\n}\n"
	}
	src := "package x\nfunc f() {\n" + stmts + "}\n"
	w := checkConstantConditional("x.go", "", src)
	if len(w) != maxConstantCondWarnings+1 {
		t.Fatalf("expected %d entries (capped + truncation), got %d", maxConstantCondWarnings+1, len(w))
	}
	if !strings.Contains(w[len(w)-1], "more constant-conditional") {
		t.Fatalf("expected truncation message, got %s", w[len(w)-1])
	}
}

func TestCheckConstantConditional_VariableNumeric(t *testing.T) {
	// Variables are not constants; should not warn.
	src := `package x
func f(n int) bool {
	if n > 0 {
		return true
	}
	return false
}
`
	w := checkConstantConditional("x.go", "", src)
	if len(w) != 0 {
		t.Fatalf("expected no warnings for variable comparison, got %v", w)
	}
}
