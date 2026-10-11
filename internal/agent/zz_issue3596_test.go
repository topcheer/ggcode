package agent

import (
	"strings"
	"testing"
)

// zz_issue3596_test.go pins the fix for #3596: a successful verification
// previously appended its (zero) error count into errorHistory, so any
// subsequent real failure produced a positive delta that permanently failed
// isStalledConvergence's non-increasing gate. A success must RESTART the
// episode instead (errorHistory = nil), keeping mixed verify flows
// (`go build && go test`, lint-then-test) detectable.

// TestIssue3596SuccessRestartsEpisode is the main bug scenario: a passing
// build interleaved into a stalling test-fix loop must not disable the
// detector for the rest of the run.
func TestIssue3596SuccessRestartsEpisode(t *testing.T) {
	s := newStalledConvergenceState()

	// Episode 1: 20 -> 15 (normal convergence), then a passing build.
	s.recordEdit()
	s.recordVerify(buildResultWithErrors(20), true)
	s.recordEdit()
	s.recordVerify(buildResultWithErrors(15), true)
	s.recordEdit()
	if got := s.recordVerify("ok   github.com/topcheer/ggcode/internal/agent\n", false); got != "" {
		t.Fatalf("passing verification itself must not fire (got %q)", got)
	}

	// Episode 2 (bug was here): post-success failures previously collided
	// with the appended 0, so this stalling tail never fired.
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

// TestIssue3596SuccessClearsHistory pins the state transition directly:
// success resets errorHistory and hadEdits.
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

// TestIssue3596NoEditFailureStillBaselined guards the preserved half of the
// old combined branch: a FAILED verification without intervening edits still
// records a baseline sample (no trend assessment).
func TestIssue3596NoEditFailureStillBaselined(t *testing.T) {
	s := newStalledConvergenceState()
	s.recordEdit()
	s.recordVerify(buildResultWithErrors(10), true)
	s.recordVerify(buildResultWithErrors(9), true) // no edit
	if len(s.errorHistory) != 2 {
		t.Fatalf("no-edit failure must still append baseline sample, history = %v", s.errorHistory)
	}
}

// TestIssue3596SuccessThenNoEditFailure records only a baseline: success
// cleared hadEdits, so an immediately following failure (no edit between)
// must not assess trend.
func TestIssue3596SuccessThenNoEditFailure(t *testing.T) {
	s := newStalledConvergenceState()
	s.recordEdit()
	s.recordVerify(buildResultWithErrors(10), true)
	s.recordEdit()
	s.recordVerify("OK\n", false)

	got := s.recordVerify(buildResultWithErrors(10), true) // no edit since success
	if got != "" {
		t.Fatalf("failure without intervening edit must not fire, got %q", got)
	}
	if len(s.errorHistory) != 1 {
		t.Fatalf("history should hold exactly the fresh baseline sample, got %v", s.errorHistory)
	}
}
