package agent

import (
	"strings"
	"testing"
)

// zz_issue5759_test.go pins #5759: "set but empty" env vars must get the same
// verdict from both sources in collectActualEnvVars. Pre-fix, parseEnvContent
// gated on value != "" so .env FOO= counted as NOT set, while the os.Environ
// branch counted an exported FOO= as set — two semantically equivalent
// configurations produced opposite verdicts, and .env FOO= triggered a false
// "not set in the local environment or .env file" advisory.
func TestIssue5759EmptyValueInDotEnvCountsAsSet(t *testing.T) {
	// Scenario B (the bug): example declares ISSUE_5759_KEY with no default;
	// .env assigns it an empty value. The var IS set in the .env file, so the
	// advisory must not fire.
	dir := t.TempDir()
	writeEnvFile(t, dir, ".env.example", "ISSUE_5759_KEY=\n")
	writeEnvFile(t, dir, ".env", "ISSUE_5759_KEY=\n")

	s := newEnvDriftState()
	if msg := s.check(dir); msg != "" {
		t.Errorf("PRE-FIX-BUG: empty-value assignment in .env must count as set, got advisory: %s", msg)
	}
}

func TestIssue5759EmptyShellExportCountsAsSetParity(t *testing.T) {
	// Scenario A (parity anchor): an exported empty value counts as set via
	// the os.Environ branch; the .env branch must agree — both are assignments.
	dir := t.TempDir()
	writeEnvFile(t, dir, ".env.example", "ISSUE_5759_SHELL=\n")
	t.Setenv("ISSUE_5759_SHELL", "")

	s := newEnvDriftState()
	if msg := s.check(dir); msg != "" {
		t.Errorf("empty-value shell export must count as set, got advisory: %s", msg)
	}
}

func TestIssue5759TrulyAbsentStillReported(t *testing.T) {
	// Guard the other direction: only genuinely absent vars stay in the
	// advisory; the fix must not silence real gaps.
	dir := t.TempDir()
	writeEnvFile(t, dir, ".env.example", "ISSUE_5759_PRESENT=\nISSUE_5759_ABSENT=\n")
	writeEnvFile(t, dir, ".env", "ISSUE_5759_PRESENT=\n")

	s := newEnvDriftState()
	msg := s.check(dir)
	if msg == "" {
		t.Fatal("expected advisory for ISSUE_5759_ABSENT")
	}
	if !strings.Contains(msg, "ISSUE_5759_ABSENT") {
		t.Errorf("expected ISSUE_5759_ABSENT in advisory, got: %s", msg)
	}
	if strings.Contains(msg, "ISSUE_5759_PRESENT") {
		t.Errorf("ISSUE_5759_PRESENT is set in .env (empty value) and must not be reported, got: %s", msg)
	}
}
