package agent

import "testing"

func TestHmClassifyTool(t *testing.T) {
	tests := []struct {
		name     string
		tool     string
		expected hmToolCategory
	}{
		{"read_file", "read_file", hmCategoryRead},
		{"multi_file_read", "multi_file_read", hmCategoryRead},
		{"edit_file", "edit_file", hmCategoryWrite},
		{"write_file", "write_file", hmCategoryWrite},
		{"multi_file_edit", "multi_file_edit", hmCategoryWrite},
		{"web_search", "web_search", hmCategorySearch},
		{"code_search", "code_search", hmCategorySearch},
		{"lsp_definition", "lsp_definition", hmCategoryReasoning},
		{"lsp_references", "lsp_references", hmCategoryReasoning},
		{"run_command", "run_command", hmCategoryExecution},
		{"start_command", "start_command", hmCategoryExecution},
		{"git_add", "git_add", hmCategoryExecution},
		{"unknown_tool", "unknown_tool", hmCategoryOther},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := hmClassifyTool(tt.tool)
			if result != tt.expected {
				t.Errorf("hmClassifyTool(%q) = %v, want %v", tt.tool, result, tt.expected)
			}
		})
	}
}

func TestHeterogeneousModelGuide(t *testing.T) {
	state := newHeterogeneousModelState()

	// Initial state should have no guidance
	if state.guidance != "" {
		t.Errorf("initial state has guidance: %q", state.guidance)
	}

	// Add read/write operations (execution-heavy)
	for i := 0; i < 8; i++ {
		state.recordToolCall("read_file", 1)
	}
	state.recordToolCall("edit_file", 2)
	state.recordToolCall("grep", 3)

	// Should have issued guidance after hitting threshold
	guidance := state.guidance
	if guidance == "" {
		t.Error("expected guidance after execution-heavy pattern, got empty")
	}
	if !containsString(guidance, "FinOps Guidance") {
		t.Errorf("guidance missing expected prefix, got: %q", guidance)
	}

	// Subsequent calls should not re-issue guidance (max 1 per session)
	state.reset()
	for i := 0; i < 10; i++ {
		state.recordToolCall("read_file", 1)
	}
	// Reset the internal counters for testing
	state.warnsIssued = 0
	state.guidance = ""
	state.recordToolCall("edit_file", 2)

	// After reset, should fire again
	guidance = state.guidance
	if guidance == "" {
		t.Error("expected guidance after reset, got empty")
	}
}

func TestHeterogeneousModelReasoningHeavy(t *testing.T) {
	state := newHeterogeneousModelState()

	// Add LSP tools (reasoning-heavy) - should NOT trigger warning
	for i := 0; i < 10; i++ {
		state.recordToolCall("lsp_definition", 1)
		state.recordToolCall("lsp_references", 2)
	}

	// Reasoning-heavy patterns don't trigger FinOps guidance
	guidance := state.guidance
	if guidance != "" {
		t.Errorf("expected no guidance for reasoning-heavy pattern, got: %q", guidance)
	}
}

func TestHeterogeneousModelMinActions(t *testing.T) {
	state := newHeterogeneousModelState()

	// Below minimum threshold - should not trigger
	for i := 0; i < 3; i++ {
		state.recordToolCall("read_file", 1)
	}

	guidance := state.guidance
	if guidance != "" {
		t.Errorf("expected no guidance below minimum action threshold, got: %q", guidance)
	}
}

func containsString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
