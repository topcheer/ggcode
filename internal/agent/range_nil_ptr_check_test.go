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

func TestCheckRangeNilPtr_NilGuardEarlyReturn(t *testing.T) {
	src := `package main

type Item struct{ Name string }

func process(items *[]Item) {
	if items == nil {
		return
	}
	for _, item := range *items {
		_ = item
	}
}
`
	w := checkRangeNilPtr("test.go", "", src)
	if w != "" {
		t.Errorf("expected no warning for nil-guarded range, got: %s", w)
	}
}

func TestCheckRangeNilPtr_NilGuardWrappedBlock(t *testing.T) {
	src := `package main

type Item struct{ Name string }

func process(items *[]Item) {
	if items != nil {
		for _, item := range *items {
			_ = item
		}
	}
}
`
	w := checkRangeNilPtr("test.go", "", src)
	if w != "" {
		t.Errorf("expected no warning for range inside if x != nil block, got: %s", w)
	}
}

func TestCheckRangeNilPtr_NilGuardReversedOrder(t *testing.T) {
	// `if nil == items` (reversed operand order) also counts as a guard.
	src := `package main

func process(items *[]int) {
	if nil == items {
		return
	}
	for _, v := range *items {
		_ = v
	}
}
`
	w := checkRangeNilPtr("test.go", "", src)
	if w != "" {
		t.Errorf("expected no warning for reversed nil guard, got: %s", w)
	}
}

func TestCheckRangeNilPtr_GuardDifferentVarStillWarns(t *testing.T) {
	// A guard on a different variable does not protect the range.
	src := `package main

func process(a *[]int, b *[]int) {
	if a == nil {
		return
	}
	for _, v := range *b {
		_ = v
	}
}
`
	w := checkRangeNilPtr("test.go", "", src)
	if w == "" {
		t.Fatal("expected warning when guard is on a different variable")
	}
}

func TestCheckRangeNilPtr_NilCheckAfterRangeStillWarns(t *testing.T) {
	// A nil check appearing AFTER the range statement does not guard it.
	src := `package main

func process(items *[]int) {
	for _, v := range *items {
		_ = v
	}
	if items == nil {
		return
	}
}
`
	w := checkRangeNilPtr("test.go", "", src)
	if w == "" {
		t.Fatal("expected warning when nil check comes after the range")
	}
}

func TestCheckRangeNilPtr_EqNilBlockWrappingStillWarns(t *testing.T) {
	// Range inside `if items == nil` block is NOT guarded (inverted logic).
	src := `package main

func process(items *[]int) {
	if items == nil {
		for _, v := range *items {
			_ = v
		}
	}
}
`
	w := checkRangeNilPtr("test.go", "", src)
	if w == "" {
		t.Fatal("expected warning for range inside if x == nil block")
	}
}

func TestCheckRangeNilPtr_DeltaSuppressesPreexisting(t *testing.T) {
	// The identical unguarded range already exists in old content -- no warning.
	src := `package main

func process(items *[]int) {
	for _, v := range *items {
		_ = v
	}
}
`
	w := checkRangeNilPtr("test.go", src, src)
	if w != "" {
		t.Fatalf("expected no warning for pre-existing unguarded range, got: %s", w)
	}
}

func TestCheckRangeNilPtr_DeltaNewInstanceReported(t *testing.T) {
	old := `package main

func process(items *[]int) {
	for _, v := range *items {
		_ = v
	}
}
`
	// A new unguarded range on a different variable is introduced.
	newContent := old[:len(old)-2] + `
	for _, v := range *other {
		_ = v
	}
}
`
	w := checkRangeNilPtr("test.go", old, newContent)
	if w == "" {
		t.Fatal("expected warning for newly introduced unguarded range")
	}
	if !strings.Contains(w, "other") {
		t.Errorf("warning should mention 'other', got: %s", w)
	}
}

func TestRnpFormatPos(t *testing.T) {
	pos := token.Position{Filename: "file.go", Line: 42}
	result := rnpFormatPos(pos)
	if !strings.Contains(result, "file.go") || !strings.Contains(result, "42") {
		t.Errorf("unexpected position format: %s", result)
	}
}

// TestCheckRangeNilPtr_ElseBlockOfNEQStillWarns verifies #271: a range *x
// inside the ELSE block of `if x != nil` proves x IS nil there, so it must
// warn (deterministic panic) instead of being treated as guarded.
func TestCheckRangeNilPtr_ElseBlockOfNEQStillWarns(t *testing.T) {
	src := `package main

func process(items *[]int) {
	if items != nil {
		_ = len(*items)
	} else {
		for _, v := range *items {
			_ = v
		}
	}
}
`
	w := checkRangeNilPtr("test.go", "", src)
	if w == "" {
		t.Fatal("expected warning for range *items inside else of `if items != nil`")
	}
	if !strings.Contains(w, "items") {
		t.Errorf("warning should mention 'items', got: %s", w)
	}
}

// TestCheckRangeNilPtr_ElseBlockOfEQLNoWarn verifies #271: a range *x inside
// the ELSE block of `if x == nil` proves x is non-nil there -- no warning.
func TestCheckRangeNilPtr_ElseBlockOfEQLNoWarn(t *testing.T) {
	src := `package main

func process(items *[]int) {
	if items == nil {
		return
	} else {
		for _, v := range *items {
			_ = v
		}
	}
}
`
	w := checkRangeNilPtr("test.go", "", src)
	if w != "" {
		t.Errorf("expected no warning for range inside else of `if items == nil`, got: %s", w)
	}
}

// TestCheckRangeNilPtr_ElseIfChainInnerGuardStillWarns verifies #271
// conservative handling of else-if chains: `else if y != nil { range *x }`
// must still warn (the chain link's own condition does not guard x), and the
// outer guard's else-region inversion must not be misapplied to the chain.
func TestCheckRangeNilPtr_ElseIfChainInnerGuardStillWarns(t *testing.T) {
	src := `package main

func process(items *[]int) {
	if items == nil {
		return
	} else if len(*items) == 0 {
		for _, v := range *items {
			_ = v
		}
	}
}
`
	// `items == nil` returns early, so inside the else-if chain items is
	// non-nil in reality; but our conservative chain handling means the
	// else-if body region falls through to the next guard check. The outer
	// guard has thenEnd < rangePos < ifEnd with a non-plain else, so no
	// inversion is applied and no guard matches -> warning expected.
	w := checkRangeNilPtr("test.go", "", src)
	if w == "" {
		t.Fatal("expected warning for range *items inside else-if chain (conservative)")
	}
}

// TestCheckRangeNilPtr_ThenBlockStillGuardedAfterRefactor ensures #271 did not
// break the original then-block semantics.
func TestCheckRangeNilPtr_ThenBlockStillGuardedAfterRefactor(t *testing.T) {
	src := `package main

func process(items *[]int) {
	if items != nil {
		for _, v := range *items {
			_ = v
		}
	}
}
`
	w := checkRangeNilPtr("test.go", "", src)
	if w != "" {
		t.Errorf("expected no warning for range inside `if items != nil` then block, got: %s", w)
	}
}
