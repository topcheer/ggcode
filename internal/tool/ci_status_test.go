package tool

import (
	"testing"
)

func TestCIStatusTool_BasicFields(t *testing.T) {
	tool := CIStatusTool{WorkingDir: "/tmp"}

	if tool.Name() != "ci_status" {
		t.Errorf("expected name 'ci_status', got %q", tool.Name())
	}

	desc := tool.Description()
	if desc == "" {
		t.Error("description should not be empty")
	}

	params := tool.Parameters()
	if len(params) == 0 {
		t.Error("parameters should not be empty")
	}
}

func TestCIStatusTool_Clone(t *testing.T) {
	original := CIStatusTool{WorkingDir: "/some/path"}
	cloned := original.Clone()

	ci, ok := cloned.(*CIStatusTool)
	if !ok {
		t.Fatalf("Clone should return *CIStatusTool, got %T", cloned)
	}
	if ci.WorkingDir != original.WorkingDir {
		t.Errorf("WorkingDir mismatch: %q vs %q", ci.WorkingDir, original.WorkingDir)
	}
}

func TestStatusIcon(t *testing.T) {
	tests := []struct {
		status, conclusion, expected string
	}{
		{"in_progress", "", "..."},
		{"queued", "", "[Q]"},
		{"completed", "success", "[OK]"},
		{"completed", "failure", "[!!]"},
		{"completed", "cancelled", "[X]"},
		{"unknown", "", "[?]"},
	}

	for _, tt := range tests {
		got := statusIcon(tt.status, tt.conclusion)
		if got != tt.expected {
			t.Errorf("statusIcon(%q, %q) = %q, want %q", tt.status, tt.conclusion, got, tt.expected)
		}
	}
}

func TestShortSHA(t *testing.T) {
	sha := "abcdef1234567890abcdef1234567890abcdef12"
	got := shortSHA(sha)
	if len(got) != 7 {
		t.Errorf("shortSHA should return 7 chars, got %d: %q", len(got), got)
	}
	if shortSHA("abc") != "abc" {
		t.Error("shortSHA should handle short strings")
	}
}
