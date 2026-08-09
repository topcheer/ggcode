package agent

import (
	"strings"
	"testing"
)

func TestHistoryErrAccum_DetectsPartialAcknowledgment(t *testing.T) {
	s := newHistoryErrAccumState()

	// Simulate a tool result with 3 test failures
	toolResult := `--- FAIL: TestA (0.01s)
--- FAIL: TestB (0.02s)
--- FAIL: TestC (0.01s)
FAIL`
	s.recordToolResult("go_test", toolResult, 1)

	if len(s.pendingIssues) < 3 {
		t.Fatalf("expected at least 3 pending issues, got %d", len(s.pendingIssues))
	}

	// Agent text addresses only TestA
	text := "I'll fix TestA by updating the expected value."
	hint := s.recordAssistantText(text, 2)

	if hint == "" {
		t.Fatalf("expected warning for unacknowledged issues, got empty")
	}

	if !strings.Contains(hint, "history-error-accumulation") {
		t.Fatalf("expected hint to contain detector name, got: %s", hint)
	}

	if !strings.Contains(hint, "unaddressed") {
		t.Fatalf("expected hint to mention unaddressed items, got: %s", hint)
	}
}

func TestHistoryErrAccum_NoWarningWhenAllAcknowledged(t *testing.T) {
	s := newHistoryErrAccumState()

	// Two test failures
	toolResult := `--- FAIL: TestAlpha (0.01s)
--- FAIL: TestBeta (0.02s)`
	s.recordToolResult("go_test", toolResult, 1)

	// Agent addresses both
	text := "I'll fix TestAlpha and TestBeta by updating their assertions."
	hint := s.recordAssistantText(text, 2)

	if hint != "" {
		t.Fatalf("expected no warning when all issues acknowledged, got: %s", hint)
	}
}

func TestHistoryErrAccum_NoWarningForSingleIssue(t *testing.T) {
	s := newHistoryErrAccumState()

	// Single failure - handled by verify_disconnect instead
	toolResult := `--- FAIL: TestOnly (0.01s)`
	s.recordToolResult("go_test", toolResult, 1)

	hint := s.recordAssistantText("Moving on to the next task.", 2)
	if hint != "" {
		t.Fatalf("expected no warning for single issue, got: %s", hint)
	}
}

func TestHistoryErrAccum_NoWarningSameIteration(t *testing.T) {
	s := newHistoryErrAccumState()

	toolResult := `--- FAIL: TestA (0.01s)
--- FAIL: TestB (0.02s)
--- FAIL: TestC (0.01s)`
	s.recordToolResult("go_test", toolResult, 1)

	// Same iteration - give agent a chance first
	hint := s.recordAssistantText("Working on something else.", 1)
	if hint != "" {
		t.Fatalf("expected no warning in same iteration as tool result, got: %s", hint)
	}
}

func TestHistoryErrAccum_MaxWarnings(t *testing.T) {
	s := newHistoryErrAccumState()

	toolResult := `--- FAIL: TestA (0.01s)
--- FAIL: TestB (0.02s)
--- FAIL: TestC (0.01s)`
	s.recordToolResult("go_test", toolResult, 1)

	// First warning
	hint1 := s.recordAssistantText("Doing something unrelated.", 2)
	if hint1 == "" {
		t.Fatalf("expected first warning")
	}

	// Reset and trigger again
	s.pendingIssues = nil
	s.recordToolResult("go_test", toolResult, 3)

	// Second warning
	hint2 := s.recordAssistantText("Still doing something else.", 4)
	if hint2 == "" {
		t.Fatalf("expected second warning")
	}

	// Reset and trigger again
	s.pendingIssues = nil
	s.recordToolResult("go_test", toolResult, 5)

	// Third should be suppressed
	hint3 := s.recordAssistantText("Again doing something else.", 6)
	if hint3 != "" {
		t.Fatalf("expected no third warning (max reached), got: %s", hint3)
	}
}

func TestHistoryErrAccum_ResetClearsState(t *testing.T) {
	s := newHistoryErrAccumState()
	s.warnings = 2
	s.pendingIssues = []string{"test:Foo", "test:Bar"}
	s.pendingTool = "go_test"
	s.pendingIter = 5

	s.reset()

	if s.warnings != 0 {
		t.Fatalf("expected warnings=0 after reset, got %d", s.warnings)
	}
	if len(s.pendingIssues) != 0 {
		t.Fatalf("expected pendingIssues cleared after reset")
	}
}

func TestHistoryErrAccum_BuildErrors(t *testing.T) {
	s := newHistoryErrAccumState()

	// Multiple build errors
	toolResult := `main.go:10:5: error: undefined: foo
main.go:20:3: error: cannot use bar (type int) as type string
util.go:5:1: error: undefined: baz`
	s.recordToolResult("go_build", toolResult, 1)

	if len(s.pendingIssues) < 2 {
		t.Fatalf("expected at least 2 build issues, got %d", len(s.pendingIssues))
	}

	// Agent only mentions foo, not bar or baz
	hint := s.recordAssistantText("I fixed the foo variable.", 2)

	if hint == "" {
		t.Fatalf("expected warning for partially addressed build errors")
	}
}

func TestHistoryErrAccum_NoIssuesNoWarning(t *testing.T) {
	s := newHistoryErrAccumState()

	// Clean tool output, no issues
	toolResult := `ok  github.com/example/pkg  0.015s`
	s.recordToolResult("go_test", toolResult, 1)

	hint := s.recordAssistantText("All tests pass.", 2)
	if hint != "" {
		t.Fatalf("expected no warning for clean output, got: %s", hint)
	}
}

func TestHistoryErrAccum_WarningsCountedInOutput(t *testing.T) {
	s := newHistoryErrAccumState()

	toolResult := `--- FAIL: TestA (0.01s)
--- FAIL: TestB (0.02s)
--- FAIL: TestC (0.01s)
--- FAIL: TestD (0.01s)
--- FAIL: TestE (0.01s)`
	s.recordToolResult("go_test", toolResult, 1)

	// Agent addresses nothing
	hint := s.recordAssistantText("Let me look at the database.", 2)

	if hint == "" {
		t.Fatalf("expected warning")
	}

	// Should mention total vs unacknowledged
	if !strings.Contains(hint, "5 distinct") {
		t.Fatalf("expected hint to mention 5 total issues, got: %s", hint)
	}
}
