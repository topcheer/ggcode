package agent

import (
	"strings"
	"testing"
)

func TestToolCallEconomy_NoWarningEarly(t *testing.T) {
	s := newToolCallEconomyState()
	s.recordCall("read_file")
	s.recordCall("read_file")
	if msg := s.check(); msg != "" {
		t.Errorf("expected no warning with only 2 calls, got: %s", msg)
	}
}

func TestToolCallEconomy_ConsecutiveSameTool(t *testing.T) {
	s := newToolCallEconomyState()
	s.recordCall("read_file")
	s.recordCall("read_file")
	s.recordCall("read_file")
	msg := s.check()
	if msg == "" {
		t.Fatal("expected warning for 3 consecutive read_file calls")
	}
	if !strings.Contains(msg, "read_file") {
		t.Errorf("warning should mention read_file, got: %s", msg)
	}
	if !strings.Contains(msg, "multi_file_read") {
		t.Errorf("warning should suggest multi_file_read, got: %s", msg)
	}
}

func TestToolCallEconomy_NonBatchableResets(t *testing.T) {
	s := newToolCallEconomyState()
	s.recordCall("read_file")
	s.recordCall("read_file")
	s.recordCall("run_command") // non-batchable, resets consecutive
	s.recordCall("read_file")
	s.recordCall("read_file")
	// 2 consecutive read_file + 1 run_command + 2 consecutive read_file = 5 calls
	// But only 4 batchable, and window is 5, so condition 2 triggers.
	// The consecutive streak is only 2, so condition 1 doesn't fire.
	// With 4/5 batchable ratio, condition 2 fires. Let's use fewer calls.
	if msg := s.check(); msg == "" {
		t.Fatal("expected warning for high batchable ratio")
	}
}

func TestToolCallEconomy_DifferentBatchableTools(t *testing.T) {
	s := newToolCallEconomyState()
	s.recordCall("read_file")
	s.recordCall("grep")
	s.recordCall("glob")
	s.recordCall("list_directory")
	// Different batchable tools in window — these CANNOT be merged into
	// a single call (you can't batch grep + glob + read_file). Should NOT warn.
	if msg := s.check(); msg != "" {
		t.Errorf("expected no warning for different tools, got: %s", msg)
	}
	// Add one more different batchable tool
	s.recordCall("search_files")
	if msg := s.check(); msg != "" {
		t.Errorf("expected no warning for different tools even with 5 calls, got: %s", msg)
	}
	// But 3+ of the SAME tool in the window should warn
	s2 := newToolCallEconomyState()
	s2.recordCall("read_file")
	s2.recordCall("grep")
	s2.recordCall("read_file")
	s2.recordCall("glob")
	s2.recordCall("read_file")
	// read_file appears 3 times in window of 5 — should warn
	msg := s2.check()
	if msg == "" {
		t.Fatal("expected warning for 3 same-tool calls in window of 5")
	}
	if !strings.Contains(msg, "read_file") {
		t.Errorf("warning should name the tool, got: %s", msg)
	}
}

func TestToolCallEconomy_MaxWarnings(t *testing.T) {
	s := newToolCallEconomyState()
	s.maxWarnings = 1

	s.recordCall("read_file")
	s.recordCall("read_file")
	s.recordCall("read_file")
	if msg := s.check(); msg == "" {
		t.Fatal("expected first warning")
	}
	// Should not warn again
	s.recordCall("read_file")
	s.recordCall("read_file")
	s.recordCall("read_file")
	if msg := s.check(); msg != "" {
		t.Errorf("expected no more warnings after maxWarnings, got: %s", msg)
	}
}

func TestToolCallEconomy_Reset(t *testing.T) {
	s := newToolCallEconomyState()
	s.recordCall("read_file")
	s.recordCall("read_file")
	s.recordCall("read_file")
	if msg := s.check(); msg == "" {
		t.Fatal("expected warning before reset")
	}
	s.reset()
	s.recordCall("read_file")
	s.recordCall("read_file")
	if msg := s.check(); msg != "" {
		t.Errorf("expected no warning after reset with only 2 calls, got: %s", msg)
	}
}

func TestToolCallEconomy_NonBatchableOnly(t *testing.T) {
	s := newToolCallEconomyState()
	s.recordCall("run_command")
	s.recordCall("run_command")
	s.recordCall("run_command")
	s.recordCall("run_command")
	s.recordCall("run_command")
	if msg := s.check(); msg != "" {
		t.Errorf("expected no warning for non-batchable tools, got: %s", msg)
	}
}

func TestToolCallEconomy_GrepBatchHint(t *testing.T) {
	s := newToolCallEconomyState()
	s.recordCall("grep")
	s.recordCall("grep")
	s.recordCall("grep")
	msg := s.check()
	if msg == "" {
		t.Fatal("expected warning for 3 consecutive grep calls")
	}
	if !strings.Contains(msg, "alternation") {
		t.Errorf("warning should mention regex alternation for grep, got: %s", msg)
	}
}

func TestToolCallEconomy_Summary(t *testing.T) {
	s := newToolCallEconomyState()
	s.recordCall("read_file")
	s.recordCall("read_file")
	summary := s.summary()
	if !strings.Contains(summary, "window=") {
		t.Errorf("summary should contain window=, got: %s", summary)
	}
}
