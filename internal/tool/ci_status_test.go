package tool

import (
	"encoding/json"
	"strings"
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

// TestCIStatusTool_PRHeadParamPinned pins the PR-head query support: the
// schema must expose a 'pr' parameter documented as resolving the PR's head
// branch with precedence over 'branch'/current-branch default. This guards
// against the false-green failure mode where a main checkout reports main's
// checks while the PR head is failing (#5035 incident).
func TestCIStatusTool_PRHeadParamPinned(t *testing.T) {
	tool := CIStatusTool{WorkingDir: "/tmp"}

	var schema struct {
		Properties map[string]struct {
			Type        string `json:"type"`
			Description string `json:"description"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(tool.Parameters(), &schema); err != nil {
		t.Fatalf("parameters schema is not valid JSON: %v", err)
	}

	pr, ok := schema.Properties["pr"]
	if !ok {
		t.Fatal("schema missing 'pr' property: PR-head queries must be expressible (false-green risk when only branch/current-branch default exists)")
	}
	if pr.Type != "integer" {
		t.Errorf("pr property type = %q, want integer", pr.Type)
	}
	for _, want := range []string{"PR", "head branch", "precedence"} {
		if !strings.Contains(pr.Description, want) {
			t.Errorf("pr description missing %q (got: %s)", want, pr.Description)
		}
	}

	// Default behavior stays intact: 'branch' still optional, current-branch
	// default remains the documented fallback.
	br, ok := schema.Properties["branch"]
	if !ok {
		t.Fatal("schema missing 'branch' property (default behavior must be preserved)")
	}
	if !strings.Contains(br.Description, "current branch") {
		t.Errorf("branch description lost its current-branch default: %s", br.Description)
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
