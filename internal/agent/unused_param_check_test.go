package agent

import (
	"testing"
)

func TestUnusedParam_BasicUnused(t *testing.T) {
	src := `package main
func helper(x int, y int) int {
	z := x * 2
	return z
}
`
	warnings := checkUnusedParam("", "test.go", src)
	if len(warnings) == 0 {
		t.Fatal("expected at least 1 warning for unused param 'y'")
	}
	found := false
	for _, w := range warnings {
		if upContains(w, "'y'") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("did not find warning for unused param 'y' in: %v", warnings)
	}
}

func TestUnusedParam_AllUsed(t *testing.T) {
	src := `package main
func helper(x int, y int) int {
	return x + y
}
`
	warnings := checkUnusedParam("", "test.go", src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings, got: %v", warnings)
	}
}

func TestUnusedParam_SkipExported(t *testing.T) {
	src := `package main
func Helper(x int, y int) int {
	return x * 2
}
`
	warnings := checkUnusedParam("", "test.go", src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for exported func, got: %v", warnings)
	}
}

func TestUnusedParam_SkipBlankIdent(t *testing.T) {
	src := `package main
func helper(x int, _ int) int {
	return x * 2
}
`
	warnings := checkUnusedParam("", "test.go", src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for blank ident, got: %v", warnings)
	}
}

func TestUnusedParam_SkipTestFiles(t *testing.T) {
	src := `package main
func helper(x int, y int) int {
	return x * 2
}
`
	warnings := checkUnusedParam("", "foo_test.go", src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for test file, got: %v", warnings)
	}
}

func TestUnusedParam_SkipStub(t *testing.T) {
	src := `package main
func helper(x int, y int) int {
	return 0
}
`
	warnings := checkUnusedParam("", "test.go", src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for stub, got: %v", warnings)
	}
}

func TestUnusedParam_MultipleUnused(t *testing.T) {
	src := `package main
func process(a int, b int, c int, d int) int {
	a = a + 1
	c = c * 2
	d = d - 3
	return a + c + d
}
`
	warnings := checkUnusedParam("", "test.go", src)
	if len(warnings) != 1 {
		t.Fatalf("expected exactly 1 warning (only b unused), got %d: %v", len(warnings), warnings)
	}
}

func TestUnusedParam_NoBody(t *testing.T) {
	src := `package main
func helper(x int, y int) int
`
	warnings := checkUnusedParam("", "test.go", src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for no-body func, got: %v", warnings)
	}
}

func TestUnusedParam_UsedInClosure(t *testing.T) {
	src := `package main
func helper(x int, y int) func() int {
	return func() int {
		return x + y
	}
}
`
	warnings := checkUnusedParam("", "test.go", src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings when params used in closure, got: %v", warnings)
	}
}

func TestUnusedParam_UsedInAssignment(t *testing.T) {
	src := `package main
var g int
func helper(x int, y int) {
	g = x
	g = y
	g = g + 1
}
`
	warnings := checkUnusedParam("", "test.go", src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings when params used in assignment, got: %v", warnings)
	}
}

func upContains(s, substr string) bool {
	return len(s) >= len(substr) && len(substr) > 0 &&
		indexOfSub(s, substr) >= 0
}

func indexOfSub(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
