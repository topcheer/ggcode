package agent

import (
	"strings"
	"testing"
)

func TestScanActionHedging(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		wantMin  int
		wantHigh int
	}{
		{
			name:    "empty text",
			text:    "",
			wantMin: 0,
		},
		{
			name:    "no hedging",
			text:    "I'll read the file and then make the edit based on what I found.",
			wantMin: 0,
		},
		{
			name:     "single hopefully fix",
			text:     "This should hopefully fix the issue with the parser.",
			wantMin:  1,
			wantHigh: 1,
		},
		{
			name:     "let's try",
			text:     "Let's try this approach and see if it works.",
			wantMin:  1,
			wantHigh: 1,
		},
		{
			name:     "best guess",
			text:     "This is my best guess at the fix.",
			wantMin:  1,
			wantHigh: 1,
		},
		{
			name:     "multiple hedging signals",
			text:     "This should hopefully fix the bug. Let's try it. If this doesn't work, we can try something else.",
			wantMin:  3,
			wantHigh: 3,
		},
		{
			name:     "not sure but proceeding",
			text:     "I'm not entirely sure this is the right approach but I'll proceed with the edit.",
			wantMin:  1,
			wantHigh: 1,
		},
		{
			name:    "medium hedging I think this should",
			text:    "I think this should work for the test case.",
			wantMin: 1,
		},
		{
			name:    "see if this works",
			text:    "Let me apply this patch and see if this works correctly.",
			wantMin: 1,
		},
		{
			name:    "fingers crossed",
			text:    "Applying the fix now, fingers crossed this resolves it.",
			wantMin: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hits := scanActionHedging(tt.text)
			if len(hits) < tt.wantMin {
				t.Errorf("expected at least %d hits, got %d: %+v", tt.wantMin, len(hits), hits)
			}
			if tt.wantHigh > 0 {
				highCount := 0
				for _, h := range hits {
					if h.level == "HIGH" {
						highCount++
					}
				}
				if highCount < tt.wantHigh {
					t.Errorf("expected at least %d HIGH hits, got %d", tt.wantHigh, highCount)
				}
			}
		})
	}
}

func TestMaybeWarnActionHedging(t *testing.T) {
	a := &Agent{
		actionHedging: newActionHedgingState(),
	}

	// No mutation - should not fire even with hedging language
	hint := a.maybeWarnActionHedging("This should hopefully fix it. Let's try.", false)
	if hint != "" {
		t.Error("should not fire without mutation")
	}

	// Single hedging signal - below threshold
	hint = a.maybeWarnActionHedging("This should hopefully fix the bug.", true)
	if hint != "" {
		t.Error("should not fire for single signal (below threshold of 2)")
	}

	// Multiple hedging signals with mutation
	hint = a.maybeWarnActionHedging("This should hopefully fix it. Let's try. If this doesn't work we'll try something else.", true)
	if hint == "" {
		t.Fatal("should fire for 3 hedging signals with mutation")
	}
	if !strings.Contains(hint, "action-hedging") {
		t.Errorf("hint should contain action-hedging tag: %s", hint)
	}
	if !strings.Contains(hint, "verbalized uncertainty") {
		t.Errorf("hint should explain the issue: %s", hint)
	}

	// Second warning should fire
	hint = a.maybeWarnActionHedging("This is my best guess. Let's try this approach.", true)
	if hint == "" {
		t.Fatal("second warning should fire")
	}

	// Third warning should not fire (max 2)
	hint = a.maybeWarnActionHedging("This should hopefully fix it. Let's try again.", true)
	if hint != "" {
		t.Error("should not fire after max warnings reached")
	}
}

func TestActionHedgingReset(t *testing.T) {
	s := newActionHedgingState()
	s.warnings = 2
	s.reset()
	if s.warnings != 0 {
		t.Errorf("expected warnings=0 after reset, got %d", s.warnings)
	}
}

func TestIsMutationTool(t *testing.T) {
	mutationTools := []string{
		"edit_file", "write_file", "multi_edit_file", "multi_file_edit",
		"batch_replace", "git_commit", "git_add", "git_reset",
		"git_revert", "git_checkout", "git_stash", "notebook_edit",
	}
	for _, tool := range mutationTools {
		if !isMutationTool(tool) {
			t.Errorf("expected %s to be a mutation tool", tool)
		}
	}

	nonMutationTools := []string{
		"read_file", "grep", "search_files", "run_command",
		"list_directory", "glob", "web_search", "lsp_definition",
	}
	for _, tool := range nonMutationTools {
		if isMutationTool(tool) {
			t.Errorf("expected %s to NOT be a mutation tool", tool)
		}
	}
}

func TestActionHedgingDedup(t *testing.T) {
	// Same pattern repeated should be deduplicated
	hits := scanActionHedging("This should hopefully fix it. This should hopefully fix it again.")
	// Should only get 2 unique excerpts, not 4 of the same
	keys := make(map[string]bool)
	for _, h := range hits {
		key := h.level + ":" + h.excerpt
		keys[key] = true
	}
	if len(keys) != len(hits) {
		t.Errorf("dedup failed: %d unique keys for %d hits", len(keys), len(hits))
	}
}
