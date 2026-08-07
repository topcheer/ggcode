package agent

import (
	"strings"
	"testing"
)

func TestStrategyStagnation_BasicFailure(t *testing.T) {
	s := newStrategyStagnationState()
	args := `{"file_path":"/tmp/test.go"}`

	// First failure - should not trigger
	if s.recordAttempt("edit_file", args, false) {
		t.Fatal("first failure should not trigger")
	}

	// Second consecutive failure - should trigger
	if !s.recordAttempt("edit_file", args, false) {
		t.Fatal("second consecutive failure should trigger")
	}
}

func TestStrategyStagnation_SuccessBreaks(t *testing.T) {
	s := newStrategyStagnationState()
	args := `{"file_path":"/tmp/test.go"}`

	s.recordAttempt("edit_file", args, false)
	s.recordAttempt("edit_file", args, true)
	if s.recordAttempt("edit_file", args, false) {
		t.Fatal("should not trigger after success reset")
	}
}

func TestStrategyStagnation_DifferentTarget(t *testing.T) {
	s := newStrategyStagnationState()
	args1 := `{"file_path":"/tmp/a.go"}`
	args2 := `{"file_path":"/tmp/b.go"}`

	s.recordAttempt("edit_file", args1, false)
	if s.recordAttempt("edit_file", args2, false) {
		t.Fatal("different target should not trigger")
	}
}

func TestStrategyStagnation_DifferentTool(t *testing.T) {
	s := newStrategyStagnationState()
	args := `{"file_path":"/tmp/test.go"}`

	s.recordAttempt("edit_file", args, false)
	if s.recordAttempt("read_file", args, false) {
		t.Fatal("different tool should not trigger")
	}
}

func TestStrategyStagnation_MaxWarnings(t *testing.T) {
	s := newStrategyStagnationState()
	args := `{"file_path":"/tmp/test.go"}`

	s.recordAttempt("edit_file", args, false)
	if !s.recordAttempt("edit_file", args, false) {
		t.Fatal("should trigger first warning")
	}

	s.recordAttempt("edit_file", args, true) // reset

	s.recordAttempt("edit_file", args, false)
	if !s.recordAttempt("edit_file", args, false) {
		t.Fatal("should trigger second warning")
	}

	s.recordAttempt("edit_file", args, true) // reset

	s.recordAttempt("edit_file", args, false)
	if s.recordAttempt("edit_file", args, false) {
		t.Fatal("should not trigger beyond max warnings")
	}
}

func TestStrategyStagnation_Reset(t *testing.T) {
	s := newStrategyStagnationState()
	args := `{"file_path":"/tmp/test.go"}`

	s.recordAttempt("edit_file", args, false)
	s.recordAttempt("edit_file", args, false)
	s.reset()

	if len(s.recent) != 0 || s.warnings != 0 {
		t.Fatal("reset should clear state")
	}
}

func TestStrategyStagnation_HistoryTrimming(t *testing.T) {
	s := newStrategyStagnationState()
	args := `{"file_path":"/tmp/test.go"}`

	for i := 0; i < stagnationHistorySize+5; i++ {
		s.recordAttempt("edit_file", args, true)
	}

	if len(s.recent) > stagnationHistorySize {
		t.Fatalf("history should be trimmed to %d, got %d", stagnationHistorySize, len(s.recent))
	}
}

func TestExtractStagnationTarget(t *testing.T) {
	tests := []struct {
		toolName string
		args     string
		want     string
	}{
		{"edit_file", `{"file_path":"/tmp/test.go"}`, "/tmp/test.go"},
		{"read_file", `{"path":"/tmp/test.go"}`, "/tmp/test.go"},
		{"run_command", `{"command":"go build"}`, "go build"},
		{"grep", `{"pattern":"TODO"}`, "TODO"},
		{"grep", `{"query":"search term"}`, "search term"},
		{"unknown_tool", `{"foo":"bar"}`, ""},
	}

	for _, tt := range tests {
		got := extractStagnationTarget(tt.toolName, tt.args)
		if got != tt.want {
			t.Errorf("extractStagnationTarget(%q, %q) = %q, want %q", tt.toolName, tt.args, got, tt.want)
		}
	}
}

func TestStrategyStagnationWarning(t *testing.T) {
	msg := strategyStagnationWarning("edit_file", "/tmp/test.go", 3)
	if !strings.Contains(msg, "STRATEGY-STAGNATION") {
		t.Fatal("warning should contain tag")
	}
	if !strings.Contains(msg, "edit_file") {
		t.Fatal("warning should contain tool name")
	}
	if !strings.Contains(msg, "different approach") {
		t.Fatal("warning should contain guidance")
	}
}

func TestStrategyStagnationWarning_LongTarget(t *testing.T) {
	longTarget := strings.Repeat("x", 100)
	msg := strategyStagnationWarning("edit_file", longTarget, 2)
	if !strings.Contains(msg, "...") {
		t.Fatal("long target should be truncated")
	}
}
