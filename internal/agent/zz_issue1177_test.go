package agent

import (
	"strings"
	"testing"
)

// Regression tests for GitHub issue #1177.
//
// #1177: cross-turn intent-action conflicts were structurally invisible.
// Two holes combined to hide them:
//
//  1. conflictCat was only filled on the statement turn
//     (recordRAIntents). An intent whose conflicting action materialized
//     in a LATER turn kept conflictCat == raCatNone and was dropped
//     silently by escalateExpiredPending.
//  2. Pure-text statement turns (no categorized tool calls) hit the
//     len(actionCats) == 0 early return before recordRAIntents, so the
//     stated intent was never even registered as pending.
//
// The minimal fix: the guard now only requires statedCats, and
// resolveRAPending fills conflictCat/conflictTool for unfulfilled
// pendings when a categorically conflicting action appears.

// TestIssue1177_CrossTurnConflictEscalates drives the issue's exact shape:
// turn 1 states verify but only runs a deploy-category action (no recorded
// conflict); turn 2 introduces the conflicting fix action in a later turn;
// the window then closes with the cross-turn conflict recorded.
func TestIssue1177_CrossTurnConflictEscalates(t *testing.T) {
	s := newReasonActionState()
	// Turn 1: verify intent stated; deploy action, which is NOT a
	// categorical mismatch for verify, so conflictCat stays raCatNone.
	if hint := s.checkAlignment("Let me run the tests to verify the fix.",
		[]toolCallInfo{{Name: "git_add"}}); hint != "" {
		t.Fatalf("statement turn must not escalate: %s", hint)
	}
	// Turn 2: the conflicting fix action first appears in a later turn.
	// Issue #1177: this conflict must be recorded on the pending intent.
	if hint := s.checkAlignment("", []toolCallInfo{{Name: "edit_file"}}); hint != "" {
		t.Fatalf("conflict turn must not escalate immediately: %s", hint)
	}
	// Turn 3: filler inside the tolerance window.
	s.checkAlignment("", []toolCallInfo{{Name: "read_file"}})
	// Turn 4: window closed; the cross-turn conflict must escalate.
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

// TestIssue1177_PureTextStatementTurnRegistersPending pins hole 2: a
// statement turn with NO tool calls at all must still register the intent,
// so a conflicting action in a later turn can escalate.
func TestIssue1177_PureTextStatementTurnRegistersPending(t *testing.T) {
	s := newReasonActionState()
	// Turn 1: pure-text statement turn, no tool calls.
	if hint := s.checkAlignment("Let me verify the fix works.", nil); hint != "" {
		t.Fatalf("statement turn must not escalate: %s", hint)
	}
	if len(s.pending) != 1 {
		t.Fatalf("pure-text statement turn must register a pending intent, got %d", len(s.pending))
	}
	// Turn 2: conflicting fix action appears later.
	s.checkAlignment("", []toolCallInfo{{Name: "edit_file"}})
	// Turn 3: filler inside the window.
	s.checkAlignment("", []toolCallInfo{{Name: "read_file"}})
	// Turn 4: must escalate now that the cross-turn conflict is recorded.
	if hint := s.checkAlignment("", []toolCallInfo{{Name: "read_file"}}); hint == "" {
		t.Fatal("intent stated on a pure-text turn never escalated despite a later conflicting action")
	}
	if s.warnings != 1 {
		t.Fatalf("expected exactly 1 warning, got %d", s.warnings)
	}
}

// TestIssue1177_InWindowFulfillmentStillSilent guards against over-eager
// escalation: recording a cross-turn conflict must not break the #1162
// tolerance semantics - a genuinely fulfilled intent stays silent.
func TestIssue1177_InWindowFulfillmentStillSilent(t *testing.T) {
	s := newReasonActionState()
	turns := [][]toolCallInfo{
		{{Name: "git_add"}},     // verify stated, deploy action
		{{Name: "edit_file"}},   // cross-turn fix conflict recorded
		{{Name: "run_command"}}, // fulfilled inside the window
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

// TestIssue1177_NonConflictingCrossTurnStillSilent pins the boundary: only
// categorically mismatching later actions may arm an escalation. Later turns
// with unrelated (non-mismatch) categories keep conflictCat == raCatNone and
// the pending still drains silently at expiry, preserving #1162 semantics.
func TestIssue1177_NonConflictingCrossTurnStillSilent(t *testing.T) {
	s := newReasonActionState()
	// Turn 1: verify stated, deploy action (not a mismatch pair).
	s.checkAlignment("Let me run the tests to verify the fix.",
		[]toolCallInfo{{Name: "git_add"}})
	// Turns 2..4: only read/understand actions, never a mismatch for verify.
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
