package agent

import (
	"strings"
	"testing"
)

func TestCheckStringEfficiency_EqualFold(t *testing.T) {
	src := `package main

import "strings"

func compare(a, b string) bool {
	return strings.ToLower(a) == strings.ToLower(b)
}
`
	warns := checkStringEfficiency("test.go", "", src)
	if len(warns) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(warns), warns)
	}
	if !strings.Contains(warns[0], "EqualFold") {
		t.Errorf("warning should mention EqualFold, got: %s", warns[0])
	}
}

func TestCheckStringEfficiency_EqualFoldToUpper(t *testing.T) {
	src := `package main

import "strings"

func compare(a, b string) bool {
	return strings.ToUpper(a) != strings.ToUpper(b)
}
`
	warns := checkStringEfficiency("test.go", "", src)
	if len(warns) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(warns), warns)
	}
	if !strings.Contains(warns[0], "EqualFold") {
		t.Errorf("warning should mention EqualFold, got: %s", warns[0])
	}
}

func TestCheckStringEfficiency_FmtSprintConcat(t *testing.T) {
	src := `package main

import "fmt"

func greet(prefix, name string) string {
	return fmt.Sprint(prefix, name)
}
`
	warns := checkStringEfficiency("test.go", "", src)
	if len(warns) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(warns), warns)
	}
	if !strings.Contains(warns[0], "concat") {
		t.Errorf("warning should mention concat, got: %s", warns[0])
	}
}

func TestCheckStringEfficiency_FmtSprintMixedTypes(t *testing.T) {
	src := `package main

import "fmt"

func format(id int) string {
	return fmt.Sprint("ID: ", id)
}
`
	warns := checkStringEfficiency("test.go", "", src)
	if len(warns) != 0 {
		t.Fatalf("expected 0 warnings for mixed-type Sprint, got %d: %v", len(warns), warns)
	}
}

func TestCheckStringEfficiency_NormalComparison(t *testing.T) {
	src := `package main

func compare(a, b string) bool {
	return a == b
}
`
	warns := checkStringEfficiency("test.go", "", src)
	if len(warns) != 0 {
		t.Fatalf("expected 0 warnings for normal comparison, got %d: %v", len(warns), warns)
	}
}

func TestCheckStringEfficiency_DeltaAware(t *testing.T) {
	src := `package main

import "strings"

func compare(a, b string) bool {
	return strings.ToLower(a) == strings.ToLower(b)
}
`
	warns := checkStringEfficiency("test.go", src, src)
	if len(warns) != 0 {
		t.Fatalf("expected 0 warnings for unchanged code, got %d: %v", len(warns), warns)
	}
}

func TestCheckStringEfficiency_DeltaAwareNewPattern(t *testing.T) {
	old := `package main

import "strings"

func compare(a, b string) bool {
	return strings.ToLower(a) == strings.ToLower(b)
}
`
	newSrc := `package main

import "strings"

func compare(a, b string) bool {
	return strings.ToLower(a) == strings.ToLower(b)
}

func compare2(c, d string) bool {
	return strings.ToUpper(c) == strings.ToUpper(d)
}
`
	warns := checkStringEfficiency("test.go", old, newSrc)
	if len(warns) != 1 {
		t.Fatalf("expected 1 warning for new pattern, got %d: %v", len(warns), warns)
	}
}

func TestCheckStringEfficiency_TestFileSkipped(t *testing.T) {
	src := `package main

import "strings"

func compare(a, b string) bool {
	return strings.ToLower(a) == strings.ToLower(b)
}
`
	warns := checkStringEfficiency("foo_test.go", "", src)
	if len(warns) != 0 {
		t.Fatalf("expected 0 warnings for test file, got %d", len(warns))
	}
}

func TestCheckStringEfficiency_NonGoSkipped(t *testing.T) {
	src := `strings.ToLower(a) == strings.ToLower(b)`
	warns := checkStringEfficiency("test.py", "", src)
	if len(warns) != 0 {
		t.Fatalf("expected 0 warnings for non-Go file, got %d", len(warns))
	}
}

func TestCheckStringEfficiency_BothPatterns(t *testing.T) {
	src := `package main

import (
	"fmt"
	"strings"
)

func process(prefix, name, a, b string) string {
	msg := fmt.Sprint(prefix, name)
	if strings.ToLower(a) == strings.ToLower(b) {
		return msg
	}
	return ""
}
`
	warns := checkStringEfficiency("test.go", "", src)
	if len(warns) != 1 {
		t.Fatalf("expected 1 warning, got %d", len(warns))
	}
	if !strings.Contains(warns[0], "EqualFold") || !strings.Contains(warns[0], "concat") {
		t.Errorf("warning should mention both patterns, got: %s", warns[0])
	}
}

func TestCheckStringEfficiency_FmtSprintlnNotFlagged(t *testing.T) {
	src := `package main

import "fmt"

func greet(a, b string) string {
	return fmt.Sprintln(a, b)
}
`
	warns := checkStringEfficiency("test.go", "", src)
	if len(warns) != 0 {
		t.Fatalf("expected 0 warnings for Sprintln, got %d: %v", len(warns), warns)
	}
}
