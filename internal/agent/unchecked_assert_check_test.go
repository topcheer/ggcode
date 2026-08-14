package agent

import (
	"strings"
	"testing"
)

func TestCheckUncheckedTypeAssert_UncheckedAssignment(t *testing.T) {
	old := `package main

func process(v interface{}) {
}
`
	new := `package main

func process(v interface{}) {
	s := v.(string)
	_ = s
}
`
	warnings := checkUncheckedTypeAssert("test.go", old, new)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "1 unchecked type assertion") {
		t.Errorf("warning should mention unchecked type assertion: %s", warnings[0])
	}
}

func TestCheckUncheckedTypeAssert_CommaOkIsSafe(t *testing.T) {
	old := `package main

func process(v interface{}) {
}
`
	new := `package main

func process(v interface{}) {
	s, ok := v.(string)
	_ = s
	_ = ok
}
`
	warnings := checkUncheckedTypeAssert("test.go", old, new)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for comma-ok assertion, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckUncheckedTypeAssert_InCallArgument(t *testing.T) {
	old := `package main

type Handler struct{}

func (h *Handler) Do(v interface{}) {
}
`
	new := `package main

import "fmt"

type Handler struct{}

func (h *Handler) Do(v interface{}) {
	fmt.Println(v.(int))
}
`
	warnings := checkUncheckedTypeAssert("test.go", old, new)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckUncheckedTypeAssert_DeltaAware(t *testing.T) {
	// Old content already has an unchecked assertion; new content keeps it
	// but doesn't add any new ones. Should NOT warn.
	old := `package main

func process(v interface{}) {
	s := v.(string)
	_ = s
}
`
	new := `package main

func process(v interface{}) {
	s := v.(string)
	_ = s
	x := 42
	_ = x
}
`
	warnings := checkUncheckedTypeAssert("test.go", old, new)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings (no new assertions), got %d: %v", len(warnings), warnings)
	}
}

func TestCheckUncheckedTypeAssert_MultipleNew(t *testing.T) {
	old := `package main

func process(v interface{}) {
}
`
	new := `package main

func process(v interface{}) {
	s := v.(string)
	n := v.(int)
	_ = s
	_ = n
}
`
	warnings := checkUncheckedTypeAssert("test.go", old, new)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "2 unchecked type assertions") {
		t.Errorf("warning should mention 2 assertions: %s", warnings[0])
	}
}

func TestCheckUncheckedTypeAssert_NonGoFile(t *testing.T) {
	new := `console.log(x.(string))`
	warnings := checkUncheckedTypeAssert("test.js", "", new)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for non-Go file, got %d", len(warnings))
	}
}

func TestCheckUncheckedTypeAssert_EmptyContent(t *testing.T) {
	warnings := checkUncheckedTypeAssert("test.go", "", "")
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for empty content, got %d", len(warnings))
	}
}

func TestCheckUncheckedTypeAssert_AssignmentCommaOk(t *testing.T) {
	// `s, ok = v.(string)` (regular assignment with comma-ok) is safe.
	old := `package main

func process(v interface{}) {
	var s string
	var ok bool
}
`
	new := `package main

func process(v interface{}) {
	var s string
	var ok bool
	s, ok = v.(string)
	_ = s
	_ = ok
}
`
	warnings := checkUncheckedTypeAssert("test.go", old, new)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for assignment comma-ok, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckUncheckedTypeAssert_VarDeclUnchecked(t *testing.T) {
	old := `package main

func process(v interface{}) {
}
`
	new := `package main

func process(v interface{}) {
	var s = v.(string)
	_ = s
}
`
	warnings := checkUncheckedTypeAssert("test.go", old, new)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning for unchecked var-decl, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckUncheckedTypeAssert_TypeSwitchSafe(t *testing.T) {
	// Type assertions in type switch cases are always safe.
	old := `package main

func process(v interface{}) {
}
`
	new := `package main

func process(v interface{}) {
	switch t := v.(type) {
	case string:
		_ = t
	}
}
`
	warnings := checkUncheckedTypeAssert("test.go", old, new)
	// v.(type) produces a TypeAssertExpr with nil Type, which should not be
	// flagged as it's a type switch, not a regular assertion.
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for type switch, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckUncheckedTypeAssert_LineShiftNotReflagged(t *testing.T) {
	old := `package main
func work(v interface{}) {
	x := v.(int)
	_ = x
	y := v.(string)
	_ = y
}
`
	// Same assertions, 5 unrelated lines inserted above them (#157).
	shifted := `package main

// unrelated comment 1
// unrelated comment 2
// unrelated comment 3
// unrelated comment 4
// unrelated comment 5
func work(v interface{}) {
	x := v.(int)
	_ = x
	y := v.(string)
	_ = y
}
`
	warnings := checkUncheckedTypeAssert("test.go", old, shifted)
	if len(warnings) != 0 {
		t.Errorf("#157 regression: line shift re-flagged pre-existing assertions: %v", warnings)
	}
}
