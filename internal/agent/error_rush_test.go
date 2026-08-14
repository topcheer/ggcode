package agent

import (
	"strings"
	"testing"
)

func TestErrorRushState_NoErrors(t *testing.T) {
	s := newErrorRushState()
	s.recordToolCall("edit_file", "ok", false)
	s.recordToolCall("edit_file", "ok", false)

	if msg := s.check(); msg != "" {
		t.Errorf("expected no warning with no errors, got: %s", msg)
	}
}

func TestErrorRushState_SingleError(t *testing.T) {
	s := newErrorRushState()
	s.recordToolCall("run_command", "build failed", true)
	s.recordToolCall("edit_file", "ok", false)

	// Only 1 error, below threshold of 2
	if msg := s.check(); msg != "" {
		t.Errorf("expected no warning with single error, got: %s", msg)
	}
}

func TestErrorRushState_TwoErrorsThenBlindEdit(t *testing.T) {
	s := newErrorRushState()
	s.recordToolCall("run_command", "build failed: undefined variable", true)
	s.recordToolCall("run_command", "test failed: assertion error", true)
	s.recordToolCall("edit_file", "ok", false)

	msg := s.check()
	if msg == "" {
		t.Fatal("expected warning for blind-fix after 2 errors")
	}
	if !strings.Contains(msg, "error-rush") {
		t.Errorf("warning should contain 'error-rush', got: %s", msg)
	}
	if !strings.Contains(strings.ToLower(msg), "re-read") {
		t.Errorf("warning should contain actionable guidance")
	}
}

func TestErrorRushState_TwoErrorsThenReadBreaksPattern(t *testing.T) {
	s := newErrorRushState()
	s.recordToolCall("run_command", "build failed", true)
	s.recordToolCall("run_command", "test failed", true)
	// Diagnostic read breaks the blind-fix pattern
	s.recordToolCall("read_file", "file content", false)
	s.recordToolCall("edit_file", "ok", false)

	if msg := s.check(); msg != "" {
		t.Errorf("expected no warning when diagnostic read intervenes, got: %s", msg)
	}
}

func TestErrorRushState_ThreeErrorsThenBlindEdit(t *testing.T) {
	s := newErrorRushState()
	s.recordToolCall("run_command", "error 1", true)
	s.recordToolCall("run_command", "error 2", true)
	s.recordToolCall("run_command", "error 3", true)
	s.recordToolCall("edit_file", "ok", false)

	msg := s.check()
	if msg == "" {
		t.Fatal("expected warning for blind-fix after 3 errors")
	}
}

func TestErrorRushState_ErrorSnippetIncluded(t *testing.T) {
	s := newErrorRushState()
	s.recordToolCall("run_command", "go build: undefined: foo in main.go", true)
	s.recordToolCall("run_command", "FAIL: test_xyz [0.00s]", true)
	s.recordToolCall("edit_file", "ok", false)

	msg := s.check()
	if msg == "" {
		t.Fatal("expected warning")
	}
	// Should include relevant error snippet
	if !strings.Contains(msg, "Last error:") {
		t.Errorf("warning should include error snippet, got: %s", msg)
	}
}

func TestErrorRushState_MaxWarns(t *testing.T) {
	s := newErrorRushState()
	// First rush
	s.recordToolCall("run_command", "error 1", true)
	s.recordToolCall("run_command", "error 2", true)
	s.recordToolCall("edit_file", "ok", false)

	msg1 := s.check()
	if msg1 == "" {
		t.Fatal("expected first warning")
	}

	// Second rush
	s.recordToolCall("run_command", "error 3", true)
	s.recordToolCall("run_command", "error 4", true)
	s.recordToolCall("edit_file", "ok", false)

	msg2 := s.check()
	if msg2 == "" {
		t.Fatal("expected second warning")
	}

	// Third rush -- should be capped
	s.recordToolCall("run_command", "error 5", true)
	s.recordToolCall("run_command", "error 6", true)
	s.recordToolCall("edit_file", "ok", false)

	msg3 := s.check()
	if msg3 != "" {
		t.Errorf("expected no third warning (capped at 2), got: %s", msg3)
	}
}

