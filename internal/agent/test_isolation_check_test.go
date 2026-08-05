package agent

import (
	"testing"
)

func TestCheckTestIsolation_NonTestFile(t *testing.T) {
	result := checkTestIsolation("foo.go", "", "package foo\n")
	if result != "" {
		t.Errorf("expected empty result for non-test file, got %q", result)
	}
}

func TestCheckTestIsolation_EmptyContent(t *testing.T) {
	result := checkTestIsolation("foo_test.go", "", "")
	if result != "" {
		t.Errorf("expected empty result for empty content, got %q", result)
	}
}

func TestCheckTestIsolation_OSSetenv(t *testing.T) {
	newContent := `package foo_test

import "os"
import "testing"

func TestBar(t *testing.T) {
	os.Setenv("MY_VAR", "value")
}
`
	result := checkTestIsolation("foo_test.go", "", newContent)
	if result == "" {
		t.Fatal("expected non-empty result for os.Setenv in new test file")
	}
	if !contains(result, "os.Setenv") {
		t.Errorf("expected result to mention os.Setenv, got %q", result)
	}
	if !contains(result, "t.Setenv") {
		t.Errorf("expected result to recommend t.Setenv, got %q", result)
	}
}

func TestCheckTestIsolation_TSetenvNotFlagged(t *testing.T) {
	newContent := `package foo_test

import "testing"

func TestBar(t *testing.T) {
	t.Setenv("MY_VAR", "value")
}
`
	result := checkTestIsolation("foo_test.go", "", newContent)
	if result != "" {
		t.Errorf("expected empty result for t.Setenv, got %q", result)
	}
}

func TestCheckTestIsolation_OSArgsMutation(t *testing.T) {
	newContent := `package foo_test

import "os"
import "testing"

func TestBar(t *testing.T) {
	os.Args = []string{"prog", "arg1"}
}
`
	result := checkTestIsolation("foo_test.go", "", newContent)
	if result == "" {
		t.Fatal("expected non-empty result for os.Args mutation")
	}
	if !contains(result, "os.Args") {
		t.Errorf("expected result to mention os.Args, got %q", result)
	}
}

func TestCheckTestIsolation_OSStdoutMutation(t *testing.T) {
	newContent := `package foo_test

import "os"
import "testing"

func TestBar(t *testing.T) {
	os.Stdout = nil
}
`
	result := checkTestIsolation("foo_test.go", "", newContent)
	if result == "" {
		t.Fatal("expected non-empty result for os.Stdout mutation")
	}
	if !contains(result, "Stdout") {
		t.Errorf("expected result to mention Stdout, got %q", result)
	}
}

func TestCheckTestIsolation_GlobalVarMutation(t *testing.T) {
	newContent := `package foo_test

import "testing"

var counter int

func TestBar(t *testing.T) {
	counter = 42
}
`
	result := checkTestIsolation("foo_test.go", "", newContent)
	if result == "" {
		t.Fatal("expected non-empty result for global var mutation")
	}
	if !contains(result, "package-level") {
		t.Errorf("expected result to mention package-level variable, got %q", result)
	}
}

func TestCheckTestIsolation_LocalVarNotFlagged(t *testing.T) {
	newContent := `package foo_test

import "testing"

func TestBar(t *testing.T) {
	local := 42
	_ = local
}
`
	result := checkTestIsolation("foo_test.go", "", newContent)
	if result != "" {
		t.Errorf("expected empty result for local variable, got %q", result)
	}
}

func TestCheckTestIsolation_DeltaAware_PreExisting(t *testing.T) {
	oldContent := `package foo_test

import "os"
import "testing"

func TestBar(t *testing.T) {
	os.Setenv("MY_VAR", "value")
}
`
	newContent := oldContent + "\nfunc TestBaz(t *testing.T) {}\n"
	result := checkTestIsolation("foo_test.go", oldContent, newContent)
	if result != "" {
		t.Errorf("expected empty result when violation count did not increase, got %q", result)
	}
}

func TestCheckTestIsolation_DeltaAware_NewViolation(t *testing.T) {
	oldContent := `package foo_test

import "testing"

func TestBar(t *testing.T) {
}
`
	newContent := `package foo_test

import "os"
import "testing"

func TestBar(t *testing.T) {
	os.Setenv("MY_VAR", "value")
}
`
	result := checkTestIsolation("foo_test.go", oldContent, newContent)
	if result == "" {
		t.Fatal("expected non-empty result when new violation is introduced")
	}
}

func TestCheckTestIsolation_NonTestFunctionNotFlagged(t *testing.T) {
	newContent := `package foo_test

import "os"

func helperSetup() {
	os.Setenv("MY_VAR", "value")
}
`
	// os.Setenv in non-Test functions should not be flagged.
	result := checkTestIsolation("foo_test.go", "", newContent)
	if result != "" {
		t.Errorf("expected empty result for os.Setenv in non-test function, got %q", result)
	}
}

func TestCheckTestIsolation_MultipleViolations(t *testing.T) {
	newContent := `package foo_test

import "os"
import "testing"

var counter int

func TestBar(t *testing.T) {
	os.Setenv("A", "1")
	os.Args = []string{"x"}
	counter = 5
	os.Stderr = nil
}
`
	result := checkTestIsolation("foo_test.go", "", newContent)
	if result == "" {
		t.Fatal("expected non-empty result for multiple violations")
	}
	// Should categorize the violations.
	if !contains(result, "os.Setenv") {
		t.Errorf("expected os.Setenv category, got %q", result)
	}
	if !contains(result, "os.Args") {
		t.Errorf("expected os.Args category, got %q", result)
	}
	if !contains(result, "package-level") {
		t.Errorf("expected package-level category, got %q", result)
	}
}

func TestCheckTestIsolation_SyntaxError(t *testing.T) {
	newContent := "package foo_test\n\nfunc broken(\n"
	result := checkTestIsolation("foo_test.go", "", newContent)
	if result != "" {
		t.Errorf("expected empty result for syntax error, got %q", result)
	}
}

func TestCheckTestIsolation_CleanTestNotFlagged(t *testing.T) {
	newContent := `package foo_test

import "testing"

func TestBar(t *testing.T) {
	result := add(1, 2)
	if result != 3 {
		t.Errorf("expected 3, got %d", result)
	}
}

func add(a, b int) int { return a + b }
`
	result := checkTestIsolation("foo_test.go", "", newContent)
	if result != "" {
		t.Errorf("expected empty result for clean test, got %q", result)
	}
}

// contains and indexOf are defined in reflection_test.go
