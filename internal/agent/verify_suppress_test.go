package agent

import (
	"testing"
)

func TestDetectSuppression_ErrorMasking(t *testing.T) {
	tests := []struct {
		name    string
		cmd     string
		matched bool
		cat     string
	}{
		{"or-true", "go test ./... || true", true, "error-masking"},
		{"or-colon", "make build || :", true, "error-masking"},
		{"or-exit-0", "npm test || exit 0", true, "error-masking"},
		{"or-echo", "go vet ./... || echo done", true, "error-masking"},
		{"semicolon-true", "go test ./...; true", true, "error-masking"},
		{"set-plus-e", "set +e && go test ./...", true, "error-masking"},
		{"stderr-null", "go test 2>/dev/null", true, "output-hiding"},
		{"stderr-merge-pipe", "go test 2>&1 | cat", true, "output-hiding"},
		{"all-output-null", "go test >/dev/null 2>&1", true, "output-hiding"},
		{"bash-all-null", "go test &>/dev/null", true, "output-hiding"},
		{"clean-command", "go test ./...", false, ""},
		{"normal-build", "go build -tags goolm ./...", false, ""},
		{"grep-pipe", "grep foo bar | wc -l", false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matched, _, cat := detectSuppression(tt.cmd)
			if matched != tt.matched {
				t.Errorf("detectSuppression(%q) matched=%v, want %v", tt.cmd, matched, tt.matched)
			}
			if matched && cat != tt.cat {
				t.Errorf("detectSuppression(%q) category=%q, want %q", tt.cmd, cat, tt.cat)
			}
		})
	}
}

func TestCheckVerificationSuppression_FiresOnErrorMaskingVerify(t *testing.T) {
	s := newVerifySuppressState()
	// Error masking on a verification command should fire immediately.
	msg := s.checkVerificationSuppression("run_command", "go test ./... || true")
	if msg == "" {
		t.Fatal("expected guidance for error-masking on verification command")
	}
	if !s.fired {
		t.Fatal("expected fired=true after error-masking on verification")
	}
}

func TestCheckVerificationSuppression_NoFireOnClean(t *testing.T) {
	s := newVerifySuppressState()
	msg := s.checkVerificationSuppression("run_command", "go test ./...")
	if msg != "" {
		t.Fatalf("expected no guidance for clean command, got: %s", msg)
	}
	if s.fired {
		t.Fatal("expected fired=false for clean command")
	}
}

func TestCheckVerificationSuppression_OutputHidingNeedsTwo(t *testing.T) {
	s := newVerifySuppressState()
	// First output-hiding on verification command should not fire.
	msg := s.checkVerificationSuppression("run_command", "go test 2>/dev/null")
	if msg != "" {
		t.Fatalf("expected no guidance on first output-hiding, got: %s", msg)
	}
	// Second should fire.
	msg = s.checkVerificationSuppression("run_command", "npm test 2>/dev/null")
	if msg == "" {
		t.Fatal("expected guidance after second output-hiding on verification")
	}
}

func TestCheckVerificationSuppression_NonVerifyNeedsTwoMasks(t *testing.T) {
	s := newVerifySuppressState()
	// First error-masking on non-verification command should not fire.
	msg := s.checkVerificationSuppression("run_command", "echo hello || true")
	if msg != "" {
		t.Fatalf("expected no guidance on first non-verify mask, got: %s", msg)
	}
	// Second should fire.
	msg = s.checkVerificationSuppression("run_command", "echo world || true")
	if msg == "" {
		t.Fatal("expected guidance after second error-masking on non-verify")
	}
}

func TestCheckVerificationSuppression_FiresOnlyOnce(t *testing.T) {
	s := newVerifySuppressState()
	// First fire.
	msg1 := s.checkVerificationSuppression("run_command", "go test || true")
	if msg1 == "" {
		t.Fatal("expected first guidance")
	}
	// After firing, should not fire again.
	msg2 := s.checkVerificationSuppression("run_command", "go test || true")
	if msg2 != "" {
		t.Fatalf("expected no second guidance, got: %s", msg2)
	}
}

func TestVerifySuppressState_Reset(t *testing.T) {
	s := newVerifySuppressState()
	s.checkVerificationSuppression("run_command", "go test || true")
	s.checkVerificationSuppression("run_command", "go test || true")
	if len(s.suppressedCmds) == 0 {
		t.Fatal("expected suppressed commands tracked")
	}
	if !s.fired {
		t.Fatal("expected fired=true")
	}
	s.reset()
	if len(s.suppressedCmds) != 0 {
		t.Fatalf("expected cleared suppressedCmds after reset, got %d", len(s.suppressedCmds))
	}
	if s.fired {
		t.Fatal("expected fired=false after reset")
	}
}

func TestVerificationCmdRe(t *testing.T) {
	verify := []string{
		"go test ./...",
		"go build ./...",
		"go vet ./...",
		"npm test",
		"npm run build",
		"make test",
		"make verify-ci",
		"cargo test",
		"pytest tests/",
		"jest",
		"mvn test",
	}
	for _, cmd := range verify {
		if !verificationCmdRe.MatchString(cmd) {
			t.Errorf("expected %q to match verification pattern", cmd)
		}
	}
	nonVerify := []string{
		"echo hello",
		"ls -la",
		"git status",
		"cat file.go",
	}
	for _, cmd := range nonVerify {
		if verificationCmdRe.MatchString(cmd) {
			t.Errorf("expected %q to NOT match verification pattern", cmd)
		}
	}
}

func TestBuildGuidance_ContainsExamples(t *testing.T) {
	s := newVerifySuppressState()
	s.checkVerificationSuppression("run_command", "go test ./... || true")
	// buildGuidance was already called internally, so fired should be true.
	if !s.fired {
		t.Fatal("expected fired=true after guidance triggered")
	}
}

func TestCheckVerificationSuppression_NonVerifyOutputHidingCounts(t *testing.T) {
	s := newVerifySuppressState()
	// Two output-hiding (2>/dev/null) occurrences on non-verification
	// commands must fire — per the documented "any suppression ×2" contract,
	// not just error-masking (#160).
	g1 := s.checkVerificationSuppression("run_command", "echo step1 2>/dev/null")
	if g1 != "" {
		t.Fatalf("first occurrence should not fire, got: %s", g1)
	}
	g2 := s.checkVerificationSuppression("run_command", "ls -la 2>/dev/null")
	if g2 == "" {
		t.Fatal("#160 regression: second output-hiding on non-verification command should fire")
	}
}

// TestCheckVerificationSuppression_MixedBranchNoPollution pins fix #170: a
// verify-branch entry must not count toward the non-verify threshold.
func TestCheckVerificationSuppression_MixedBranchNoPollution(t *testing.T) {
	s := newVerifySuppressState()
	// 1) verification command with output-hiding (verify branch, count 1 < 2)
	if out := s.checkVerificationSuppression("run_command", "go test 2>/dev/null"); out != "" {
		t.Fatalf("first verify-branch suppression must not fire: %q", out)
	}
	// 2) single non-verify suppression — must NOT fire (verify entry must not pollute)
	if out := s.checkVerificationSuppression("run_command", "ls /tmp/nope 2>/dev/null"); out != "" {
		t.Fatalf("mixed-sequence single non-verify suppression must not fire: %q", out)
	}
	// 3) second non-verify suppression — now fires (2 same-branch occurrences)
	if out := s.checkVerificationSuppression("run_command", "cat /tmp/nope2 2>/dev/null"); out == "" {
		t.Fatal("second non-verify suppression must fire")
	}
}
