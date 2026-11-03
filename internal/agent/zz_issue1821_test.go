package agent

import "testing"

func TestScopeNarrowSkipsWhenCoverageFiredForSameCmd(t *testing.T) {
	s := newScopeNarrowState()
	s.lastCoverageWarnedCmd = "go test ./internal/config/"

	if msg := s.recordVerificationCommand("run_command", "go test ./internal/config/", "FAIL", true); msg != "" {
		t.Fatalf("scopeNarrow must skip when coverage fired for the same cmd, got %q", msg)
	}
	if s.lastCoverageWarnedCmd != "" {
		t.Fatal("slot must clear after one suppression")
	}

	_ = s.recordVerificationCommand("run_command", "go test ./...", "ok", false)
}
