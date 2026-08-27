package agent

import (
	"testing"
)

func TestOvercorrectionDetection(t *testing.T) {
	tests := []struct {
		name       string
		errorSev   errorSeverity
		editSize   int
		expectWarn bool
	}{
		{
			name:       "trivial error small fix - no warn",
			errorSev:   severityTrivial,
			editSize:   50,
			expectWarn: false,
		},
		{
			name:       "trivial error massive fix - warn",
			errorSev:   severityTrivial,
			editSize:   6000,
			expectWarn: true,
		},
		{
			name:       "moderate error moderate fix - no warn",
			errorSev:   severityModerate,
			editSize:   300,
			expectWarn: false,
		},
		{
			name:       "moderate error massive fix - no single warn (needs cascade)",
			errorSev:   severityModerate,
			editSize:   8000,
			expectWarn: false,
		},
		{
			name:       "severe error large fix - no warn",
			errorSev:   severitySevere,
			editSize:   5000,
			expectWarn: false,
		},
		{
			name:       "severe error massive fix - no single warn (needs cascade)",
			errorSev:   severitySevere,
			editSize:   20000,
			expectWarn: false,
		},
		{
			name:       "edit below minimum size - no warn",
			errorSev:   severityTrivial,
			editSize:   200,
			expectWarn: false,
		},
		{
			name:       "no error pending - no warn",
			errorSev:   severityNone,
			editSize:   10000,
			expectWarn: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newOvercorrectionState()
			if tt.errorSev != severityNone {
				s.pendingErr = tt.errorSev
			}
			hint := s.recordEdit(tt.editSize, "test.go")
			gotWarn := hint != ""
			if gotWarn != tt.expectWarn {
				t.Errorf("expected warn=%v, got warn=%v (hint=%q)", tt.expectWarn, gotWarn, hint)
			}
		})
	}
}

func TestOvercorrectionCascade(t *testing.T) {
	s := newOvercorrectionState()

	// First overcorrection: trivial error, massive fix
	s.pendingErr = severityTrivial
	hint1 := s.recordEdit(6000, "a.go")
	if hint1 == "" {
		t.Fatal("expected warning for first overcorrection")
	}

	// Second overcorrection: should trigger cascade
	s.pendingErr = severityTrivial
	hint2 := s.recordEdit(7000, "b.go")
	if hint2 == "" {
		t.Fatal("expected cascade warning for second consecutive overcorrection")
	}
	// Cascade warning should mention "overcorrection"
	if !overcorrectionContains(hint2, "overcorrection") {
		t.Errorf("cascade warning should mention 'consecutive', got: %s", hint2)
	}
}

func TestOvercorrectionMaxWarn(t *testing.T) {
	s := newOvercorrectionState()

	for i := 0; i < 10; i++ {
		s.pendingErr = severityTrivial
		hint := s.recordEdit(6000, "test.go")
		if i >= overcorrectionMaxWarn && hint != "" {
			t.Errorf("iteration %d: should not warn after max warnings", i)
		}
	}
}

func TestOvercorrectionSmallEditKeepsPendingErr(t *testing.T) {
	// A sub-minimum edit is deemed too small to assess, so it must not
	// consume the pending error anchor: a later assessable edit following a
	// small edit should still be recorded against the original severity.
	s := newOvercorrectionState()
	s.pendingErr = severitySevere

	// Small edit: no warning, and pendingErr must survive.
	hint := s.recordEdit(overcorrectionMinEditBytes-1, "a.go")
	if hint != "" {
		t.Fatalf("small edit should never warn, got: %s", hint)
	}
	if s.pendingErr != severitySevere {
		t.Fatalf("small edit must not consume pendingErr, got %v", s.pendingErr)
	}

	// Large edit afterwards must still be recorded as an entry (assessed
	// against the preserved severe severity), not silently dropped by the
	// pendingErr==severityNone early return.
	hint = s.recordEdit(20000, "a.go")
	if s.pendingErr != severityNone {
		t.Fatalf("assessable edit should consume pendingErr, got %v", s.pendingErr)
	}
	found := false
	for _, e := range s.entries {
		if e.editSize == 20000 && e.errorSeverity == severitySevere {
			found = true
		}
	}
	if !found {
		t.Fatal("large edit after small edit should be recorded with severe severity")
	}
}

func TestOvercorrectionReset(t *testing.T) {
	s := newOvercorrectionState()
	s.pendingErr = severityTrivial
	s.recordEdit(6000, "a.go")
	s.recordEdit(6000, "b.go")
	if len(s.entries) == 0 {
		t.Fatal("entries should exist before reset")
	}

	s.reset()
	if len(s.entries) != 0 {
		t.Errorf("entries should be cleared after reset, got %d", len(s.entries))
	}
	if s.pendingErr != severityNone {
		t.Errorf("pendingErr should be cleared after reset")
	}
	if s.warnCount != 0 {
		t.Errorf("warnCount should be 0 after reset")
	}
}

func TestClassifyErrorSeverity(t *testing.T) {
	tests := []struct {
		name     string
		tool     string
		content  string
		isError  bool
		expected errorSeverity
	}{
		{"panic", "run_command", "panic: runtime error", true, severitySevere},
		{"segfault", "run_command", "signal SIGSEGV: segfault", true, severitySevere},
		{"build fail", "run_command", "build failed: undefined: foo", true, severityModerate},
		{"syntax error", "run_command", "syntax error near unexpected token", true, severityModerate},
		{"short error", "run_command", "exit code 1", true, severityTrivial},
		{"edit fail", "edit_file", "old_text not found", true, severityTrivial},
		{"lsp short", "lsp_diagnostics", "unused variable", true, severityTrivial},
		{"lsp long", "lsp_diagnostics", "type mismatch: cannot use int as string in argument to function foo bar baz baz baz baz baz", true, severityModerate},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyErrorSeverity(tt.tool, tt.content)
			if got != tt.expected {
				t.Errorf("expected %d, got %d", tt.expected, got)
			}
		})
	}
}

func TestClassifyDiagnosticSeverity(t *testing.T) {
	// Non-error result with lint warning
	hint := classifyDiagnosticSeverity("run_command", "Warning: unused import 'fmt'")
	if hint != severityTrivial {
		t.Errorf("expected severityTrivial for unused import warning, got %d", hint)
	}

	// No warning patterns
	hint = classifyDiagnosticSeverity("run_command", "all tests passed")
	if hint != severityNone {
		t.Errorf("expected severityNone for clean result, got %d", hint)
	}

	// Non-command tool
	hint = classifyDiagnosticSeverity("read_file", "some content")
	if hint != severityNone {
		t.Errorf("expected severityNone for read_file, got %d", hint)
	}
}

func TestIsOvercorrection(t *testing.T) {
	if !isOvercorrection(severityTrivial, 10000) {
		t.Error("10000 bytes for trivial error should be overcorrection")
	}
	if isOvercorrection(severityTrivial, 50) {
		t.Error("50 bytes for trivial error should not be overcorrection")
	}
	if !isOvercorrection(severityModerate, 10000) {
		t.Error("10000 bytes for moderate error should be overcorrection")
	}
	if isOvercorrection(severitySevere, 3000) {
		t.Error("3000 bytes for severe error should not be overcorrection")
	}
}

func overcorrectionContains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
