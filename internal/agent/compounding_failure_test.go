package agent

import (
	"testing"
)

func TestCompoundingFailure_DoesNotFireWithEmptyWindow(t *testing.T) {
	c := newCompoundingFailureState()
	if msg := c.check(); msg != "" {
		t.Fatalf("expected empty message with empty window, got: %s", msg)
	}
}

func TestCompoundingFailure_DoesNotFireBelowThreshold(t *testing.T) {
	c := newCompoundingFailureState()

	results := []struct {
		tool string
		err  bool
	}{
		{"edit_file", true},
		{"grep", true},
		{"read_file", false},
		{"run_command", true},
		{"read_file", false},
		{"edit_file", true},
		{"read_file", false},
		{"grep", true},
		{"read_file", false},
		{"read_file", false},
	}
	for _, r := range results {
		c.recordResult(r.tool, r.err)
	}
	if msg := c.check(); msg != "" {
		t.Fatalf("expected no guidance at 50%% failure rate, got: %s", msg)
	}
}

func TestCompoundingFailure_DoesNotFireSingleCategory(t *testing.T) {
	c := newCompoundingFailureState()

	for i := 0; i < 8; i++ {
		c.recordResult("edit_file", true)
	}
	c.recordResult("read_file", false)
	c.recordResult("read_file", false)
	if msg := c.check(); msg != "" {
		t.Fatalf("expected no guidance with single failure category, got: %s", msg)
	}
}

func TestCompoundingFailure_FiresOnHighRateMultiCategory(t *testing.T) {
	c := newCompoundingFailureState()

	results := []struct {
		tool string
		err  bool
	}{
		{"edit_file", true},
		{"grep", true},
		{"run_command", true},
		{"read_file", false},
		{"edit_file", true},
		{"grep", true},
		{"run_command", true},
		{"edit_file", true},
		{"read_file", false},
		{"lsp_hover", true},
	}
	for _, r := range results {
		c.recordResult(r.tool, r.err)
	}
	msg := c.check()
	if msg == "" {
		t.Fatal("expected strategy reset guidance for 80%% failure rate across 4 categories")
	}
	if c.fired == false {
		t.Fatal("expected fired=true after guidance")
	}
}

func TestCompoundingFailure_FiresOnlyOnce(t *testing.T) {
	c := newCompoundingFailureState()
	for i := 0; i < 8; i++ {
		c.recordResult("edit_file", true)
	}
	c.recordResult("read_file", false)
	c.recordResult("grep", true)
	_ = c.check()
	if !c.fired {
		t.Fatal("expected fired after first check")
	}

	if msg := c.check(); msg != "" {
		t.Fatalf("expected empty on second check, got: %s", msg)
	}
}

func TestCompoundingFailure_Reset(t *testing.T) {
	c := newCompoundingFailureState()
	for i := 0; i < 10; i++ {
		c.recordResult("edit_file", true)
	}
	_ = c.check()
	c.reset()
	if c.fired {
		t.Fatal("expected fired=false after reset")
	}
	if len(c.window) != 0 {
		t.Fatal("expected empty window after reset")
	}
	if len(c.failedCategories) != 0 {
		t.Fatal("expected empty failedCategories after reset")
	}
}

func TestCompoundingFailure_SlidingWindowEviction(t *testing.T) {
	c := newCompoundingFailureState()

	for i := 0; i < 5; i++ {
		c.recordResult("edit_file", true)
	}
	for i := 0; i < 5; i++ {
		c.recordResult("grep", true)
	}

	for i := 0; i < 10; i++ {
		c.recordResult("read_file", false)
	}

	failCount := 0
	for _, e := range c.window {
		if e.isError {
			failCount++
		}
	}
	if failCount != 0 {
		t.Fatalf("expected 0 failures after eviction, got %d", failCount)
	}
	if len(c.failedCategories) != 0 {
		t.Fatalf("expected 0 failed categories after eviction, got %d", len(c.failedCategories))
	}
}

func TestCompoundingFailure_InterleavedSuccessesMaskedBySlidingWindow(t *testing.T) {
	c := newCompoundingFailureState()

	results := []struct {
		tool string
		err  bool
	}{
		{"edit_file", true},
		{"grep", true},
		{"read_file", false},
		{"run_command", true},
		{"edit_file", true},
		{"read_file", false},
		{"grep", true},
		{"run_command", true},
		{"read_file", false},
		{"edit_file", true},
	}
	for _, r := range results {
		c.recordResult(r.tool, r.err)
	}

	msg := c.check()
	if msg == "" {
		t.Fatal("expected guidance for interleaved failure pattern (70%% rate, 3 categories)")
	}
}

func TestToolCategory(t *testing.T) {
	tests := []struct {
		tool     string
		expected string
	}{
		{"edit_file", "editing"},
		{"multi_edit_file", "editing"},
		{"write_file", "editing"},
		{"multi_file_edit", "editing"},
		{"notebook_edit", "editing"},
		{"run_command", "command"},
		{"start_command", "command"},
		{"grep", "search"},
		{"search_files", "search"},
		{"code_search", "search"},
		{"glob", "search"},
		{"read_file", "reading"},
		{"multi_file_read", "reading"},
		{"list_directory", "reading"},
		{"lsp_hover", "lsp"},
		{"lsp_definition", "lsp"},
		{"git_status", "git"},
		{"git_diff", "git"},
		{"unknown_tool", "other"},
	}
	for _, tc := range tests {
		got := toolCategory(tc.tool)
		if got != tc.expected {
			t.Errorf("toolCategory(%q) = %q, want %q", tc.tool, got, tc.expected)
		}
	}
}
