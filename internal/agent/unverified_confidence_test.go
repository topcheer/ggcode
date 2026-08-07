package agent

import (
	"strings"
	"testing"
)

func TestDetectOverconfidentClaims(t *testing.T) {
	tests := []struct {
		name    string
		text    string
		wantMin int // minimum number of claims expected
	}{
		{
			name:    "definitely works",
			text:    "This will definitely work now. I've fixed the issue.",
			wantMin: 1,
		},
		{
			name:    "fix is complete",
			text:    "The fix is complete and ready for review.",
			wantMin: 1,
		},
		{
			name:    "fully resolved",
			text:    "The bug is fully resolved with these changes.",
			wantMin: 1,
		},
		{
			name:    "guaranteed to work",
			text:    "This is guaranteed to work correctly.",
			wantMin: 1,
		},
		{
			name:    "issue is resolved",
			text:    "The issue is now resolved.",
			wantMin: 1,
		},
		{
			name:    "should work fine",
			text:    "This should work fine in production.",
			wantMin: 1,
		},
		{
			name:    "no claims",
			text:    "I've edited the file. Let me run the tests next.",
			wantMin: 0,
		},
		{
			name:    "hedging language not flagged",
			text:    "I think this might work, but I'm not sure.",
			wantMin: 0,
		},
		{
			name:    "multiple claims",
			text:    "This will definitely work. The fix is complete. The issue is now resolved.",
			wantMin: 3,
		},
		{
			name:    "properly handles",
			text:    "The new code properly handles edge cases.",
			wantMin: 1,
		},
		{
			name:    "no longer an issue",
			text:    "The race condition is no longer an issue.",
			wantMin: 1,
		},
		{
			name:    "changes are correct",
			text:    "These changes are correct and follow best practices.",
			wantMin: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claims := detectOverconfidentClaims(tt.text)
			if len(claims) < tt.wantMin {
				t.Errorf("detectOverconfidentClaims() got %d claims, want at least %d. Claims: %v", len(claims), tt.wantMin, claims)
			}
		})
	}
}

func TestUnverifiedConfidenceState_RecordToolCall(t *testing.T) {
	s := newUnverifiedConfidenceState()

	// Initially nothing edited or verified
	if s.codeEdited {
		t.Error("codeEdited should be false initially")
	}
	if s.verified {
		t.Error("verified should be false initially")
	}

	// Record an edit tool
	s.recordToolCall("edit_file", `{"path":"foo.go"}`)
	if !s.codeEdited {
		t.Error("codeEdited should be true after edit_file")
	}
	if s.verified {
		t.Error("verified should be false after edit_file")
	}

	// Record a verification tool
	s.recordToolCall("run_command", `{"command":"go test ./..."}`)
	if s.codeEdited {
		t.Error("codeEdited should be false after verification")
	}
	if !s.verified {
		t.Error("verified should be true after run_command with test")
	}
}

func TestUnverifiedConfidenceState_Reset(t *testing.T) {
	s := newUnverifiedConfidenceState()
	s.warnings = 5
	s.codeEdited = true
	s.verified = true
	s.recentTools = append(s.recentTools, "edit_file", "run_command")

	s.reset()

	if s.warnings != 0 {
		t.Errorf("warnings = %d, want 0", s.warnings)
	}
	if s.codeEdited {
		t.Error("codeEdited should be false after reset")
	}
	if s.verified {
		t.Error("verified should be false after reset")
	}
	if len(s.recentTools) != 0 {
		t.Errorf("recentTools len = %d, want 0", len(s.recentTools))
	}
}

func TestMaybeWarnUnverifiedConfidence(t *testing.T) {
	a := &Agent{
		unverifiedConfidence: newUnverifiedConfidenceState(),
	}

	// Case 1: Code edited + overconfident claim + no verification -> should warn
	a.unverifiedConfidence.reset()
	a.unverifiedConfidence.recordToolCall("edit_file", `{"path":"main.go"}`)
	hint := a.maybeWarnUnverifiedConfidence("This will definitely work now. The fix is complete.")
	if hint == "" {
		t.Error("should warn when code edited with overconfident claims and no verification")
	}
	if !strings.Contains(hint, "Calibration") {
		t.Errorf("hint should contain 'Calibration', got: %s", hint)
	}

	// Case 2: Code edited + verification run + overconfident claim -> should NOT warn
	a.unverifiedConfidence.reset()
	a.unverifiedConfidence.recordToolCall("edit_file", `{"path":"main.go"}`)
	a.unverifiedConfidence.recordToolCall("run_command", `{"command":"go test ./..."}`)
	hint = a.maybeWarnUnverifiedConfidence("This will definitely work now.")
	if hint != "" {
		t.Errorf("should NOT warn when verification was run, got: %s", hint)
	}

	// Case 3: No code edited + overconfident claim -> should NOT warn (nothing to verify)
	a.unverifiedConfidence.reset()
	hint = a.maybeWarnUnverifiedConfidence("This will definitely work now.")
	if hint != "" {
		t.Errorf("should NOT warn when no code was edited, got: %s", hint)
	}

	// Case 4: Max warnings enforced
	a.unverifiedConfidence.reset()
	a.unverifiedConfidence.recordToolCall("write_file", `{"path":"x.go"}`)
	hint1 := a.maybeWarnUnverifiedConfidence("This definitely works.")
	hint2 := a.maybeWarnUnverifiedConfidence("This certainly works too.")
	hint3 := a.maybeWarnUnverifiedConfidence("This guaranteed works as well.")
	if hint1 == "" || hint2 == "" {
		t.Error("first two warnings should fire")
	}
	if hint3 != "" {
		t.Error("third warning should be rate-limited (max 2)")
	}
}

func TestIsCodeEditTool(t *testing.T) {
	editTools := []string{"edit_file", "multi_edit_file", "write_file", "multi_file_edit", "batch_replace", "notebook_edit", "file_ops"}
	for _, tool := range editTools {
		if !isCodeEditTool(tool) {
			t.Errorf("isCodeEditTool(%q) = false, want true", tool)
		}
	}

	nonEditTools := []string{"read_file", "run_command", "grep", "search_files", "glob", "git_status"}
	for _, tool := range nonEditTools {
		if isCodeEditTool(tool) {
			t.Errorf("isCodeEditTool(%q) = true, want false", tool)
		}
	}
}

func TestExtractSentence(t *testing.T) {
	text := "First sentence. This will definitely work now. Third sentence."
	idx := strings.Index(text, "definitely")
	if idx < 0 {
		t.Fatal("test setup error: 'definitely' not found")
	}
	// Find match index range for "definitely"
	matchIdx := []int{idx, idx + len("definitely")}
	sentence := extractSentence(text, matchIdx)
	if !strings.Contains(sentence, "definitely") {
		t.Errorf("extractSentence should contain 'definitely', got: %s", sentence)
	}
	if strings.Contains(sentence, "First sentence") {
		t.Errorf("extractSentence should not contain previous sentence, got: %s", sentence)
	}
}
