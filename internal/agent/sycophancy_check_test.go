package agent

import (
	"strings"
	"testing"
)

func TestExtractPremises(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantMin int // minimum number of premises expected
	}{
		{
			name:    "factual state claim",
			input:   "The bug is in auth.go where the token validation fails",
			wantMin: 1,
		},
		{
			name:    "usage claim",
			input:   "This function uses a deprecated API that we need to update",
			wantMin: 1,
		},
		{
			name:    "multiple premises",
			input:   "The config uses YAML. The database returns an error. The handler is broken.",
			wantMin: 1,
		},
		{
			name:    "requirement premise",
			input:   "We should use the new v2 API instead of the old one",
			wantMin: 1,
		},
		{
			name:    "question excluded",
			input:   "What is the best way to fix the auth bug?",
			wantMin: 0,
		},
		{
			name:    "command excluded",
			input:   "Please fix the bug in auth.go",
			wantMin: 0,
		},
		{
			name:    "empty input",
			input:   "",
			wantMin: 0,
		},
		{
			name:    "short line excluded",
			input:   "fix it",
			wantMin: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			premises := extractPremises(tt.input)
			if len(premises) < tt.wantMin {
				t.Errorf("extractPremises() got %d premises, want at least %d", len(premises), tt.wantMin)
			}
		})
	}
}

func TestExtractPremisesDedup(t *testing.T) {
	input := "The bug is in auth.go\nThe bug is in auth.go\nThe bug is in auth.go"
	premises := extractPremises(input)
	if len(premises) != 1 {
		t.Errorf("expected 1 deduplicated premise, got %d", len(premises))
	}
}

func TestAssistantAgrees(t *testing.T) {
	agreeCases := []string{
		"You're right, that's the issue.",
		"That's correct, the function is deprecated.",
		"Exactly! The problem is in the handler.",
		"As you mentioned, the config uses YAML.",
		"Good catch, that API is indeed broken.",
		"Spot on, we need to update the handler.",
		"I agree with your assessment.",
		"Yes, that's the root cause.",
	}
	for _, tc := range agreeCases {
		if !assistantAgrees(tc) {
			t.Errorf("assistantAgrees(%q) = false, want true", tc)
		}
	}

	noAgreeCases := []string{
		"I'll check the code first.",
		"Let me verify that claim.",
		"Running the build now.",
		"Actually, I think the issue might be elsewhere.",
	}
	for _, tc := range noAgreeCases {
		if assistantAgrees(tc) {
			t.Errorf("assistantAgrees(%q) = true, want false", tc)
		}
	}
}

func TestIsPremiseVerificationTool(t *testing.T) {
	verificationTools := []string{
		"read_file", "multi_file_read", "grep", "search_files", "glob",
		"code_search", "run_command", "list_directory",
		"lsp_definition", "lsp_references", "lsp_symbols", "lsp_workspace_symbols",
		"lsp_hover", "lsp_implementation", "lsp_diagnostics", "git_show", "git_blame",
	}
	for _, name := range verificationTools {
		if !isPremiseVerificationTool(name) {
			t.Errorf("isPremiseVerificationTool(%q) = false, want true", name)
		}
	}

	nonVerificationTools := []string{
		"edit_file", "write_file", "multi_edit_file", "git_commit", "git_add",
	}
	for _, name := range nonVerificationTools {
		if isPremiseVerificationTool(name) {
			t.Errorf("isPremiseVerificationTool(%q) = true, want false", name)
		}
	}
}

func TestCheckSycophancy_NoPremises(t *testing.T) {
	s := newSycophancyState()
	msg := s.checkSycophancy("You're right!")
	if msg != "" {
		t.Errorf("expected empty message with no premises, got: %s", msg)
	}
}

func TestCheckSycophancy_UnverifiedAgreement(t *testing.T) {
	s := newSycophancyState()
	s.captureUserPremises("The bug is in the auth handler where tokens are validated.")

	// Assistant agrees WITHOUT any verification tool being used.
	msg := s.checkSycophancy("You're right, the bug is in the auth handler.")
	if msg == "" {
		t.Fatal("expected sycophancy warning for unverified agreement, got empty")
	}
	if !strings.Contains(msg, "[Sycophancy Guard]") {
		t.Errorf("expected warning to contain [Sycophancy Guard], got: %s", msg)
	}
	if !strings.Contains(msg, "verify") {
		t.Errorf("expected warning to mention verification, got: %s", msg)
	}
}

