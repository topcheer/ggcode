package agent

import "testing"

// #5758: recordGreenBuild must reset the warning budget together with the
// distinct-file window. Before the fix, warningsIssued only reset per turn /
// on context compaction, so every risk window after the first green build
// stayed permanently silent (false negative).

func TestIssue5758_GreenBuildResetsWarningBudget(t *testing.T) {
	s := newEditPropagationState()

	for _, p := range []string{"/a.go", "/b.go", "/c.go", "/d.go", "/e.go", "/f.go", "/g.go"} {
		s.recordEdit("edit_file", `{"file_path":"`+p+`"}`)
	}
	if msg := s.maybeWarn(5); msg == "" {
		t.Fatal("final warning expected at 7 files in first window")
	}
	if msg := s.maybeWarn(6); msg != "" {
		t.Fatal("should stay silent after budget exhausted within same window")
	}

	s.recordGreenBuild()

	for _, p := range []string{"/h.go", "/i.go", "/j.go", "/k.go"} {
		s.recordEdit("edit_file", `{"file_path":"`+p+`"}`)
	}
	if msg := s.maybeWarn(7); msg == "" {
		t.Fatal("second risk window must warn again after green build (#5758 regression)")
	}
	if s.warningsIssued != 1 {
		t.Fatalf("expected warningsIssued=1 in second window, got %d", s.warningsIssued)
	}
}

func TestIssue5758_GreenBuildUnblocksEscalation(t *testing.T) {
	s := newEditPropagationState()

	for _, p := range []string{"/a.go", "/b.go", "/c.go", "/d.go"} {
		s.recordEdit("edit_file", `{"file_path":"`+p+`"}`)
	}
	if msg := s.maybeWarn(5); msg == "" {
		t.Fatal("first-tier warning expected at 4 files")
	}

	s.recordGreenBuild()

	// Upgrade asymmetry: with the old carried-over warningsIssued==1, a second
	// window of 4-6 files hit the else branch and stayed silent forever.
	for _, p := range []string{"/e.go", "/f.go", "/g.go", "/h.go"} {
		s.recordEdit("edit_file", `{"file_path":"`+p+`"}`)
	}
	if msg := s.maybeWarn(7); msg == "" {
		t.Fatal("second window at 4 files must re-warn after green build")
	}

	for _, p := range []string{"/i.go", "/j.go", "/k.go"} {
		s.recordEdit("edit_file", `{"file_path":"`+p+`"}`)
	}
	if msg := s.maybeWarn(8); msg == "" {
		t.Fatal("escalation to final warning expected at 7 files in second window")
	}
}

func TestIssue5758_AntiNagWithinWindowStillHolds(t *testing.T) {
	s := newEditPropagationState()

	for _, p := range []string{"/a.go", "/b.go", "/c.go", "/d.go", "/e.go", "/f.go", "/g.go"} {
		s.recordEdit("edit_file", `{"file_path":"`+p+`"}`)
	}
	if msg := s.maybeWarn(5); msg == "" {
		t.Fatal("final warning expected at 7 files")
	}
	s.recordEdit("edit_file", `{"file_path":"/h.go"}`)
	if msg := s.maybeWarn(6); msg != "" {
		t.Fatal("must stay silent within the same risk window (anti-nag cap)")
	}
	if s.warningsIssued != propagationMaxWarnings {
		t.Fatalf("expected budget cap %d within window, got %d", propagationMaxWarnings, s.warningsIssued)
	}
}
