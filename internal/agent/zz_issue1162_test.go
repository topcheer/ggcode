package agent

import "testing"

// Regression tests for GitHub issue #1162.
//
// The reasoning-action alignment verifier previously required every stated
// intent category to be matched by an action category on the SAME turn. Two
// systematic false positives followed:
//
//  1. Multi-intent two-step plans: "Let me fix the bug and then I'll verify
//     with tests." + edit_file flagged the verify intent because the fix step
//     ran first - a fully aligned plan opening move.
//  2. Sequential plans: "Let me verify the fix works. First, let me find
//     where the tests are." + grep flagged verify-vs-search even though the
//     verify action simply comes one turn later.
//
// This file pins, per the issue's fix direction:
//  1. sequential plans no longer misfire (temporal tolerance window +
//     coordination-marker exemption);
//  2. multi-intent statements are exempt from escalation entirely;
//  3. an intent fulfilled within raAlignmentWindow turns stays silent;
//  4. a genuinely abandoned intent (never acted on, single stated category)
//     still escalates after the window closes.

func TestIssue1162_SequentialPlanNotMisaligned(t *testing.T) {
	s := newReasonActionState()
	// Exactly the issue-#1162 false positive: verify intent planned AFTER a
	// preparatory search; only grep runs on the statement turn, run_command
	// (verify) arrives two turns later.
	seq := []toolCallInfo{
		{Name: "grep"},        // find tests first
		{Name: "run_command"}, // then run them to verify
	}
	for i, tc := range seq {
		hint := s.checkAlignment(
			"Let me verify the fix works. First, let me find where the tests are.",
			[]toolCallInfo{tc})
		if hint != "" {
			t.Fatalf("turn %d (%s): sequential plan wrongly flagged: %s", i+1, tc.Name, hint)
		}
	}
	if s.warnings != 0 {
		t.Fatalf("sequential plan produced %d warnings, want 0", s.warnings)
	}
}

func TestIssue1162_TwoStepPlanMultiIntentExempt(t *testing.T) {
	s := newReasonActionState()
	// Stated categories {fix, verify}; only fix executes on this turn and the
	// agent keeps editing afterwards. The multi-intent exemption (#1162) must
	// prevent ANY escalation across and beyond the window.
	text := "Let me fix the bug and then I'll verify with tests."
	for _, tc := range []toolCallInfo{
		{Name: "edit_file"},
		{Name: "write_file"},
	} {
		if hint := s.checkAlignment(text, []toolCallInfo{tc}); hint != "" {
			t.Fatalf("multi-intent plan wrongly escalated on %s: %s", tc.Name, hint)
		}
	}
	// Keep working past the window on turns whose prose states NO new intent,
	// so the original exempt pending drains silently (#1162) instead of being
	// re-created by restated plans.
	for i := 0; i < raAlignmentWindow+2; i++ {
		if hint := s.checkAlignment("Still working through the module now.",
			[]toolCallInfo{{Name: "edit_file"}}); hint != "" {
			t.Fatalf("exempt multi-intent escalated on drain turn %d: %s", i+1, hint)
		}
	}
	if s.warnings != 0 {
		t.Fatalf("multi-intent plan produced %d warnings, want 0", s.warnings)
	}
	if len(s.pending) != 0 {
		t.Fatalf("exempt intents should drop silently, %d still pending", len(s.pending))
	}
}

func TestIssue1162_IntentFulfilledWithinWindowStaysSilent(t *testing.T) {
	s := newReasonActionState()
	// Single category, NO coordination marker: verify-stated against a fix
	// action today but the verification genuinely runs on turn 3, inside the
	// raAlignmentWindow tolerance. No warning may fire at any point (#1162).
	turns := [][]toolCallInfo{
		{{Name: "edit_file"}},
		{{Name: "read_file"}},
		{{Name: "run_command"}},
	}
	for i, tcs := range turns {
		if hint := s.checkAlignment("I should verify the results.", tcs); hint != "" {
			t.Fatalf("turn %d: in-window fulfillment must not warn: %s", i+1, hint)
		}
	}
	if s.warnings != 0 {
		t.Fatalf("in-window fulfillment produced %d warnings, want 0", s.warnings)
	}
}

func TestIssue1162_StaleSingleIntentStillEscalates(t *testing.T) {
	s := newReasonActionState()
	// True misalignment remains detectable: ONE strictly-stated verify intent,
	// no sequencing language, no matching action for the whole window plus a
	// margin. The warning names the stored intent phrase and conflicting tool.
	text := "I should verify the coverage numbers now."
	var hint string
	for i := 0; i < raAlignmentWindow+2 && hint == ""; i++ {
		hint = s.checkAlignment(text, []toolCallInfo{{Name: "edit_file"}})
	}
	if hint == "" {
		t.Fatal("expected escalation after tolerance window for never-executed intent")
	}
	want := "[Reasoning-Action Alignment]"
	if got := len(hint); got < len(want) || hint[:len(want)] != want {
		t.Fatalf("warning lost its prefix, got: %s", hint)
	}
	for _, sub := range []string{"verify", "edit_file"} {
		if !containsSub(hint, sub) {
			t.Fatalf("warning missing %q, got: %s", sub, hint)
		}
	}
	if s.warnings != 1 {
		t.Fatalf("expected exactly 1 warning, got %d", s.warnings)
	}
	if len(s.pending) != 0 {
		t.Fatalf("escalated intent should be consumed, %d pending remain", len(s.pending))
	}
}

func containsSub(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

func TestIssue1162_WindowLengthMatchesIssueDirection(t *testing.T) {
	// Pin the tolerance threshold taken from the issue's fix direction so it
	// survives accidental edits (#1162): stale intents escalate on the first
	// check following the window close, not earlier.
	s := newReasonActionState()
	text := "I should verify the schema migration results."
	firstConflict := s.checkAlignment(text, []toolCallInfo{{Name: "edit_file"}})
	if firstConflict != "" {
		t.Fatal("statement turn itself must not escalate")
	}
	for i := 2; i <= raAlignmentWindow; i++ {
		if hint := s.checkAlignment(text, []toolCallInfo{{Name: "edit_file"}}); hint != "" {
			t.Fatalf("turn %d inside window must stay silent, got: %s", i, hint)
		}
	}
	fired := ""
	for i := 0; fired == "" && i < 3; i++ {
		fired = s.checkAlignment(text, []toolCallInfo{{Name: "edit_file"}})
	}
	if fired == "" {
		t.Fatal("window-close escalation never fired")
	}
}