func TestErrorRushState_Reset(t *testing.T) {
	s := newErrorRushState()
	s.recordToolCall("run_command", "error 1", true)
	s.recordToolCall("run_command", "error 2", true)
	s.recordToolCall("edit_file", "ok", false)
	s.consecutiveErrors = 5
	s.rushCount = 3

	s.reset()

	if s.consecutiveErrors != 0 || s.rushCount != 0 || s.warnCount != 0 {
		t.Errorf("reset did not clear state: %+v", s)
	}
}

func TestErrorRushState_RepeatWarningEscalates(t *testing.T) {
	s := newErrorRushState()

	// First rush
	s.recordToolCall("run_command", "error 1", true)
	s.recordToolCall("run_command", "error 2", true)
	s.recordToolCall("edit_file", "ok", false)
	msg1 := s.check()
	if msg1 == "" {
		t.Fatal("expected first warning")
	}
	if !strings.Contains(msg1, "SLOW DOWN") {
		t.Logf("first warning: %s", msg1)
	}

	// Second rush
	s.recordToolCall("run_command", "error 3", true)
	s.recordToolCall("run_command", "error 4", true)
	s.recordToolCall("edit_file", "ok", false)
	msg2 := s.check()
	if msg2 == "" {
		t.Fatal("expected second warning")
	}
	if !strings.Contains(msg2, "STOP editing") {
		t.Errorf("repeat warning should escalate with STOP editing, got: %s", msg2)
	}
}

func TestErrorRushIsDiagnostic(t *testing.T) {
	diagnostics := []string{"read_file", "multi_file_read", "grep", "search_files", "glob",
		"lsp_hover", "lsp_definition", "lsp_references", "lsp_symbols",
		"lsp_diagnostics", "list_directory", "code_search", "lsp_document_highlights"}
	for _, tool := range diagnostics {
		if !errorRushIsDiagnostic(tool) {
			t.Errorf("expected %s to be diagnostic", tool)
		}
	}
	if errorRushIsDiagnostic("edit_file") {
		t.Error("edit_file should not be diagnostic")
	}
}

func TestErrorRushIsMutation(t *testing.T) {
	mutations := []string{"edit_file", "write_file", "multi_edit_file", "multi_file_write",
		"notebook_edit", "batch_replace", "lsp_rename"}
	for _, tool := range mutations {
		if !errorRushIsMutation(tool) {
			t.Errorf("expected %s to be mutation", tool)
		}
	}
	if errorRushIsMutation("read_file") {
		t.Error("read_file should not be mutation")
	}
}

func TestErrorRushState_SuccessfulBuildResetsStreak(t *testing.T) {
	s := newErrorRushState()
	s.recordToolCall("run_command", "error 1", true)
	s.recordToolCall("run_command", "error 2", true)
	// A successful non-error, non-diagnostic tool resets the streak
	s.recordToolCall("git_status", "clean", false)
	s.recordToolCall("edit_file", "ok", false)

	// consecutiveErrors was reset by the successful git_status call
	if msg := s.check(); msg != "" {
		t.Errorf("expected no warning after successful tool reset streak, got: %s", msg)
	}
}

// #149: the first warning must report the error streak that TRIGGERED the
// rush (snapshot), not the already-reset zero.
func TestErrorRushState_FirstWarnReportsStreak(t *testing.T) {
	s := newErrorRushState()
	s.recordToolCall("run_command", "exit 1", true)
	s.recordToolCall("run_command", "exit 1", true)
	s.recordToolCall("edit_file", "", false) // blind fix: streak resets here
	g := s.check()
	if g == "" {
		t.Fatal("expected guidance")
	}
	if !strings.Contains(g, "2 consecutive error(s)") {
		t.Fatalf("expected '2 consecutive error(s)' in first warning, got: %s", g)
	}
}