func TestCheckSycophancy_VerifiedAgreementNoWarning(t *testing.T) {
	s := newSycophancyState()
	s.captureUserPremises("The bug is in the auth handler where tokens are validated.")

	// Agent verifies first.
	s.markVerified()

	// Now assistant agrees - but premise was verified, so no warning.
	msg := s.checkSycophancy("You're right, the bug is in the auth handler.")
	if msg != "" {
		t.Errorf("expected no warning after verification, got: %s", msg)
	}
}

func TestCheckSycophancy_NoAgreementNoWarning(t *testing.T) {
	s := newSycophancyState()
	s.captureUserPremises("The bug is in the auth handler where tokens are validated.")

	// Assistant does NOT agree - just investigates.
	msg := s.checkSycophancy("Let me check the auth handler to verify.")
	if msg != "" {
		t.Errorf("expected no warning without agreement language, got: %s", msg)
	}
}

func TestCheckSycophancy_MaxWarnings(t *testing.T) {
	s := newSycophancyState()
	// First premise + agreement.
	s.captureUserPremises("The config uses YAML format.")
	msg1 := s.checkSycophancy("That's correct, the config uses YAML.")
	if msg1 == "" {
		t.Fatal("expected first warning")
	}

	// Second premise + agreement.
	s.captureUserPremises("The database requires a password.")
	msg2 := s.checkSycophancy("You're right, it requires a password.")
	if msg2 == "" {
		t.Fatal("expected second warning")
	}

	// Third should be capped.
	s.captureUserPremises("The API endpoint is deprecated.")
	msg3 := s.checkSycophancy("Exactly, the endpoint is deprecated.")
	if msg3 != "" {
		t.Errorf("expected empty after max warnings, got: %s", msg3)
	}
}

func TestCheckSycophancy_ConsumedPremiseNotReFired(t *testing.T) {
	s := newSycophancyState()
	s.captureUserPremises("The function returns an error on bad input.")
	_ = s.checkSycophancy("You're right, it returns an error.")

	// Same premise should not re-fire in a subsequent check.
	msg := s.checkSycophancy("That's correct.")
	if msg != "" {
		t.Errorf("expected empty for already-consumed premise, got: %s", msg)
	}
}

func TestSycophancyState_Reset(t *testing.T) {
	s := newSycophancyState()
	s.captureUserPremises("The bug is in auth.go")
	s.checkSycophancy("You're right!")
	if s.warnings != 1 {
		t.Fatalf("expected 1 warning before reset, got %d", s.warnings)
	}

	s.reset()
	if s.warnings != 0 {
		t.Errorf("expected 0 warnings after reset, got %d", s.warnings)
	}
	if len(s.premises) != 0 {
		t.Errorf("expected 0 premises after reset, got %d", len(s.premises))
	}
}

func TestMarkVerified(t *testing.T) {
	s := newSycophancyState()
	s.captureUserPremises("The config uses YAML.")
	s.captureUserPremises("The handler is broken.")

	s.markVerified()

	for i, p := range s.premises {
		if !p.verified {
			t.Errorf("premise %d not marked verified after markVerified()", i)
		}
	}
}

func TestSycophancyGuardEndToEnd(t *testing.T) {
	// Simulate the full flow: user states premise, agent agrees without verifying.
	s := newSycophancyState()

	// 1. User message arrives with a premise.
	s.captureUserPremises("The function getConfig returns nil when the env var is missing.")

	// 2. Agent does NOT use a verification tool, just agrees.
	msg := s.checkSycophancy("You're right, getConfig returns nil in that case.")

	if msg == "" {
		t.Fatal("end-to-end: expected sycophancy warning")
	}
	if !strings.Contains(msg, "getConfig") {
		t.Errorf("end-to-end: expected warning to reference the premise, got: %s", msg)
	}
}
