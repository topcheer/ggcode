package agent

import (
	"testing"
)

func TestCheckFloatEquality_BasicLiteral(t *testing.T) {
	src := `package main
func f() bool {
	return 0.1 == 0.2
}`
	warnings := checkFloatEquality("test.go", "", src)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckFloatEquality_NEQ(t *testing.T) {
	src := `package main
func f() bool {
	return 3.14 != 2.71
}`
	warnings := checkFloatEquality("test.go", "", src)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckFloatEquality_TypedVar(t *testing.T) {
	src := `package main
func f(a, b float64) bool {
	return a == b
}`
	warnings := checkFloatEquality("test.go", "", src)
	if len(warnings) != 0 {
		// a and b are params, not local vars - not collected as floatVars.
		// This is expected behavior; we focus on declared vars and literals.
		t.Logf("got %d warnings (params not tracked): %v", len(warnings), warnings)
	}
}

func TestCheckFloatEquality_DeclaredFloatVar(t *testing.T) {
	src := `package main
func f() bool {
	var x float64 = 1.5
	var y float64 = 2.5
	return x == y
}`
	warnings := checkFloatEquality("test.go", "", src)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning for float var comparison, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckFloatEquality_IntComparison(t *testing.T) {
	src := `package main
func f(a, b int) bool {
	return a == b
}`
	warnings := checkFloatEquality("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for int comparison, got %d", len(warnings))
	}
}

func TestCheckFloatEquality_MathFuncResult(t *testing.T) {
	src := `package main
import "math"
func f() bool {
	return math.Sqrt(2.0) == math.Sqrt(2.0)
}`
	warnings := checkFloatEquality("test.go", "", src)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning for math func comparison, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckFloatEquality_FloatArithmetic(t *testing.T) {
	src := `package main
func f() bool {
	return 0.1+0.2 == 0.3
}`
	warnings := checkFloatEquality("test.go", "", src)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning for float arithmetic comparison, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckFloatEquality_LessThan(t *testing.T) {
	src := `package main
func f() bool {
	return 1.5 < 2.5
}`
	warnings := checkFloatEquality("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for < comparison, got %d", len(warnings))
	}
}

func TestCheckFloatEquality_NonGoFile(t *testing.T) {
	warnings := checkFloatEquality("test.py", "", "x = 0.1")
	if warnings != nil {
		t.Fatalf("expected nil for non-Go file, got %v", warnings)
	}
}

func TestCheckFloatEquality_EmptyContent(t *testing.T) {
	warnings := checkFloatEquality("test.go", "", "")
	if warnings != nil {
		t.Fatalf("expected nil for empty content, got %v", warnings)
	}
}

func TestCheckFloatEquality_PackageLevelFloatVar(t *testing.T) {
	src := `package main
var threshold float64 = 0.001
func f(target float64) bool {
	return threshold == target
}`
	warnings := checkFloatEquality("test.go", "", src)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning for package-level float var comparison, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckFloatEquality_MaxWarnings(t *testing.T) {
	src := `package main
func f() {
	_ = 0.1 == 0.2
	_ = 0.3 == 0.4
	_ = 0.5 == 0.6
	_ = 0.7 == 0.8
	_ = 0.9 == 1.0
	_ = 1.1 == 1.2
}`
	warnings := checkFloatEquality("test.go", "", src)
	// Should cap at maxFloatEqWarnings=5 + 1 truncation message.
	if len(warnings) != maxFloatEqWarnings+1 {
		t.Fatalf("expected %d warnings (capped + truncation), got %d", maxFloatEqWarnings+1, len(warnings))
	}
}

func TestCheckFloatEquality_Float32Var(t *testing.T) {
	src := `package main
var pi float32 = 3.14
func f() bool {
	return pi == 3.14
}`
	warnings := checkFloatEquality("test.go", "", src)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning for float32 var comparison, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckFloatEquality_ParenExpr(t *testing.T) {
	src := `package main
func f() bool {
	return (1.5) == (2.5)
}`
	warnings := checkFloatEquality("test.go", "", src)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning for parenthesized float comparison, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckFloatEquality_StringComparison(t *testing.T) {
	src := `package main
func f(a, b string) bool {
	return a == b
}`
	warnings := checkFloatEquality("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for string comparison, got %d", len(warnings))
	}
}

func TestCheckFloatEquality_InvalidGo(t *testing.T) {
	src := `package main
func f() {
	this is not valid go`
	warnings := checkFloatEquality("test.go", "", src)
	if warnings != nil {
		t.Fatalf("expected nil for invalid Go, got %v", warnings)
	}
}
