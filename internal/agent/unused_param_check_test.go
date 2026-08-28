package agent

import (
	"strings"
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
	z := x * 2
	return z
}
`
	// Non-stub body so the skip can only come from the _test.go path (#1219:
	// the previous fixture was a 1-statement stub and never exercised it).
	warnings := checkUnusedParam("foo_test.go", "", src)
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

// TestUnusedParam_DeltaNoReflag pins #1219 B: findings already present in the
// old content are not re-flagged on every edit of the file.
func TestUnusedParam_DeltaNoReflag(t *testing.T) {
	src := `package main
func helper(x int, y int) int {
	z := x * 2
	_ = z
	return z
}
`
	warnings := checkUnusedParam("test.go", "", src)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning for unused 'y' on fresh content, got %d: %v", len(warnings), warnings)
	}
	// Re-save with an unrelated comment edit above: the pre-existing finding
	// must stay silent instead of firing on every write.
	edited := "package main\n\n// new comment\n" + strings.TrimPrefix(src, "package main\n")
	warnings = checkUnusedParam("test.go", src, edited)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for pre-existing unused param, got: %v", warnings)
	}
}

// TestUnusedParam_DeltaSecondSameNameFunc: adding a second method with the
// same name (and thus the same delta key) is still reported - multiset
// counting, not set membership (#1215 family).
func TestUnusedParam_DeltaSecondSameNameFunc(t *testing.T) {
	old := "package main\ntype A struct{}\ntype B struct{}\n" +
		"func (a A) helper(x int, y int) int { z := x * 2; _ = z; return z }\n"
	new_ := old + "func (b B) helper(x int, y int) int { z := x * 2; _ = z; return z }\n"
	warnings := checkUnusedParam("test.go", old, new_)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning for the newly added method's unused 'y', got %d: %v", len(warnings), warnings)
	}
}

// TestUnusedParam_ShadowingDetected pins #1219 C: an inner shadowing
// declaration (x := 5) no longer masks an unused parameter - scope-aware
// usage via parser object resolution.
func TestUnusedParam_ShadowingDetected(t *testing.T) {
	src := `package main
func f(x int) int {
	if true {
		x := 5
		return x
	}
	return 0
}
`
	warnings := checkUnusedParam("test.go", "", src)
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "'x'") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected warning for param 'x' shadowed in inner scope, got: %v", warnings)
	}
}

// TestUnusedParam_Registered pins #1219 A: the check is wired into the
// write-integrity pipeline for Go files (previously dead code).
func TestUnusedParam_Registered(t *testing.T) {
	for _, c := range allChecks {
		if c.Name == "unused-param" {
			if !c.appliesTo(LangGo) {
				t.Error("unused-param must apply to Go files")
			}
			if c.appliesTo(LangPython) {
				t.Error("unused-param must not apply to non-Go files")
			}
			return
		}
	}
	t.Fatal("unused-param not found in registry")
}
