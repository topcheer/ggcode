package agent

import (
	"strings"
	"testing"
)

func TestCheckDeadCode_EmptyBranch(t *testing.T) {
	old := ""
	src := `package foo

func bar(x int) int {
	if x > 0 {
	}
	return x
}
`
	warnings := checkDeadCode("test.go", old, src)
	if len(warnings) == 0 {
		t.Fatal("expected at least one empty-branch warning")
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "Empty if body") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected empty-branch warning, got: %v", warnings)
	}
}

func TestCheckDeadCode_EmptyFor(t *testing.T) {
	old := ""
	src := `package foo

func bar(items []int) {
	for i := range items {
	}
}
`
	warnings := checkDeadCode("test.go", old, src)
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "Empty range body") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected empty-range warning, got: %v", warnings)
	}
}

func TestCheckDeadCode_EmptyFuncBody(t *testing.T) {
	old := ""
	src := `package foo

func unimplemented() {
}
`
	warnings := checkDeadCode("test.go", old, src)
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "Empty function body for unimplemented") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected empty-func-body warning, got: %v", warnings)
	}
}

func TestCheckDeadCode_EmptyFuncBody_SkipsInit(t *testing.T) {
	old := ""
	src := `package foo

func init() {
}
`
	warnings := checkDeadCode("test.go", old, src)
	for _, w := range warnings {
		if strings.Contains(w, "Empty function body for init") {
			t.Fatalf("init() should not be flagged: %s", w)
		}
	}
}

func TestCheckDeadCode_EmptyFuncBody_SkipsTestFuncs(t *testing.T) {
	old := ""
	src := `package foo

func TestSomething(t *testing.T) {
}
`
	warnings := checkDeadCode("test_test.go", old, src)
	for _, w := range warnings {
		if strings.Contains(w, "Empty function body") {
			t.Fatalf("test functions should not be flagged: %s", w)
		}
	}
}

func TestCheckDeadCode_DeadAssignment(t *testing.T) {
	old := ""
	src := `package foo

func bar() int {
	x := computeSomething()
	x = 42
	return x
}

func computeSomething() int { return 0 }
`
	warnings := checkDeadCode("test.go", old, src)
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "Dead assignment") && strings.Contains(w, "x") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected dead-assignment warning, got: %v", warnings)
	}
}

func TestCheckDeadCode_NoDeadAssignment_WhenReadBeforeReassign(t *testing.T) {
	old := ""
	src := `package foo

func bar() int {
	x := computeSomething()
	y := x + 1
	x = 42
	return x + y
}

func computeSomething() int { return 0 }
`
	warnings := checkDeadCode("test.go", old, src)
	for _, w := range warnings {
		if strings.Contains(w, "Dead assignment") {
			t.Fatalf("should not flag read-before-reassign: %s", w)
		}
	}
}

func TestCheckDeadCode_UnusedParam(t *testing.T) {
	old := ""
	src := `package foo

func process(data string, count int) string {
	return data
}
`
	warnings := checkDeadCode("test.go", old, src)
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "Unused parameter") && strings.Contains(w, "count") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected unused-param warning, got: %v", warnings)
	}
}

func TestCheckDeadCode_UnusedParam_SkipsUnderscore(t *testing.T) {
	old := ""
	src := `package foo

func process(data string, _ int) string {
	return data
}
`
	warnings := checkDeadCode("test.go", old, src)
	for _, w := range warnings {
		if strings.Contains(w, "Unused parameter") {
			t.Fatalf("underscore params should not be flagged: %s", w)
		}
	}
}

func TestCheckDeadCode_CleanCode(t *testing.T) {
	old := ""
	src := `package foo

func process(data string, count int) string {
	result := data
	for i := 0; i < count; i++ {
		result += "x"
	}
	return result
}
`
	warnings := checkDeadCode("test.go", old, src)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings for clean code, got: %v", warnings)
	}
}

func TestCheckDeadCode_NonGoFile(t *testing.T) {
	warnings := checkDeadCode("test.py", "", "def foo(): pass")
	if warnings != nil {
		t.Fatalf("expected nil for non-Go file, got: %v", warnings)
	}
}

func TestCheckDeadCode_SyntaxError(t *testing.T) {
	old := ""
	src := `package foo

func bar( {`
	warnings := checkDeadCode("test.go", old, src)
	if warnings != nil {
		t.Fatalf("expected nil for syntax error, got: %v", warnings)
	}
}

func TestCheckDeadCode_WarningCap(t *testing.T) {
	old := ""
	src := `package foo

func a(x int) {
	if x > 0 {
	}
	if x < 0 {
	}
	if x == 0 {
	}
	if x > 10 {
	}
	if x > 20 {
	}
}
`
	warnings := checkDeadCode("test.go", old, src)
	if len(warnings) > maxDeadCodeWarnings {
		t.Fatalf("expected at most %d warnings, got %d", maxDeadCodeWarnings, len(warnings))
	}
}
