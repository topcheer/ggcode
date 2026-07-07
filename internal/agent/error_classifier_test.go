package agent

import (
	"testing"
)

func TestClassifyErrorContent_FileNotFound(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{"no such file or directory", "open /path/to/file: no such file or directory"},
		{"file does not exist", "Error: file does not exist"},
		{"file not found", "Error: file not found"},
		{"no such file", "stat /tmp/missing: no such file"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cat := classifyErrorContent("read_file", tt.content)
			if cat.Name != "file_not_found" {
				t.Errorf("expected file_not_found, got %s", cat.Name)
			}
			if cat.Guidance == "" {
				t.Error("guidance should not be empty")
			}
		})
	}
}

func TestClassifyErrorContent_PermissionDenied(t *testing.T) {
	cat := classifyErrorContent("write_file", "open /file: permission denied")
	if cat.Name != "permission_denied" {
		t.Errorf("expected permission_denied, got %s", cat.Name)
	}
}

func TestClassifyErrorContent_NilPointer(t *testing.T) {
	tests := []string{
		"panic: runtime error: invalid memory address or nil pointer dereference",
		"nil pointer dereference",
		"runtime error: invalid memory address",
	}
	for _, c := range tests {
		cat := classifyErrorContent("run_command", c)
		if cat.Name != "nil_pointer" {
			t.Errorf("for %q: expected nil_pointer, got %s", c, cat.Name)
		}
	}
}

func TestClassifyErrorContent_TypeError(t *testing.T) {
	tests := []string{
		"cannot use foo (type int) as type string",
		"undefined: someFunc",
		"someVar not defined",
	}
	for _, c := range tests {
		cat := classifyErrorContent("run_command", c)
		if cat.Name != "type_error" {
			t.Errorf("for %q: expected type_error, got %s", c, cat.Name)
		}
	}
}

func TestClassifyErrorContent_ImportError(t *testing.T) {
	tests := []string{
		"cannot find package github.com/foo/bar",
		"no required module provides package foo",
		"import cycle not allowed",
	}
	for _, c := range tests {
		cat := classifyErrorContent("run_command", c)
		if cat.Name != "import_error" {
			t.Errorf("for %q: expected import_error, got %s", c, cat.Name)
		}
	}
}

func TestClassifyErrorContent_SyntaxError(t *testing.T) {
	tests := []string{
		"syntax error: unexpected token",
		"unexpected character",
		"expected ';' but got '{'",
		"unterminated string literal",
	}
	for _, c := range tests {
		cat := classifyErrorContent("run_command", c)
		if cat.Name != "syntax_error" {
			t.Errorf("for %q: expected syntax_error, got %s", c, cat.Name)
		}
	}
}

func TestClassifyErrorContent_TimeoutNetwork(t *testing.T) {
	tests := []string{
		"request timeout",
		"timed out waiting for response",
		"context deadline exceeded",
		"connection refused",
		"connection reset by peer",
		"no such host",
		"i/o timeout",
	}
	for _, c := range tests {
		cat := classifyErrorContent("run_command", c)
		if cat.Name != "timeout_network" {
			t.Errorf("for %q: expected timeout_network, got %s", c, cat.Name)
		}
	}
}

func TestClassifyErrorContent_BuildError(t *testing.T) {
	tests := []string{
		"declared and not used: foo",
		"not enough arguments in call to bar",
		"too many arguments in call to baz",
		"mismatched types: int and string",
	}
	for _, c := range tests {
		cat := classifyErrorContent("run_command", c)
		if cat.Name != "build_error" {
			t.Errorf("for %q: expected build_error, got %s", c, cat.Name)
		}
	}
}

func TestClassifyErrorContent_EditAnchorMismatch(t *testing.T) {
	cat := classifyErrorContent("edit_file", "old_text not found in file")
	if cat.Name != "edit_anchor_mismatch" {
		t.Errorf("expected edit_anchor_mismatch, got %s", cat.Name)
	}
}

func TestClassifyErrorContent_TestFailure(t *testing.T) {
	tests := []string{
		"--- FAIL: TestFoo",
		"test failed: expected 1 but got 2",
		"assertion failed: values don't match",
	}
	for _, c := range tests {
		cat := classifyErrorContent("run_command", c)
		if cat.Name != "test_failure" {
			t.Errorf("for %q: expected test_failure, got %s", c, cat.Name)
		}
	}
}

func TestClassifyErrorContent_GenericFallback(t *testing.T) {
	cat := classifyErrorContent("run_command", "something weird happened")
	if cat.Name != "" {
		t.Errorf("expected empty category for unrecognized error, got %s", cat.Name)
	}
}

func TestErrorClassifier_FiresOncePerCategory(t *testing.T) {
	ec := NewErrorClassifier()

	// First call — should fire
	cat1 := ec.classifyToolError("edit_file", "no such file or directory")
	if cat1.Name != "file_not_found" {
		t.Fatalf("expected file_not_found on first call, got %s", cat1.Name)
	}

	// Second call with same category — should NOT fire (already fired)
	cat2 := ec.classifyToolError("read_file", "file does not exist")
	if cat2.Name != "" {
		t.Errorf("expected empty (already fired), got %s", cat2.Name)
	}

	// Different category — should fire
	cat3 := ec.classifyToolError("run_command", "panic: nil pointer dereference")
	if cat3.Name != "nil_pointer" {
		t.Errorf("expected nil_pointer, got %s", cat3.Name)
	}
}

func TestErrorClassifier_Reset(t *testing.T) {
	ec := NewErrorClassifier()

	// Fire a category
	ec.classifyToolError("edit_file", "no such file or directory")

	// Reset — should allow firing again
	ec.reset()

	cat := ec.classifyToolError("read_file", "file does not exist")
	if cat.Name != "file_not_found" {
		t.Errorf("expected file_not_found after reset, got %s", cat.Name)
	}
}

func TestErrorContainsAny(t *testing.T) {
	if !errorContainsAny("hello world", "hello", "foo") {
		t.Error("expected match for 'hello'")
	}
	if errorContainsAny("hello world", "foo", "bar") {
		t.Error("expected no match")
	}
}

func TestClassifyErrorContent_OrderingPrecedence(t *testing.T) {
	// "cannot find package" should match import_error now (since we removed
	// the generic "cannot find" from file_not_found)
	cat := classifyErrorContent("run_command", "cannot find package github.com/foo/bar")
	if cat.Name != "import_error" {
		t.Errorf("expected import_error, got %s", cat.Name)
	}
}
