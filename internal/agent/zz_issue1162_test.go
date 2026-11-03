package agent

import "testing"

func TestIssue1162_SequentialPlanNotMisaligned(t *testing.T) {
	s := newReasonActionState()

	seq := []toolCallInfo{
		{Name: "grep"},
		{Name: "run_command"},
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

	text := "Let me fix the bug and then I'll verify with tests."
	for _, tc := range []toolCallInfo{
		{Name: "edit_file"},
		{Name: "write_file"},
	} {
		if hint := s.checkAlignment(text, []toolCallInfo{tc}); hint != "" {
			t.Fatalf("multi-intent plan wrongly escalated on %s: %s", tc.Name, hint)
		}
	}

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
