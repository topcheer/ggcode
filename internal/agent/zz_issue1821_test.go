package agent

// #1821 regression (cases 2+3):
//   - case 2: two file-header "distinct from" lists still cited the
//     detectors deleted in 387282a6 as living division partners.
//   - case 3: the coverage-gap and scope-narrow detectors both injected
//     near-identical guidance for the SAME verification command in one
//     tool call (3 injections on a standard narrowing sequence).

import "testing"

func TestScopeNarrowSkipsWhenCoverageFiredForSameCmd(t *testing.T) {
	s := newScopeNarrowState()
	s.lastCoverageWarnedCmd = "go test ./internal/config/"

	// Same command: suppressed, slot cleared.
	if msg := s.recordVerificationCommand("run_command", "go test ./internal/config/", "FAIL", true); msg != "" {
		t.Fatalf("scopeNarrow must skip when coverage fired for the same cmd, got %q", msg)
	}
	if s.lastCoverageWarnedCmd != "" {
		t.Fatal("slot must clear after one suppression")
	}
	// A DIFFERENT command narrows normally afterwards (baseline fires
	// only on the narrowing pattern; here we just assert no panic and
	// that suppression is not sticky).
	_ = s.recordVerificationCommand("run_command", "go test ./...", "ok", false)
}
