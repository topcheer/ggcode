package agent

import (
	"strings"
	"testing"
)

func TestDescribeJSONError_SyntaxError(t *testing.T) {
	raw := []byte(`{"path": "a.go",}`)
	got := describeJSONError(raw)
	if got == "" || got == "unknown parse error" {
		t.Fatalf("expected a syntax error description, got %q", got)
	}
	if !strings.Contains(got, "offset") {
		t.Errorf("expected offset info in description, got %q", got)
	}
}

func TestDescribeJSONError_Truncated(t *testing.T) {
	raw := []byte(`{"path": "abc`)
	got := describeJSONError(raw)
	if got == "" || got == "unknown parse error" {
		t.Fatalf("expected truncated-JSON description, got %q", got)
	}
	if !strings.Contains(got, "offset") {
		t.Errorf("expected offset info in description, got %q", got)
	}
}

func TestDescribeJSONError_ValidJSON(t *testing.T) {
	raw := []byte(`{"path": "a.go"}`)
	if got := describeJSONError(raw); got != "unknown parse error" {
		t.Errorf("valid JSON should yield unknown-parse fallback, got %q", got)
	}
}
