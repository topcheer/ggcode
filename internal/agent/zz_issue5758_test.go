package agent

import "testing"

// #5758: recordGreenBuild must reset warningsIssued alongside distinctFiles.
// distinctFiles has green-build window semantics ("since the last successful
// build"), but warningsIssued was turn-scoped: once the two-warning budget was
// spent, every later risk window in the same turn stayed silent. These tests
// use only pre-fix symbols so they fail behaviorally at the parent commit.

func recordIssue5758Edits(s *editPropagationState, names ...string) {
	for _, n := range names {
		s.recordEdit("edit_file", `{"file_path":"/`+n+`.go"}`)
	}
}

func TestIssue5758GreenBuildResetsWarningsIssued(t *testing.T) {
	s := newEditPropagationState()

	// First risk window: 4 distinct files -> first warning (budget spent 1/2).
	recordIssue5758Edits(s, "a", "b", "c", "d")
	if msg := s.maybeWarn(5); msg == "" {
		t.Fatal("expected first warning at 4 files")
	}
	if s.warningsIssued != 1 {
		t.Fatalf("expected warningsIssued=1 after first warning, got %d", s.warningsIssued)
	}

	s.recordGreenBuild()

	if len(s.distinctFiles) != 0 {
		t.Fatalf("expected distinctFiles cleared on green build, got %d", len(s.distinctFiles))
	}
	if s.warningsIssued != 0 {
		t.Fatalf("#5758: expected warningsIssued reset to 0 on green build, got %d", s.warningsIssued)
	}
}

func TestIssue5758SecondWindowWarnsAgain(t *testing.T) {
	s := newEditPropagationState()

	// Window 1: first warning issued.
	recordIssue5758Edits(s, "a", "b", "c", "d")
	if msg := s.maybeWarn(5); msg == "" {
		t.Fatal("expected first warning in window 1")
	}

	s.recordGreenBuild()

	// Window 2 (the bug): 4 NEW unverified files after a green build must
	// re-trigger the first-tier warning, not stay silent for the rest of the turn.
	recordIssue5758Edits(s, "e", "f", "g", "h")
	if msg := s.maybeWarn(6); msg == "" {
		t.Fatal("#5758: expected warning to fire again in the second risk window after a green build")
	}
}

func TestIssue5758EscalationLadderPerWindow(t *testing.T) {
	s := newEditPropagationState()

	// Window 1: first warning at 4 files.
	recordIssue5758Edits(s, "a", "b", "c", "d")
	if msg := s.maybeWarn(5); msg == "" {
		t.Fatal("expected first warning in window 1")
	}

	s.recordGreenBuild()

	// Window 2: 4-6 files must re-warn at first tier (asymmetric ladder fix:
	// pre-fix this window could never reach first-tier again).
	recordIssue5758Edits(s, "e", "f", "g", "h")
	first := s.maybeWarn(6)
	if first == "" {
		t.Fatal("#5758: expected first-tier warning in window 2 at 4 files")
	}

	// Escalate within window 2 to 7 files -> final warning.
	recordIssue5758Edits(s, "i", "j", "k")
	final := s.maybeWarn(7)
	if final == "" {
		t.Fatal("expected escalation warning in window 2 at 7 files")
	}

	// Budget exhausted within window 2 until the next green build.
	recordIssue5758Edits(s, "l", "m", "n", "o")
	if msg := s.maybeWarn(8); msg != "" {
		t.Fatal("should not exceed max warnings within one window")
	}

	s.recordGreenBuild()

	// Window 3: detector must be armed again.
	recordIssue5758Edits(s, "p", "q", "r", "s")
	if msg := s.maybeWarn(9); msg == "" {
		t.Fatal("#5758: expected warning in the third risk window after a green build")
	}
}
