package agent

import (
	"strings"
	"testing"
)

func TestCheckExitPath_RedundantElse(t *testing.T) {
	src := `package foo

func handler(x int) int {
	if x > 0 {
		return 1
	} else {
		return 0
	}
}
`
	warnings := checkExitPath("/tmp/foo.go", "", src)
	if len(warnings) == 0 {
		t.Fatal("expected redundant-else warning, got none")
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "redundant else") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected 'redundant else' in warnings, got: %v", warnings)
	}
}

func TestCheckExitPath_NoRedundantElseWithElseIf(t *testing.T) {
	src := `package foo

func handler(x int) int {
	if x > 0 {
		return 1
	} else if x < 0 {
		return -1
	}
	return 0
}
`
	warnings := checkExitPath("/tmp/foo.go", "", src)
	for _, w := range warnings {
		if strings.Contains(w, "redundant else") {
			t.Fatalf("else-if chain should not be flagged as redundant: %s", w)
		}
	}
}

func TestCheckExitPath_NoRedundantElseWithoutTerminator(t *testing.T) {
	// if-body does NOT end with return/break/continue, so else is fine.
	src := `package foo

func handler(x int) int {
	if x > 0 {
		x = x * 2
	} else {
		x = -x
	}
	return x
}
`
	warnings := checkExitPath("/tmp/foo.go", "", src)
	for _, w := range warnings {
		if strings.Contains(w, "redundant else") {
			t.Fatalf("non-terminating if body should not be flagged: %s", w)
		}
	}
}

func TestCheckExitPath_RedundantElseAfterBreak(t *testing.T) {
	src := `package foo

func handler(items []int) int {
	for _, v := range items {
		if v == 42 {
			break
		} else {
			continue
		}
	}
	return 0
}
`
	warnings := checkExitPath("/tmp/foo.go", "", src)
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "redundant else") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected redundant-else after break, got: %v", warnings)
	}
}

func TestCheckExitPath_DeepNesting(t *testing.T) {
	src := `package foo

func handler(a, b, c, d bool) int {
	if a {
		if b {
			if c {
				return 1
			}
		}
	}
	return 0
}
`
	warnings := checkExitPath("/tmp/foo.go", "", src)
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "deeply nested") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected deep-nesting warning, got: %v", warnings)
	}
}

func TestCheckExitPath_NoDeepNestingShallow(t *testing.T) {
	src := `package foo

func handler(a, b bool) int {
	if a {
		if b {
			return 1
		}
	}
	return 0
}
`
	warnings := checkExitPath("/tmp/foo.go", "", src)
	for _, w := range warnings {
		if strings.Contains(w, "deeply nested") {
			t.Fatalf("depth 2 should not be flagged: %s", w)
		}
	}
}

func TestCheckExitPath_DeltaAware(t *testing.T) {
	oldSrc := `package foo

func handler(x int) int {
	if x > 0 {
		return 1
	} else {
		return 0
	}
}
`
	newSrc := oldSrc + "\n// unchanged\n"
	warnings := checkExitPath("/tmp/foo.go", oldSrc, newSrc)
	for _, w := range warnings {
		if strings.Contains(w, "redundant else") {
			t.Fatalf("pre-existing issue should be delta-filtered: %s", w)
		}
	}
}

func TestCheckExitPath_SkipsTestFiles(t *testing.T) {
	src := `package foo

func handler(x int) int {
	if x > 0 {
		return 1
	} else {
		return 0
	}
}
`
	warnings := checkExitPath("/tmp/foo_test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("test files should be skipped, got: %v", warnings)
	}
}

func TestCheckExitPath_SkipsNonGo(t *testing.T) {
	warnings := checkExitPath("/tmp/foo.py", "", "def x():\n\tpass\n")
	if len(warnings) != 0 {
		t.Fatalf("non-Go files should be skipped, got: %v", warnings)
	}
}

func TestCheckExitPath_NilCheckNoFalsePositive(t *testing.T) {
	// Classic Go pattern: if err != nil { return err }; no else at all.
	src := `package foo

func handler() error {
	err := doWork()
	if err != nil {
		return err
	}
	return nil
}
`
	warnings := checkExitPath("/tmp/foo.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("clean guard clause should not be flagged, got: %v", warnings)
	}
}

func TestCheckExitPath_BothIssues(t *testing.T) {
	src := `package foo

func handler(a, b, c bool) int {
	if a {
		if b {
			if c {
				return 1
			} else {
				return 2
			}
		}
	}
	return 0
}
`
	warnings := checkExitPath("/tmp/foo.go", "", src)
	hasRedundant := false
	hasDeep := false
	for _, w := range warnings {
		if strings.Contains(w, "redundant else") {
			hasRedundant = true
		}
		if strings.Contains(w, "deeply nested") {
			hasDeep = true
		}
	}
	if !hasRedundant {
		t.Errorf("expected redundant-else warning")
	}
	if !hasDeep {
		t.Errorf("expected deep-nesting warning")
	}
}
