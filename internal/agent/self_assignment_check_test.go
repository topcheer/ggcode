package agent

import (
	"testing"
)

func TestCheckSelfAssignment_TrivialSelfAssign(t *testing.T) {
	src := `package main

func foo() {
	x := 1
	x = x
}
`
	result := checkSelfAssignment("test.go", "", src)
	if result == "" {
		t.Fatal("expected self-assignment warning for 'x = x'")
	}
	if !contains(result, "x") {
		t.Errorf("warning should mention variable 'x': %s", result)
	}
}

func TestCheckSelfAssignment_FieldSelfAssign(t *testing.T) {
	src := `package main

type Server struct {
	Port int
}

func foo(s *Server) {
	s.Port = s.Port
}
`
	result := checkSelfAssignment("test.go", "", src)
	if result == "" {
		t.Fatal("expected self-assignment warning for 's.Port = s.Port'")
	}
}

func TestCheckSelfAssignment_NestedFieldSelfAssign(t *testing.T) {
	src := `package main

type Inner struct {
	B int
}

type Outer struct {
	A Inner
}

func foo(o *Outer) {
	o.A.B = o.A.B
}
`
	result := checkSelfAssignment("test.go", "", src)
	if result == "" {
		t.Fatal("expected warning for nested field self-assignment")
	}
}

func TestCheckSelfAssignment_NoFalsePositiveNormalAssign(t *testing.T) {
	src := `package main

func foo() {
	x := 1
	y := 2
	x = y
	y = x + 1
}
`
	result := checkSelfAssignment("test.go", "", src)
	if result != "" {
		t.Errorf("expected no warning for normal assignments, got: %s", result)
	}
}

func TestCheckSelfAssignment_BlankIdentifier(t *testing.T) {
	src := `package main

func foo() {
	x := 1
	_ = x
}
`
	result := checkSelfAssignment("test.go", "", src)
	if result != "" {
		t.Errorf("expected no warning for blank identifier assignment, got: %s", result)
	}
}

func TestCheckSelfAssignment_CompoundOperator(t *testing.T) {
	src := `package main

func foo() {
	x := 1
	x += x
}
`
	result := checkSelfAssignment("test.go", "", src)
	if result != "" {
		t.Errorf("expected no warning for compound operator (+=), got: %s", result)
	}
}

func TestCheckSelfAssignment_NonGoFile(t *testing.T) {
	result := checkSelfAssignment("test.py", "", `x = x`)
	if result != "" {
		t.Errorf("expected no warning for non-Go file, got: %s", result)
	}
}

func TestCheckSelfAssignment_DeltaAware(t *testing.T) {
	oldSrc := `package main

func foo() {
	x := 1
	x = x
}
`
	result := checkSelfAssignment("test.go", oldSrc, oldSrc)
	if result != "" {
		t.Errorf("expected no warning for unchanged code (delta-aware), got: %s", result)
	}
}

func TestCheckSelfAssignment_DeltaDetectsNewInstance(t *testing.T) {
	oldSrc := `package main

func foo() {
	x := 1
	_ = x
}
`
	newSrc := `package main

func foo() {
	x := 1
	x = x
}
`
	result := checkSelfAssignment("test.go", oldSrc, newSrc)
	if result == "" {
		t.Fatal("expected warning for newly added self-assignment")
	}
}

func TestCheckSelfAssignment_MultipleAssign(t *testing.T) {
	src := `package main

func foo() {
	x, y := 1, 2
	x, y = y, x // swap - not self-assignment
}
`
	result := checkSelfAssignment("test.go", "", src)
	if result != "" {
		t.Errorf("expected no warning for swap, got: %s", result)
	}
}

func TestCheckSelfAssignment_SyntaxError(t *testing.T) {
	src := `package main

func foo() {
	x = 
}
`
	result := checkSelfAssignment("test.go", "", src)
	if result != "" {
		t.Errorf("expected no warning for syntax error, got: %s", result)
	}
}

func TestCheckSelfAssignment_IndexExpr(t *testing.T) {
	src := `package main

func foo(a []int) {
	a[0] = a[0]
}
`
	result := checkSelfAssignment("test.go", "", src)
	if result == "" {
		t.Fatal("expected warning for index self-assignment")
	}
}
