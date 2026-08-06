package agent

import (
	"go/ast"
	"go/token"
	"strings"
	"testing"
)

func TestCheckRangeNilPtr_BasicDetection(t *testing.T) {
	src := `package main

type Item struct{ Name string }

func process(items *[]Item) {
	for _, item := range *items {
		_ = item
	}
}
`
	w := checkRangeNilPtr("test.go", "", src)
	if w == "" {
		t.Fatal("expected warning for range over nil pointer")
	}
	if !strings.Contains(w, "items") {
		t.Errorf("warning should mention variable name 'items', got: %s", w)
	}
	if !strings.Contains(w, "nil") {
		t.Errorf("warning should mention nil, got: %s", w)
	}
}

func TestCheckRangeNilPtr_NoPointer(t *testing.T) {
	src := `package main

func process(items []int) {
	for _, item := range items {
		_ = item
	}
}
`
	w := checkRangeNilPtr("test.go", "", src)
	if w != "" {
		t.Errorf("expected no warning for non-pointer range, got: %s", w)
	}
}

func TestCheckRangeNilPtr_NonVariableDeref(t *testing.T) {
	src := `package main

type Item struct{ Name string }

func getPtr() *[]Item { return nil }

func process() {
	for _, item := range *getPtr() {
		_ = item
	}
}
`
	w := checkRangeNilPtr("test.go", "", src)
	if w == "" {
		t.Fatal("expected warning for range over function call dereference")
	}
	if !strings.Contains(w, "non-variable") {
		t.Errorf("warning should mention non-variable expression, got: %s", w)
	}
}

func TestCheckRangeNilPtr_EmptyContent(t *testing.T) {
	w := checkRangeNilPtr("test.go", "", "")
	if w != "" {
		t.Errorf("expected no warning for empty content, got: %s", w)
	}
}

func TestCheckRangeNilPtr_SyntaxError(t *testing.T) {
	src := `package main

func process(items *[]int) {
	for range *items // syntax error
`
	w := checkRangeNilPtr("test.go", "", src)
	if w != "" {
		t.Errorf("expected no warning for unparseable code, got: %s", w)
	}
}

func TestCheckRangeNilPtr_NonGoFile(t *testing.T) {
	w := checkRangeNilPtr("test.py", "", "for x in items:")
	if w != "" {
		t.Errorf("expected no warning for non-Go file, got: %s", w)
	}
}

func TestCheckRangeNilPtr_MapPointer(t *testing.T) {
	src := `package main

func process(m *map[string]int) {
	for k, v := range *m {
		_ = k
		_ = v
	}
}
`
	w := checkRangeNilPtr("test.go", "", src)
	if w == "" {
		t.Fatal("expected warning for range over map pointer")
	}
	if !strings.Contains(w, "m") {
		t.Errorf("warning should mention variable name 'm', got: %s", w)
	}
}

func TestCheckRangeNilPtr_MultipleWarnings(t *testing.T) {
	src := `package main

func process(a *[]int, b *[]int) {
	for _, v := range *a {
		_ = v
	}
	for _, v := range *b {
		_ = v
	}
}
`
	w := checkRangeNilPtr("test.go", "", src)
	if w == "" {
		t.Fatal("expected warnings for multiple range-over-pointer")
	}
	lines := strings.Split(w, "\n")
	if len(lines) < 2 {
		t.Errorf("expected at least 2 warnings, got %d: %s", len(lines), w)
	}
}

func TestCheckRangeNilPtr_NoStarExpr(t *testing.T) {
	src := `package main

func process() {
	m := map[int]int{1: 2}
	for k, v := range m {
		_ = k
		_ = v
	}
}
`
	w := checkRangeNilPtr("test.go", "", src)
	if w != "" {
		t.Errorf("expected no warning for plain map range, got: %s", w)
	}
}

func TestCheckRangeNilPtr_MaxWarnings(t *testing.T) {
	var lines []string
	lines = append(lines, "package main")
	lines = append(lines, "")
	lines = append(lines, "func process() {")
	for i := 0; i < 10; i++ {
		lines = append(lines, "\tx := new([]int)")
		lines = append(lines, "\tfor _, v := range *x {")
		lines = append(lines, "\t\t_ = v")
		lines = append(lines, "\t}")
	}
	lines = append(lines, "}")
	src := strings.Join(lines, "\n")

	w := checkRangeNilPtr("test.go", "", src)
	if w == "" {
		t.Fatal("expected warnings for many range-over-pointer")
	}
	if !strings.Contains(w, "more range-nil-pointer") {
		t.Errorf("expected truncation message, got: %s", w)
	}
}

func TestCheckRangeNilPtr_SelectorDeref(t *testing.T) {
	src := `package main

type S struct{ Items []int }

func process(s *S) {
	for _, v := range *s.Items {
		_ = v
	}
}
`
	// s.Items is a selector expression, not a simple Ident
	// This should still warn but may show as non-variable or no warning
	// since rnpExprName only handles *ast.Ident
	w := checkRangeNilPtr("test.go", "", src)
	// Selector deref: rnpExprName returns "" for selectors, so it either warns
	// as non-variable or no warning. Verify it doesn't crash.
	// The star expr is detected but since s.Items is a SelectorExpr (not Ident),
	// it gets the non-variable message.
	if w == "" {
		t.Error("expected warning for selector dereference range")
	}
}

func TestRnpExprName_Ident(t *testing.T) {
	ident := &ast.Ident{Name: "foo"}
	name := rnpExprName(ident)
	if name != "foo" {
		t.Errorf("expected 'foo', got '%s'", name)
	}
}

func TestRnpExprName_NonIdent(t *testing.T) {
	// A CallExpr is not an Ident, should return ""
	name := rnpExprName(&ast.CallExpr{})
	if name != "" {
		t.Errorf("expected empty string for non-Ident, got '%s'", name)
	}
}

func TestRnpFormatPos(t *testing.T) {
	pos := token.Position{Filename: "file.go", Line: 42}
	result := rnpFormatPos(pos)
	if !strings.Contains(result, "file.go") || !strings.Contains(result, "42") {
		t.Errorf("unexpected position format: %s", result)
	}
}
