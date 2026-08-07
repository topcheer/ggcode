package agent

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestReasonAction_AlignedVerify(t *testing.T) {
	s := newReasonActionState()
	// Stated: "let me verify" + actual: run_command => aligned
	hint := s.checkAlignment("Let me verify the fix by running tests.",
		[]toolCallInfo{{Name: "run_command"}})
	if hint != "" {
		t.Fatalf("expected no warning for aligned verify, got: %s", hint)
	}
}

func TestReasonAction_MismatchVerifyVsFix(t *testing.T) {
	s := newReasonActionState()
	// Stated: "let me verify" but actual: edit_file => categorical mismatch
	hint := s.checkAlignment("Let me verify the fix works.",
		[]toolCallInfo{{Name: "edit_file"}})
	if hint == "" {
		t.Fatal("expected warning for verify-stated but fix-action mismatch")
	}
	if !strings.Contains(hint, "verify") || !strings.Contains(hint, "edit_file") {
		t.Fatalf("warning should mention verify and edit_file, got: %s", hint)
	}
}

func TestReasonAction_MismatchUnderstandVsFix(t *testing.T) {
	s := newReasonActionState()
	// Stated: "let me understand" but actual: edit_file => mismatch
	hint := s.checkAlignment("Let me understand the data flow in this module.",
		[]toolCallInfo{{Name: "edit_file"}})
	if hint == "" {
		t.Fatal("expected warning for understand-stated but fix-action mismatch")
	}
}

func TestReasonAction_MismatchUnderstandVsDeploy(t *testing.T) {
	s := newReasonActionState()
	hint := s.checkAlignment("I need to understand the deployment setup.",
		[]toolCallInfo{{Name: "git_commit"}})
	if hint == "" {
		t.Fatal("expected warning for understand-stated but deploy-action mismatch")
	}
}

func TestReasonAction_MismatchVerifyVsSearch(t *testing.T) {
	s := newReasonActionState()
	hint := s.checkAlignment("Let me verify the test results.",
		[]toolCallInfo{{Name: "grep"}})
	if hint == "" {
		t.Fatal("expected warning for verify-stated but search-action mismatch")
	}
}

func TestReasonAction_AlignedUnderstand(t *testing.T) {
	s := newReasonActionState()
	// Stated: "let me understand" + actual: read_file => aligned
	hint := s.checkAlignment("Let me understand how auth works.",
		[]toolCallInfo{{Name: "read_file"}})
	if hint != "" {
		t.Fatalf("expected no warning for aligned understand, got: %s", hint)
	}
}

func TestReasonAction_AlignedFix(t *testing.T) {
	s := newReasonActionState()
	hint := s.checkAlignment("Let me fix the bug in auth.go.",
		[]toolCallInfo{{Name: "edit_file"}})
	if hint != "" {
		t.Fatalf("expected no warning for aligned fix, got: %s", hint)
	}
}

func TestReasonAction_NoIntent(t *testing.T) {
	s := newReasonActionState()
	// No intent phrase in text
	hint := s.checkAlignment("The auth module looks good.",
		[]toolCallInfo{{Name: "edit_file"}})
	if hint != "" {
		t.Fatalf("expected no warning when no intent stated, got: %s", hint)
	}
}

func TestReasonAction_NoToolCalls(t *testing.T) {
	s := newReasonActionState()
	hint := s.checkAlignment("Let me verify the tests.", nil)
	if hint != "" {
		t.Fatalf("expected no warning when no tool calls, got: %s", hint)
	}
}

func TestReasonAction_MaxWarnings(t *testing.T) {
	s := newReasonActionState()
	// First mismatch
	hint1 := s.checkAlignment("Let me verify the fix.",
		[]toolCallInfo{{Name: "edit_file"}})
	if hint1 == "" {
		t.Fatal("expected first warning")
	}
	// Second mismatch
	hint2 := s.checkAlignment("Let me verify again.",
		[]toolCallInfo{{Name: "write_file"}})
	if hint2 == "" {
		t.Fatal("expected second warning")
	}
	// Third should be suppressed
	hint3 := s.checkAlignment("Let me verify once more.",
		[]toolCallInfo{{Name: "edit_file"}})
	if hint3 != "" {
		t.Fatal("expected third warning to be suppressed")
	}
}

func TestReasonAction_Reset(t *testing.T) {
	s := newReasonActionState()
	_ = s.checkAlignment("Let me verify the fix.",
		[]toolCallInfo{{Name: "edit_file"}})
	if s.warnings != 1 {
		t.Fatalf("expected 1 warning, got %d", s.warnings)
	}
	s.reset()
	if s.warnings != 0 {
		t.Fatalf("expected 0 warnings after reset, got %d", s.warnings)
	}
	if len(s.mismatches) != 0 {
		t.Fatalf("expected 0 mismatches after reset, got %d", len(s.mismatches))
	}
}

func TestReasonAction_MaybeWarnReasonAction(t *testing.T) {
	a := &Agent{reasonAction: newReasonActionState()}
	hint := a.maybeWarnReasonAction("Let me verify the build passes.",
		[]provider.ToolCallDelta{{Name: "edit_file"}})
	if hint == "" {
		t.Fatal("expected alignment warning from agent method")
	}
}

func TestReasonAction_NilState(t *testing.T) {
	a := &Agent{reasonAction: nil}
	hint := a.maybeWarnReasonAction("Let me verify.",
		[]provider.ToolCallDelta{{Name: "edit_file"}})
	if hint != "" {
		t.Fatal("expected no warning when reasonAction is nil")
	}
}

func TestReasonAction_MultipleActionsPartialMatch(t *testing.T) {
	s := newReasonActionState()
	// Stated verify + fix, actual: edit_file + run_command => aligned (both present)
	hint := s.checkAlignment("Let me verify the fix and then update the code.",
		[]toolCallInfo{{Name: "run_command"}, {Name: "edit_file"}})
	if hint != "" {
		t.Fatalf("expected no warning for multi-intent with matching actions, got: %s", hint)
	}
}

func TestReasonAction_SearchToolNotMismatched(t *testing.T) {
	s := newReasonActionState()
	// Stated "let me search" + actual: grep => aligned
	hint := s.checkAlignment("Let me search for all usages.",
		[]toolCallInfo{{Name: "grep"}})
	if hint != "" {
		t.Fatalf("expected no warning for aligned search, got: %s", hint)
	}
}
