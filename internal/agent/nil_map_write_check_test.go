package agent

import (
	"strings"
	"testing"
)

func TestCheckNilMapWrite_BasicPanicCase(t *testing.T) {
	src := `package main

func foo() {
	var m map[string]int
	m["key"] = 1
}
`
	result := checkNilMapWrite("test.go", "", src)
	if result == "" {
		t.Fatal("expected nil map write warning, got empty")
	}
	if !contains(result, "m") {
		t.Errorf("warning should mention map name 'm': %s", result)
	}
}

func TestCheckNilMapWrite_InitializedWithMake(t *testing.T) {
	src := `package main

func foo() {
	var m map[string]int
	m = make(map[string]int)
	m["key"] = 1
}
`
	result := checkNilMapWrite("test.go", "", src)
	if result != "" {
		t.Errorf("expected no warning (map initialized with make), got: %s", result)
	}
}

func TestCheckNilMapWrite_InitializedWithLiteral(t *testing.T) {
	src := `package main

func foo() {
	var m map[string]int
	m = map[string]int{"init": 0}
	m["key"] = 1
}
`
	result := checkNilMapWrite("test.go", "", src)
	if result != "" {
		t.Errorf("expected no warning (map initialized with literal), got: %s", result)
	}
}

func TestCheckNilMapWrite_ShortVarDeclWithMake(t *testing.T) {
	src := `package main

func foo() {
	m := make(map[string]int)
	m["key"] = 1
}
`
	result := checkNilMapWrite("test.go", "", src)
	if result != "" {
		t.Errorf("expected no warning (short decl with make), got: %s", result)
	}
}

func TestCheckNilMapWrite_ShortVarDeclWithLiteral(t *testing.T) {
	src := `package main

func foo() {
	m := map[string]int{"init": 0}
	m["key"] = 1
}
`
	result := checkNilMapWrite("test.go", "", src)
	if result != "" {
		t.Errorf("expected no warning (short decl with literal), got: %s", result)
	}
}

func TestCheckNilMapWrite_NonGoFile(t *testing.T) {
	result := checkNilMapWrite("test.py", "", `var m = {}; m["key"] = 1`)
	if result != "" {
		t.Errorf("expected no warning for non-Go file, got: %s", result)
	}
}

func TestCheckNilMapWrite_DeltaAware(t *testing.T) {
	oldSrc := `package main

func foo() {
	var m map[string]int
	m["key"] = 1
}
`
	newSrc := oldSrc // No change
	result := checkNilMapWrite("test.go", oldSrc, newSrc)
	if result != "" {
		t.Errorf("expected no warning for unchanged code (delta-aware), got: %s", result)
	}
}

func TestCheckNilMapWrite_DeltaDetectsNewInstance(t *testing.T) {
	oldSrc := `package main

func foo() {
	x := 1
	_ = x
}
`
	newSrc := `package main

func foo() {
	x := 1
	_ = x
	var m map[string]int
	m["key"] = 1
}
`
	result := checkNilMapWrite("test.go", oldSrc, newSrc)
	if result == "" {
		t.Fatal("expected warning for newly added nil map write")
	}
}

func TestCheckNilMapWrite_ReadOnlyAccess(t *testing.T) {
	src := `package main

import "fmt"

func foo() {
	var m map[string]int
	v := m["key"]
	fmt.Println(v)
}
`
	result := checkNilMapWrite("test.go", "", src)
	if result != "" {
		t.Errorf("expected no warning for read-only nil map access, got: %s", result)
	}
}

func TestCheckNilMapWrite_IncDecWrite(t *testing.T) {
	src := `package main

func foo() {
	var m map[string]int
	m["key"]++
}
`
	result := checkNilMapWrite("test.go", "", src)
	if result == "" {
		t.Fatal("expected warning for nil map write via ++ operator")
	}
}

func TestCheckNilMapWrite_AssignedFromExpression(t *testing.T) {
	src := `package main

func getMap() map[string]int {
	return map[string]int{}
}

func foo() {
	var m map[string]int
	m = getMap()
	m["key"] = 1
}
`
	result := checkNilMapWrite("test.go", "", src)
	if result != "" {
		t.Errorf("expected no warning (map assigned from function call), got: %s", result)
	}
}

func TestCheckNilMapWrite_SyntaxError(t *testing.T) {
	src := `package main

func foo() {
	var m map[string]int
	m["key"] = 
}
`
	result := checkNilMapWrite("test.go", "", src)
	if result != "" {
		t.Errorf("expected no warning for syntactically invalid code, got: %s", result)
	}
}

// TestNilMapWriteCrossFunctionDelta verifies that a nil-map write on a
// variable with the same name as one in another (pre-existing) function is
// NOT suppressed by the delta filter — map names are function-scoped (#222).
func TestNilMapWriteCrossFunctionDelta(t *testing.T) {
	old := `package p
func a() {
	var m map[string]int
	m["k"] = 1
}
`
	edited := old + `
func b() {
	var m map[string]int
	m["x"] = 2
}
`
	out := checkNilMapWrite("test.go", old, edited)
	// The func-b write lands on line 9 (old content is 5 lines + 2 new decl
	// lines + blank). Line-based check proves it is the NEW instance that
	// survived the delta filter, not the pre-existing func-a one (line 3).
	if !strings.Contains(out, "test.go:9") {
		t.Errorf("new same-named nil-map write in func b must be flagged, got: %q", out)
	}
	// Unrelated edit to the pre-existing pattern must not re-warn.
	if out2 := checkNilMapWrite("test.go", old, "// note\n"+old); out2 != "" {
		t.Errorf("pre-existing nil-map write re-reported on unrelated edit: %q", out2)
	}
}
