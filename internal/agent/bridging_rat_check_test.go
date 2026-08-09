package agent

import (
	"strings"
	"testing"
)

func newTestBridgingRat() *bridgingRatState {
	return newBridgingRatState()
}

func TestBridgingRat_NoContradictionNoHint(t *testing.T) {
	s := newTestBridgingRat()
	// No contradiction recorded, so rationalization text should not trigger.
	hint := s.checkRationalization("the environment may have changed since my last read", 2)
	if hint != "" {
		t.Fatalf("expected empty hint with no contradiction, got: %s", hint)
	}
}

func TestBridgingRat_ErrorContradictionThenRationalization(t *testing.T) {
	s := newTestBridgingRat()
	// Record a contradiction (error in tool result).
	s.recordToolResult("run_command", "BUILD FAILURE: undefined: foo", 1, false)

	// Assistant rationalizes the contradiction.
	hint := s.checkRationalization("The environment may have changed since I last read this file.", 2)
	if hint == "" {
		t.Fatal("expected bridging rationalization hint after error contradiction")
	}
	if !strings.Contains(hint, "Bridging Rationalization") {
		t.Fatalf("hint should contain category label, got: %s", hint)
	}
}

func TestBridgingRat_NotFoundContradictionThenRationalization(t *testing.T) {
	s := newTestBridgingRat()
	s.recordToolResult("grep", "no matches found", 1, false)

	hint := s.checkRationalization("this discrepancy is likely due to a stale cache", 2)
	if hint == "" {
		t.Fatal("expected hint after not_found contradiction + stale cache rationalization")
	}
}

func TestBridgingRat_MismatchContradictionThenRationalization(t *testing.T) {
	s := newTestBridgingRat()
	s.recordToolResult("run_command", "expected 5 got 3, assertion failed", 1, false)

	hint := s.checkRationalization("this is probably a timing issue with the test runner", 2)
	if hint == "" {
		t.Fatal("expected hint after mismatch contradiction + timing rationalization")
	}
}

func TestBridgingRat_RationalizationWithoutContradictionGap(t *testing.T) {
	s := newTestBridgingRat()
	// Contradiction too far in the past (iteration 1, checking at iteration 10).
	s.recordToolResult("run_command", "BUILD FAILURE", 1, false)
	hint := s.checkRationalization("the environment may have changed", 10)
	if hint != "" {
		t.Fatal("should not trigger for stale contradiction (>3 iterations ago)")
	}
}

func TestBridgingRat_NoRationalizationText(t *testing.T) {
	s := newTestBridgingRat()
	s.recordToolResult("run_command", "BUILD FAILURE", 1, false)

	// Normal response that doesn't rationalize.
	hint := s.checkRationalization("The build failed because foo is undefined. Let me fix it.", 2)
	if hint != "" {
		t.Fatalf("should not trigger for non-rationalization text, got: %s", hint)
	}
}

func TestBridgingRat_MaxWarningsCap(t *testing.T) {
	s := newTestBridgingRat()
	// First fire.
	s.recordToolResult("run_command", "BUILD FAILURE", 1, false)
	h1 := s.checkRationalization("the environment may have changed", 2)
	if h1 == "" {
		t.Fatal("expected first hint")
	}
	// Second fire.
	s.recordToolResult("run_command", "FAIL: test_bar", 3, false)
	h2 := s.checkRationalization("this is probably a stale build artifact", 4)
	if h2 == "" {
		t.Fatal("expected second hint")
	}
	// Third fire should be capped.
	s.recordToolResult("run_command", "panic: nil pointer", 5, false)
	h3 := s.checkRationalization("this could be a transient race condition", 6)
	if h3 != "" {
		t.Fatalf("expected third hint to be capped, got: %s", h3)
	}
}

func TestBridgingRat_Reset(t *testing.T) {
	s := newTestBridgingRat()
	s.recordToolResult("run_command", "BUILD FAILURE", 1, false)
	_ = s.checkRationalization("the environment may have changed", 2)
	s.reset()
	if s.warnings != 0 || len(s.contradictions) != 0 {
		t.Fatal("reset should clear warnings and contradictions")
	}
}

func TestBridgingRat_IsErrorFlag(t *testing.T) {
	s := newTestBridgingRat()
	// When IsError=true, it should record regardless of content.
	s.recordToolResult("edit_file", "", 1, true)
	hint := s.checkRationalization("this is expected, the environment must have changed", 2)
	if hint == "" {
		t.Fatal("expected hint when error flag is true + rationalization text")
	}
}

func TestBridgingRat_VariousRationalizationPatterns(t *testing.T) {
	tests := []struct {
		name string
		text string
		want bool
	}{
		{"env_changed", "the environment may have changed", true},
		{"filesystem_changed", "the filesystem must have changed", true},
		{"stale_cache", "this is probably a stale cache issue", true},
		{"cached_build", "likely a cached build artifact", true},
		{"this_is_expected", "this is expected behavior given the circumstances", true},
		{"timing_issue", "might be a timing issue", true},
		{"out_of_sync", "the files are out of sync", true},
		{"discrepancy_due_to", "this discrepancy is due to version differences", true},
		{"external_modification", "the file may have been overwritten externally", true},
		{"normal_text", "let me re-read the file to check", false},
		{"fix_oriented", "I'll fix the undefined variable", false},
		{"honest_acknowledgment", "the test failed, let me investigate", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestBridgingRat()
			s.recordToolResult("run_command", "BUILD FAILURE", 1, false)
			hint := s.checkRationalization(tt.text, 2)
			if tt.want && hint == "" {
				t.Errorf("expected hint for %q but got none", tt.text)
			}
			if !tt.want && hint != "" {
				t.Errorf("did not expect hint for %q but got: %s", tt.text, hint)
			}
		})
	}
}

func TestBridgingRat_SameIterationContradictionIgnored(t *testing.T) {
	s := newTestBridgingRat()
	// Contradiction in same iteration as the check (gap=0).
	s.recordToolResult("run_command", "BUILD FAILURE", 2, false)
	hint := s.checkRationalization("the environment may have changed", 2)
	if hint != "" {
		t.Fatal("should not trigger for same-iteration contradiction (gap=0)")
	}
}
