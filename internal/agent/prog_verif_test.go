package agent

import (
	"testing"
)

func TestBuildTestFailureGuidance(t *testing.T) {
	tests := []struct {
		name     string
		result   string
		contains string
	}{
		{
			name:     "undefined symbol",
			result:   "build failed: undefined: MyFunction",
			contains: "undefined symbol",
		},
		{
			name:     "type mismatch",
			result:   "cannot use x (type int) as type string",
			contains: "Type mismatch",
		},
		{
			name:     "file not found",
			result:   "no such file or directory: missing.go",
			contains: "File or package not found",
		},
		{
			name:     "test assertion",
			result:   "expected 5, got 3",
			contains: "Test assertion",
		},
		{
			name:     "generic error",
			result:   "some random build error",
			contains: "Review the error output",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			guidance := buildTestFailureGuidance(tt.result)
			if guidance == "" {
				t.Fatal("buildTestFailureGuidance returned empty string")
			}
			if !containsSubstring(guidance, tt.contains) {
				t.Errorf("guidance %q does not contain expected substring %q", guidance, tt.contains)
			}
		})
	}
}

func TestProgVerifTracker(t *testing.T) {
	tracker := &progVerifTracker{}

	// Test reset
	tracker.fires = 5
	tracker.reset()
	if tracker.fires != 0 {
		t.Errorf("reset failed: fires = %d, want 0", tracker.fires)
	}

	// Test build/test failure guidance
	guidance := tracker.checkAfterToolResult("run_command", "build failed: undefined: Foo", true)
	if guidance == "" {
		t.Error("expected guidance for build error, got empty")
	}
	if !containsSubstring(guidance, "Progressive Verification") {
		t.Error("guidance should include 'Progressive Verification' prefix")
	}
	if tracker.fires != 1 {
		t.Errorf("expected 1 fire, got %d", tracker.fires)
	}

	// Test max warnings limit
	tracker.reset()
	for i := 0; i < 10; i++ {
		tracker.checkAfterToolResult("run_command", "error", true)
	}
	if tracker.fires > maxProgVerifWarnings {
		t.Errorf("expected max %d warnings, got %d", maxProgVerifWarnings, tracker.fires)
	}

	// Test edit without verification pattern
	tracker.reset()
	_ = tracker.checkAfterToolResult("edit_file", "success", false)
	if tracker.fires != 0 {
		t.Error("first edit should not trigger warning")
	}
	guidance = tracker.checkAfterToolResult("edit_file", "another edit", false)
	if guidance == "" {
		t.Error("second edit without verify should trigger warning")
	}
}

func containsSubstring(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && findSubstring(s, substr))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
