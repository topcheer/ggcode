package agent

import (
	"strings"
	"testing"
)

func TestIssue3596SuccessRestartsEpisode(t *testing.T) {
	s := newStalledConvergenceState()

	s.recordEdit()
	s.recordVerify(buildResultWithErrors(20), true)
	s.recordEdit()
	s.recordVerify(buildResultWithErrors(15), true)
	s.recordEdit()
	if got := s.recordVerify("ok   github.com/topcheer/ggcode/internal/agent\n", false); got != "" {
		t.Fatalf("passing verification itself must not fire (got %q)", got)
	}

	s.recordEdit()
	s.recordVerify(buildResultWithErrors(20), true)
	s.recordEdit()
	s.recordVerify(buildResultWithErrors(15), true)
	s.recordEdit()
	got := s.recordVerify(buildResultWithErrors(14), true)
	if got == "" {
		t.Fatal("stalled tail after a passing verification must fire guidance")
	}
	if !strings.Contains(got, "STALLED CONVERGENCE") {
		t.Fatalf("unexpected guidance text: %q", got)
	}
}

func TestIssue3596SuccessClearsHistory(t *testing.T) {
	s := newStalledConvergenceState()
	s.recordEdit()
	s.recordVerify(buildResultWithErrors(20), true)
	s.recordEdit()
	s.recordVerify(buildResultWithErrors(15), true)

	s.recordEdit()
	s.recordVerify("OK\n", false)

	if len(s.errorHistory) != 0 {
		t.Fatalf("success must clear errorHistory, got %v", s.errorHistory)
	}
	if s.hadEdits {
		t.Fatal("success must clear hadEdits")
	}
}

func TestIssue3596NoEditFailureStillBaselined(t *testing.T) {
	s := newStalledConvergenceState()
	s.recordEdit()
	s.recordVerify(buildResultWithErrors(10), true)
	s.recordVerify(buildResultWithErrors(9), true)
	if len(s.errorHistory) != 2 {
		t.Fatalf("no-edit failure must still append baseline sample, history = %v", s.errorHistory)
	}
}

func TestIssue3596SuccessThenNoEditFailure(t *testing.T) {
	s := newStalledConvergenceState()
	s.recordEdit()
	s.recordVerify(buildResultWithErrors(10), true)
	s.recordEdit()
	s.recordVerify("OK\n", false)

	got := s.recordVerify(buildResultWithErrors(10), true)
	if got != "" {
		t.Fatalf("failure without intervening edit must not fire, got %q", got)
	}
	if len(s.errorHistory) != 1 {
		t.Fatalf("history should hold exactly the fresh baseline sample, got %v", s.errorHistory)
	}
}
