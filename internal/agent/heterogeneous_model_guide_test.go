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

// TestHeterogeneousModelMixedExplorationThenExecution pins #2427: the
// documented sliding-window semantics. An early exploration phase (glob/
// todo calls -> hmCategoryOther, counted in a lifetime denominator but not
// in the exec numerator) used to permanently dilute a later pure-execution
// burst: 8 explore + 12 exec = 0.60 lifetime, below the 0.70 threshold
// forever, so the FinOps downgrade hint was silently dropped in exactly the
// Plan-and-Execute scenario the module cites as its flagship. The window of
// the last 10 calls is 100% exec by call 18, so guidance must fire.
func TestHeterogeneousModelMixedExplorationThenExecution(t *testing.T) {
	state := newHeterogeneousModelState()

	// Exploration phase: 8 non-exec, non-reasoning calls.
	for i := 0; i < 8; i++ {
		if g := state.recordToolCall("glob", 1); g != "" {
			t.Fatalf("unexpected guidance during exploration phase at call %d", i+1)
		}
	}

	// Execution burst: with a true 10-call window the last 10 calls are
	// all exec once 10 edit_file calls have accumulated (call 18 overall);
	// the old lifetime ratio (12/20 = 0.60) never reached 0.70.
	fired := false
	for i := 0; i < 12; i++ {
		if g := state.recordToolCall("edit_file", 2); g != "" {
			fired = true
			break
		}
	}
	if !fired {
		t.Error("expected sliding-window guidance during pure-execution burst after exploration phase; lifetime-ratio bug (#2427) would drop it")
	}
}

// TestHeterogeneousModelWindowTrimBoundary pins the #2427 off-by-one: the
// old trim-before-append (len > window) left an 11-entry steady-state
// window. After trimming post-append, len(toolHistory) must stay exactly
// hmLookbackWindow.
func TestHeterogeneousModelWindowTrimBoundary(t *testing.T) {
	state := newHeterogeneousModelState()

	for i := 0; i < 15; i++ {
		state.recordToolCall("read_file", 1)
	}
	if got := len(state.toolHistory); got != hmLookbackWindow {
		t.Errorf("toolHistory length = %d, want exactly %d (off-by-one in trim)", got, hmLookbackWindow)
	}
}
