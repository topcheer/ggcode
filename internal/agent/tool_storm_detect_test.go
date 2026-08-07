package agent

import (
	"strings"
	"testing"
)

func TestToolStormNoWarningBelowWindowSize(t *testing.T) {
	s := newToolStormState()
	s.recordReasoning("short")
	s.recordToolCall("read_file", 1)
	s.recordReasoning("short")
	s.recordToolCall("grep", 2)
	// Only 2 iterations, window needs 4
	if msg := s.maybeWarn(); msg != "" {
		t.Fatalf("expected no warning with partial window, got: %s", msg)
	}
}

func TestToolStormWarnsOnBurst(t *testing.T) {
	s := newToolStormState()

	// 4 diverse tools with very thin reasoning
	tools := []string{"read_file", "grep", "glob", "list_directory"}
	for i, tool := range tools {
		s.recordReasoning("ok") // 2 chars, very thin
		s.recordToolCall(tool, i+1)
	}

	msg := s.maybeWarn()
	if msg == "" {
		t.Fatal("expected storm warning for 4 diverse tools with thin reasoning")
	}
	if !strings.Contains(msg, "[Tool Call Storm]") {
		t.Errorf("warning should contain tag, got: %s", msg)
	}
	if !strings.Contains(msg, "4 consecutive") {
		t.Errorf("warning should mention 4 consecutive calls, got: %s", msg)
	}
}

func TestToolStormNoWarnOnRichReasoning(t *testing.T) {
	s := newToolStormState()

	richReasoning := strings.Repeat("This is a detailed analysis of the code structure and I need to understand the patterns. ", 5)
	tools := []string{"read_file", "grep", "glob", "list_directory"}
	for i, tool := range tools {
		s.recordReasoning(richReasoning) // well above 80 avg
		s.recordToolCall(tool, i+1)
	}

	msg := s.maybeWarn()
	if msg != "" {
		t.Errorf("expected no warning with rich reasoning, got: %s", msg)
	}
}

func TestToolStormNoWarnOnLowDiversity(t *testing.T) {
	s := newToolStormState()

	// All same tool -- repetition_tracker covers this, storm should not fire
	for i := 0; i < 4; i++ {
		s.recordReasoning("ok")
		s.recordToolCall("grep", i+1)
	}

	msg := s.maybeWarn()
	if msg != "" {
		t.Errorf("expected no warning for same-tool repetition, got: %s", msg)
	}
}

func TestToolStormBreakByNoToolCall(t *testing.T) {
	s := newToolStormState()

	s.recordReasoning("ok")
	s.recordToolCall("read_file", 1)
	s.recordReasoning("ok")
	s.recordToolCall("grep", 2)
	// Iteration with no tool call breaks the storm
	s.recordReasoning("Let me think about this.")
	s.recordNoTool(3)
	s.recordReasoning("ok")
	s.recordToolCall("glob", 4)

	msg := s.maybeWarn()
	if msg != "" {
		t.Errorf("expected no warning when window broken by no-tool iteration, got: %s", msg)
	}
}

func TestToolStormMaxInjections(t *testing.T) {
	s := newToolStormState()

	// First burst - diverse tools with thin reasoning
	diverse1 := []string{"read_file", "grep", "glob", "list_directory"}
	for i := 0; i < 4; i++ {
		s.recordReasoning("ok")
		s.recordToolCall(diverse1[i], i+1)
	}
	msg1 := s.maybeWarn()
	if msg1 == "" {
		t.Fatal("expected first warning")
	}

	// Extend window past the cooldown (need different iterations and diverse tools)
	diverse2 := []string{"read_file", "grep", "glob", "list_directory", "search_files", "lsp_symbols"}
	for i := 4; i < 10; i++ {
		s.recordReasoning("ok")
		s.recordToolCall(diverse2[i-4], i+1)
	}
	msg2 := s.maybeWarn()
	if msg2 == "" {
		t.Fatal("expected second warning after cooldown")
	}

	// Third should be capped
	diverse3 := []string{"read_file", "grep", "glob", "list_directory", "search_files", "lsp_symbols", "code_search", "lsp_hover", "web_search", "git_log"}
	for i := 10; i < 20; i++ {
		s.recordReasoning("ok")
		s.recordToolCall(diverse3[i-10], i+1)
	}
	msg3 := s.maybeWarn()
	if msg3 != "" {
		t.Errorf("expected no third warning (capped at 2), got: %s", msg3)
	}
}

func TestToolStormReset(t *testing.T) {
	s := newToolStormState()
	s.recordReasoning("ok")
	s.recordToolCall("read_file", 1)
	s.recordReasoning("ok")
	s.recordToolCall("grep", 2)
	s.recordReasoning("ok")
	s.recordToolCall("glob", 3)
	s.recordReasoning("ok")
	s.recordToolCall("list_directory", 4)
	s.injectionCount = 1

	s.reset()

	if len(s.window) != 0 {
		t.Errorf("expected empty window after reset, got %d entries", len(s.window))
	}
	if s.injectionCount != 0 {
		t.Errorf("expected injectionCount 0 after reset, got %d", s.injectionCount)
	}
	if s.lastWarnedIter != -1 {
		t.Errorf("expected lastWarnedIter -1 after reset, got %d", s.lastWarnedIter)
	}
}

func TestToolStormMultipleCallsSameIter(t *testing.T) {
	s := newToolStormState()

	s.recordReasoning("ok")
	s.recordToolCall("read_file", 1)
	s.recordToolCall("grep", 1) // same iter, should not add duplicate entry
	s.recordReasoning("ok")
	s.recordToolCall("glob", 2)
	s.recordReasoning("ok")
	s.recordToolCall("list_directory", 3)
	s.recordReasoning("ok")
	s.recordToolCall("search_files", 4)

	// Should still detect with 4 distinct iterations
	msg := s.maybeWarn()
	if msg == "" {
		t.Fatal("expected warning even with multiple calls in one iteration")
	}
}

func TestToolStormAbsoluteReasoningThreshold(t *testing.T) {
	s := newToolStormState()

	// Average is above 80 but total is below 120 -- borderline case
	// 3 iterations at ~30 chars each = 90 total < 120 absolute threshold
	// But we need 4 iterations for the window. Let's test with 4 at 25 each = 100 total
	for i := 0; i < 4; i++ {
		s.recordReasoning("twenty five chars here!") // ~23 chars
		s.recordToolCall([]string{"read_file", "grep", "glob", "list_directory"}[i], i+1)
	}

	msg := s.maybeWarn()
	// avg = ~23 < 80, so thinAvg should trigger
	if msg == "" {
		t.Fatal("expected warning with thin absolute reasoning")
	}
}
