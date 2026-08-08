package agent

import (
	"strings"
	"testing"
)

func TestContainsFailureIndicator(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		want     bool
		wantType string
	}{
		{"build error", "main.go:10: undefined: foo", true, "error in output"},
		{"test failure", "FAIL  TestAgent [0.003s]", true, "test/build failure"},
		{"panic", "panic: runtime error: nil pointer", true, "error in output"},
		{"not found", "grep: ./foo: No such file or directory", true, "empty/not-found result"},
		{"no results", "No results found.", true, "empty/not-found result"},
		{"exit code", "exit code 1", true, "failure indicator"},
		{"success output", "All tests passed.", false, ""},
		{"empty", "", false, ""},
		{"short", "ok", false, ""},
		{"compile error", "./main.go:5:3: undefined: bar", true, "error in output"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gotType := containsFailureIndicator(tt.content)
			if got != tt.want {
				t.Errorf("containsFailureIndicator(%q) got=%v want=%v", tt.content, got, tt.want)
			}
			if tt.want && gotType != tt.wantType {
				t.Errorf("containsFailureIndicator(%q) type=%q want=%q", tt.content, gotType, tt.wantType)
			}
		})
	}
}

func TestOutcomeMisattribRecordAndCheck(t *testing.T) {
	// Scenario: tool result has failure, next iteration claims success.
	s := newOutcomeMisattribState()

	// Record a failure from run_command at iteration 3.
	s.recordResult("run_command", "FAIL  TestFoo [0.001s]", false, 3)

	// Iteration 4: agent claims success without corrective action.
	hint := s.checkMisattribution("Great, the fix works correctly now!", 4)
	if hint == "" {
		t.Fatal("expected misattribution warning but got none")
	}
	if !strings.Contains(hint, "outcome misattribution") {
		t.Errorf("unexpected hint: %s", hint)
	}
	if s.warnings != 1 {
		t.Errorf("warnings=%d want=1", s.warnings)
	}
}

func TestOutcomeMisattribWithCorrectiveAction(t *testing.T) {
	// Scenario: failure at iter 3, corrective edit at iter 4, success at iter 5.
	s := newOutcomeMisattribState()

	s.recordResult("run_command", "panic: nil pointer dereference", false, 3)
	s.recordToolCallForOM("edit_file")
	// Iteration 4 - same as failure+1, but corrective action was taken.
	hint := s.checkMisattribution("done", 4)
	if hint != "" {
		t.Errorf("expected no warning after corrective action, got: %s", hint)
	}
}

func TestOutcomeMisattribNoSuccessClaim(t *testing.T) {
	// Failure at iter 3, no success claim at iter 4.
	s := newOutcomeMisattribState()

	s.recordResult("run_command", "error: something went wrong", false, 3)
	hint := s.checkMisattribution("Let me look at the error more closely.", 4)
	if hint != "" {
		t.Errorf("expected no warning without success claim, got: %s", hint)
	}
}

func TestOutcomeMisattribStaleFailure(t *testing.T) {
	// Failure at iter 3, no check until iter 6 (too late).
	s := newOutcomeMisattribState()

	s.recordResult("run_command", "FAIL test", false, 3)
	hint := s.checkMisattribution("all done", 6)
	if hint != "" {
		t.Errorf("expected no warning for stale failure, got: %s", hint)
	}
	// Pending failure should be cleared.
	if s.pendingFailureIter != -1 {
		t.Errorf("expected pendingFailureIter=-1 after stale check, got %d", s.pendingFailureIter)
	}
}

func TestOutcomeMisattribNonVerifiableTool(t *testing.T) {
	// Non-verifiable tool results should not be checked.
	s := newOutcomeMisattribState()
	s.recordResult("todo_write", "error in some task", false, 3)
	hint := s.checkMisattribution("done", 4)
	if hint != "" {
		t.Errorf("expected no warning for non-verifiable tool, got: %s", hint)
	}
}

func TestOutcomeMisattribExplicitError(t *testing.T) {
	// IsError results should always be recorded, regardless of tool.
	s := newOutcomeMisattribState()
	s.recordResult("some_random_tool", "whatever", true, 5)
	hint := s.checkMisattribution("The fix is done and verified.", 6)
	if hint == "" {
		t.Error("expected warning for explicit error result")
	}
}

func TestOutcomeMisattribMaxWarnings(t *testing.T) {
	s := newOutcomeMisattribState()
	s.warnings = outcomeMisattribMaxWarnings
	s.recordResult("run_command", "FAIL", false, 3)
	hint := s.checkMisattribution("done", 4)
	if hint != "" {
		t.Error("expected no warning after max warnings reached")
	}
}

func TestOutcomeMisattribSuccessClaimRegex(t *testing.T) {
	claims := []string{
		"Done",
		"The build works correctly now",
		"All tests are passing",
		"successfully implemented the feature",
		"no issues found",
		"the fix works as expected",
	}
	for _, c := range claims {
		if outcomeSuccessClaimRe.FindString(c) == "" {
			t.Errorf("expected success claim match for %q", c)
		}
	}
}
