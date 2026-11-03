package agent

import (
	"strings"
	"testing"
)

func TestIssue1177_CrossTurnConflictEscalates(t *testing.T) {
	s := newReasonActionState()

	if hint := s.checkAlignment("Let me run the tests to verify the fix.",
		[]toolCallInfo{{Name: "git_add"}}); hint != "" {
		t.Fatalf("statement turn must not escalate: %s", hint)
	}

	if hint := s.checkAlignment("", []toolCallInfo{{Name: "edit_file"}}); hint != "" {
		t.Fatalf("conflict turn must not escalate immediately: %s", hint)
	}

	s.checkAlignment("", []toolCallInfo{{Name: "read_file"}})

	hint := s.checkAlignment("", []toolCallInfo{{Name: "read_file"}})
	if hint == "" {
		t.Fatal("cross-turn conflict (verify stated, fix materialized later) never escalated")
	}
	if !strings.Contains(hint, "verify") || !strings.Contains(hint, "edit_file") {
		t.Fatalf("warning should name the stated intent and the cross-turn conflicting tool, got: %s", hint)
	}
	if s.warnings != 1 {
		t.Fatalf("expected exactly 1 warning, got %d", s.warnings)
	}
}

func TestIssue1177_PureTextStatementTurnRegistersPending(t *testing.T) {
	s := newReasonActionState()

	if hint := s.checkAlignment("Let me verify the fix works.", nil); hint != "" {
		t.Fatalf("statement turn must not escalate: %s", hint)
	}
	if len(s.pending) != 1 {
		t.Fatalf("pure-text statement turn must register a pending intent, got %d", len(s.pending))
	}

	s.checkAlignment("", []toolCallInfo{{Name: "edit_file"}})

	s.checkAlignment("", []toolCallInfo{{Name: "read_file"}})

	if hint := s.checkAlignment("", []toolCallInfo{{Name: "read_file"}}); hint == "" {
		t.Fatal("intent stated on a pure-text turn never escalated despite a later conflicting action")
	}
	if s.warnings != 1 {
		t.Fatalf("expected exactly 1 warning, got %d", s.warnings)
	}
}

func TestIssue1177_InWindowFulfillmentStillSilent(t *testing.T) {
	s := newReasonActionState()
	turns := [][]toolCallInfo{
		{{Name: "git_add"}},
		{{Name: "edit_file"}},
		{{Name: "run_command"}},
	}
	for i, tcs := range turns {
		if hint := s.checkAlignment("I should verify the results.", tcs); hint != "" {
			t.Fatalf("turn %d: in-window fulfillment must stay silent: %s", i+1, hint)
		}
	}
	if s.warnings != 0 {
		t.Fatalf("fulfilled intent produced %d warnings, want 0", s.warnings)
	}
	if len(s.pending) != 0 {
		t.Fatalf("fulfilled intent should resolve silently, %d still pending", len(s.pending))
	}
}

func TestIssue1177_NonConflictingCrossTurnStillSilent(t *testing.T) {
	s := newReasonActionState()

	s.checkAlignment("Let me run the tests to verify the fix.",
		[]toolCallInfo{{Name: "git_add"}})

	for i := 0; i < raAlignmentWindow+1; i++ {
		if hint := s.checkAlignment("", []toolCallInfo{{Name: "read_file"}}); hint != "" {
			t.Fatalf("turn %d: non-conflicting cross-turn actions must stay silent: %s", i+2, hint)
		}
	}
	if s.warnings != 0 {
		t.Fatalf("non-conflicting cross-turn actions produced %d warnings, want 0", s.warnings)
	}
	if len(s.pending) != 0 {
		t.Fatalf("intent without conflict should drain silently, %d still pending", len(s.pending))
	}
}
