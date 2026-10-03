package agent

// Tests for the r405 attribution experiment state machine (Dov-inspired
// intervention-driven validation of causal attributions).

import (
	"strings"
	"testing"
)

const (
	r405Suspect = "internal/tool/grep.go"
	r405Verify  = "go test ./internal/tool/"
)

func r405Arm(t *testing.T) *attributionExperimentState {
	t.Helper()
	s := newAttributionExperimentState()
	if g := s.arm(r405Suspect, r405Verify); g == "" {
		t.Fatal("first arm should inject the experiment protocol")
	} else if !strings.Contains(g, "git stash push") || !strings.Contains(g, r405Verify) {
		t.Fatalf("protocol guidance missing stash/rerun elements: %q", g)
	}
	return s
}

func TestR405_ArmDuplicateIsSilent(t *testing.T) {
	s := r405Arm(t)
	if g := s.arm(r405Suspect, r405Verify); g != "" {
		t.Fatalf("same hypothesis re-arm must not repeat nagging, got %q", g)
	}
}

func TestR405_InterventionThenRerunPass_Confirmed(t *testing.T) {
	s := r405Arm(t)
	if g := s.observeCommand("run_command", "git stash push -- "+r405Suspect, false, ""); g != "" {
		t.Fatalf("intervention itself must be silent, got %q", g)
	}
	g := s.observeCommand("run_command", r405Verify, false, "ok  all tests passed")
	if !strings.Contains(g, "CONFIRMED") || !strings.Contains(g, "root cause") {
		t.Fatalf("expected CONFIRMED verdict, got %q", g)
	}
	if !strings.Contains(g, "git stash pop") {
		t.Fatalf("verdict must carry restore reminder when no pop observed, got %q", g)
	}
	if g2 := s.observeCommand("run_command", r405Verify, false, "ok"); g2 != "" {
		t.Fatalf("concluded experiment must stay silent, got %q", g2)
	}
}

func TestR405_InterventionThenRerunFail_Refuted(t *testing.T) {
	s := r405Arm(t)
	s.observeCommand("run_command", "git stash push -- "+r405Suspect, false, "")
	g := s.observeCommand("run_command", r405Verify, true, "--- FAIL: TestX")
	if !strings.Contains(g, "REFUTED") || !strings.Contains(g, "elsewhere") {
		t.Fatalf("expected REFUTED verdict steering elsewhere, got %q", g)
	}
}

func TestR405_RerunWithoutIntervention_NoVerdict(t *testing.T) {
	s := r405Arm(t)
	if g := s.observeCommand("run_command", r405Verify, true, "--- FAIL: TestX"); g != "" {
		t.Fatalf("rerun without prior intervention must not conclude, got %q", g)
	}
}

// #3150 V1: undo_edit carries no path signal in its args, so it can no
// longer count as an intervention. Missing the real undo only leaves the
// experiment inconclusive; counting it blindly risked a false CONFIRMED
// when the undo actually reverted an unrelated edit.
func TestR405_UndoEditNoLongerCountsAsIntervention(t *testing.T) {
	s := r405Arm(t)
	if g := s.observeCommand("undo_edit", `{"checkpoint_id":"cp1"}`, false, "reverted"); g != "" {
		t.Fatalf("undo_edit observation must be silent, got %q", g)
	}
	if g := s.observeCommand("run_command", r405Verify+" 2>&1", false, "ok"); g != "" {
		t.Fatalf("undo_edit must not mark intervened; verify pass should stay silent, got %q", g)
	}
	// The experiment stays armed: a later suspect-scoped revert still concludes.
	s.observeCommand("run_command", "git restore "+r405Suspect, false, "")
	if g := s.observeCommand("run_command", r405Verify, false, "ok"); !strings.Contains(g, "CONFIRMED") {
		t.Fatalf("suspect-scoped restore after non-intervention should conclude, got %q", g)
	}
}

