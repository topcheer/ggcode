package agent

import (
	"testing"
)

func TestSelfDiagScanDiagnosisClaims(t *testing.T) {
	tests := []struct {
		name string
		text string
		want int // minimum number of expected claims
	}{
		{
			name: "definitive diagnosis",
			text: "The error is caused by a missing import statement at the top of the file.",
			want: 1,
		},
		{
			name: "this fails because",
			text: "This fails because the struct field name doesn't match the JSON tag.",
			want: 1,
		},
		{
			name: "root cause",
			text: "The root cause is that we're passing a nil pointer to the function.",
			want: 1,
		},
		{
			name: "no diagnosis language",
			text: "Let me check the file to understand what's happening.",
			want: 0,
		},
		{
			name: "multiple claims",
			text: "The error is caused by a missing import. The fix is simple. That's because the struct changed.",
			want: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claims := scanDiagnosisClaims(tt.text)
			if len(claims) < tt.want {
				t.Errorf("expected >= %d claims, got %d: %v", tt.want, len(claims), claims)
			}
		})
	}
}

func TestSelfDiagHasRecentError(t *testing.T) {
	s := newSelfDiagState()

	// No entries.
	hasErr, errIter := s.hasRecentError(1)
	if hasErr {
		t.Error("expected no error with empty state")
	}
	if errIter != 0 {
		t.Errorf("expected error iter 0, got %d", errIter)
	}

	// Record a successful run_command.
	s.recordToolCall(1, "run_command", false)
	hasErr, _ = s.hasRecentError(2)
	if hasErr {
		t.Error("expected no error after successful tool call")
	}

	// Record a failed edit.
	s.recordToolCall(2, "edit_file", true)
	hasErr, errIter = s.hasRecentError(3)
	if !hasErr {
		t.Error("expected error after failed edit_file")
	}
	if errIter != 2 {
		t.Errorf("expected error iter 2, got %d", errIter)
	}

	// Error too old.
	hasErr, _ = s.hasRecentError(10)
	if hasErr {
		t.Error("expected no error when error is too old")
	}
}

func TestSelfDiagHadVerificationSinceError(t *testing.T) {
	s := newSelfDiagState()

	// Error at iteration 2.
	s.recordToolCall(2, "edit_file", true)

	// No verification after error.
	if s.hadVerificationSinceError(2, 3) {
		t.Error("expected no verification when none called")
	}

	// Verification tool called after error.
	s.recordToolCall(3, "read_file", false)
	if !s.hadVerificationSinceError(2, 4) {
		t.Error("expected verification after read_file call")
	}

	// Verification tool called before error doesn't count.
	s2 := newSelfDiagState()
	s2.recordToolCall(1, "read_file", false)
	s2.recordToolCall(2, "edit_file", true)
	if s2.hadVerificationSinceError(2, 3) {
		t.Error("expected no verification when read_file was before error")
	}
}

func TestSelfDiagMaybeWarn(t *testing.T) {
	a := &Agent{selfDiagState: newSelfDiagState()}

	// No recent error -> no warning.
	hint := a.maybeWarnSelfDiagnosis("The error is caused by a missing import.", 5)
	if hint != "" {
		t.Error("expected no warning when no recent error")
	}

	// Record a failed tool call.
	a.selfDiagState.recordToolCall(3, "run_command", true)

	// Diagnosis without verification -> warning.
	hint = a.maybeWarnSelfDiagnosis("The error is caused by a missing import.", 4)
	if hint == "" {
		t.Error("expected warning for unverified diagnosis after error")
	}

	// Max warnings limit.
	a.selfDiagState.warnings = selfDiagMaxWarnings
	a.selfDiagState.recordToolCall(5, "edit_file", true)
	hint = a.maybeWarnSelfDiagnosis("This fails because the struct changed.", 6)
	if hint != "" {
		t.Error("expected no warning when max warnings reached")
	}
}

func TestSelfDiagVerifiedSuppressesWarning(t *testing.T) {
	a := &Agent{selfDiagState: newSelfDiagState()}

	// Error then verification then diagnosis.
	a.selfDiagState.recordToolCall(1, "run_command", true)
	a.selfDiagState.recordToolCall(2, "read_file", false)

	hint := a.maybeWarnSelfDiagnosis("The error is caused by a missing import.", 3)
	if hint != "" {
		t.Error("expected no warning when verification was called")
	}
}

func TestSelfDiagReset(t *testing.T) {
	s := newSelfDiagState()
	s.recordToolCall(1, "run_command", true)
	s.warnings = 2
	s.diagCount = 1

	s.reset()

	if len(s.entries) != 0 {
		t.Errorf("expected entries cleared, got %d", len(s.entries))
	}
	if s.warnings != 0 {
		t.Errorf("expected warnings 0, got %d", s.warnings)
	}
	if s.diagCount != 0 {
		t.Errorf("expected diagCount 0, got %d", s.diagCount)
	}
}

func TestSelfDiagNonErrorToolsIgnored(t *testing.T) {
	s := newSelfDiagState()

	// A non-error-tool failure (e.g. grep returning error) shouldn't trigger.
	s.recordToolCall(1, "grep", true)
	hasErr, _ := s.hasRecentError(2)
	if hasErr {
		t.Error("expected no trigger for non-error-tool (grep) failure")
	}
}