// #3150 V1 regression: reverting an UNRELATED file must not mark the
// experiment intervened; the subsequent passing rerun must NOT emit a
// false CONFIRMED pointing at the suspect.
func TestR405_UnrelatedRevertIsNotIntervention(t *testing.T) {
	s := r405Arm(t)
	for _, cmd := range []string{
		"git restore internal/other/y.go",
		"git checkout -- internal/other/y.go",
		"git stash push -- internal/other/y.go",
		"git stash push -m wip -- internal/other/y.go",
	} {
		s2 := r405Arm(t)
		if g := s2.observeCommand("run_command", cmd, false, ""); g != "" {
			t.Fatalf("%s observation must be silent, got %q", cmd, g)
		}
		if g := s2.observeCommand("run_command", r405Verify, false, "ok"); g != "" {
			t.Fatalf("%s must not mark intervened (no verdict on rerun), got %q", cmd, g)
		}
	}
	_ = s
}

// #3150 V1 positive path: suspect-scoped reverts (full path, basename,
// bare stash) still conclude correctly.
func TestR405_SuspectScopedRevertsStillIntervene(t *testing.T) {
	for _, cmd := range []string{
		"git restore " + r405Suspect,
		"git checkout -- " + r405Suspect,
		"git stash push -- " + r405Suspect,
		"git stash push -m wip -- " + r405Suspect,
		"git restore grep.go", // basename form (cd-prefixed shell)
		"git stash",           // bare stash: stashes everything
	} {
		s2 := r405Arm(t)
		if g := s2.observeCommand("run_command", cmd, false, ""); g != "" {
			t.Fatalf("%s observation must be silent, got %q", cmd, g)
		}
		if g := s2.observeCommand("run_command", r405Verify, false, "ok"); !strings.Contains(g, "CONFIRMED") {
			t.Fatalf("%s should count as intervention (CONFIRMED on rerun), got %q", cmd, g)
		}
	}
}

func TestR405_PopSilencesRestoreReminder(t *testing.T) {
	s := r405Arm(t)
	s.observeCommand("run_command", "git stash push -- "+r405Suspect, false, "")
	if g := s.observeCommand("run_command", "git stash pop", false, ""); g != "" {
		t.Fatalf("pop must be silent, got %q", g)
	}
	g := s.observeCommand("run_command", r405Verify, true, "--- FAIL: TestX")
	if !strings.Contains(g, "REFUTED") || strings.Contains(g, "git stash pop") {
		t.Fatalf("restored experiment must not nag pop; got %q", g)
	}
}

func TestR405_GiveUpAfterSteps(t *testing.T) {
	s := r405Arm(t)
	s.observeCommand("run_command", "git stash push -- "+r405Suspect, false, "")
	for i := 0; i < attrExpGiveUpSteps+2; i++ {
		s.observeCommand("run_command", "echo noise", false, "")
	}
	if g := s.observeCommand("run_command", r405Verify, false, "ok"); g != "" {
		t.Fatalf("abandoned experiment must not conclude, got %q", g)
	}
}

func TestR405_ArmBudget(t *testing.T) {
	s := r405Arm(t)
	// Different hypothesis arms again (conclude nothing: budget only).
	if g := s.arm("internal/util/x.go", r405Verify); g == "" {
		t.Fatal("second distinct hypothesis should arm")
	}
	if g := s.arm("internal/util/y.go", r405Verify); g != "" {
		t.Fatalf("third arm must be refused, got %q", g)
	}
}

func TestR405_RerunMatchTolerance(t *testing.T) {
	cases := []struct {
		cmd  string
		want bool
	}{
		{r405Verify, true},
		{r405Verify + " 2>&1", true},
		{"cd /repo && " + r405Verify, true},
		{r405Verify + " -run TestGrep", true}, // superset rerun still counts
		{"go build ./...", false},             // different command
		{"echo", false},                       // too short for substring
	}
	for _, c := range cases {
		if got := isRerunOf(c.cmd, r405Verify); got != c.want {
			t.Errorf("isRerunOf(%q) = %v, want %v", c.cmd, got, c.want)
		}
	}
}
