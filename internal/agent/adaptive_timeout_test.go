package agent

import (
	"testing"
	"time"
)

// llm_call must never be timed out at the catDefault 2m wall nor shrunk by
// fast history: one-shot LLM calls (thinking models, schema re-asks) are
// high-variance by nature (user report 2026-11-08: most calls died at 2m0s).
func TestLLMCallTimeoutCategory(t *testing.T) {
	if got := categoryDefaultTimeout(catLLM); got != 10*time.Minute {
		t.Errorf("categoryDefaultTimeout(catLLM) = %v, want 10m", got)
	}
	lt := NewLatencyTracker()
	for i := 0; i < 10; i++ {
		lt.RecordAndCheck("llm_call", 2*time.Second) // fast history
	}
	if got := lt.computeAdaptiveTimeout("llm_call"); got != 10*time.Minute {
		t.Errorf("computeAdaptiveTimeout(llm_call) with fast history = %v, want 10m (non-adaptive)", got)
	}
}

func TestClassifyTool(t *testing.T) {
	tests := []struct {
		name     string
		expected toolTimeoutCategory
	}{
		{"read_file", catFileIO},
		{"multi_file_read", catFileIO},
		{"list_directory", catFileIO},
		{"grep", catSearch},
		{"search_files", catSearch},
		{"glob", catSearch},
		{"code_search", catSearch},
		{"edit_file", catEdit},
		{"write_file", catEdit},
		{"multi_edit_file", catEdit},
		{"multi_file_write", catEdit},
		{"notebook_edit", catEdit},
		{"lsp_definition", catLSP},
		{"lsp_hover", catLSP},
		{"code_health", catLSP},

		{"lsp_rename", catEdit},

		{"lsp_code_actions", catLSP},
		{"web_search", catWeb},
		{"web_fetch", catWeb},
		{"git_status", catGit},
		{"git_diff", catGit},
		{"browser", catBrowser},
		{"screenshot", catBrowser},
		{"mobile_device", catBrowser},
		{"mcp__filesystem__read", catMCP},
		{"mcp__github__create_issue", catMCP},
		{"llm_call", catLLM},
		{"unknown_tool", catDefault},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyTool(tt.name)
			if got != tt.expected {
				t.Errorf("classifyTool(%q) = %v, want %v", tt.name, got, tt.expected)
			}
		})
	}
}

func TestCategoryDefaultTimeout(t *testing.T) {
	tests := []struct {
		cat      toolTimeoutCategory
		min      time.Duration
		max      time.Duration
		toolName string
	}{
		{catFileIO, 10 * time.Second, 90 * time.Second, "read_file"},
		{catSearch, 10 * time.Second, 90 * time.Second, "grep"},
		{catEdit, 10 * time.Second, 60 * time.Second, "edit_file"},
		{catLSP, 10 * time.Second, 90 * time.Second, "lsp_definition"},
		{catWeb, 30 * time.Second, 180 * time.Second, "web_fetch"},
		{catMCP, 30 * time.Second, 180 * time.Second, "mcp__test"},
		{catGit, 10 * time.Second, 90 * time.Second, "git_status"},
		{catBrowser, 60 * time.Second, 240 * time.Second, "browser"},
		{catDefault, 30 * time.Second, 180 * time.Second, "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.toolName, func(t *testing.T) {
			d := categoryDefaultTimeout(tt.cat)
			if d < tt.min || d > tt.max {
				t.Errorf("categoryDefaultTimeout(%v) = %v, expected between %v and %v",
					tt.cat, d, tt.min, tt.max)
			}
		})
	}
}

func TestComputeAdaptiveTimeout_NoHistory(t *testing.T) {
	lt := NewLatencyTracker()

	timeout := lt.computeAdaptiveTimeout("read_file")
	expected := categoryDefaultTimeout(catFileIO)
	if timeout != expected {
		t.Errorf("computeAdaptiveTimeout(read_file) with no history = %v, want %v", timeout, expected)
	}

	timeout = lt.computeAdaptiveTimeout("mcp__test__tool")
	expected = categoryDefaultTimeout(catMCP)
	if timeout != expected {
		t.Errorf("computeAdaptiveTimeout(mcp__test__tool) with no history = %v, want %v", timeout, expected)
	}
}

func TestComputeAdaptiveTimeout_WithHistory(t *testing.T) {
	lt := NewLatencyTracker()

	for i := 0; i < 5; i++ {
		lt.RecordAndCheck("read_file", 50*time.Millisecond)
	}

	timeout := lt.computeAdaptiveTimeout("read_file")
	if timeout != adaptiveTimeoutFloor {
		t.Errorf("computeAdaptiveTimeout(read_file) with fast history = %v, want floor %v",
			timeout, adaptiveTimeoutFloor)
	}
}

func TestComputeAdaptiveTimeout_SlowTool(t *testing.T) {
	lt := NewLatencyTracker()

	for i := 0; i < 5; i++ {
		lt.RecordAndCheck("web_fetch", 20*time.Second)
	}

	timeout := lt.computeAdaptiveTimeout("web_fetch")
	expected := categoryDefaultTimeout(catWeb)
	if timeout != expected {
		t.Errorf("computeAdaptiveTimeout(web_fetch) with 20s history = %v, want %v (category default, since adaptive 100s < cat default 120s)",
			timeout, expected)
	}
}

func TestComputeAdaptiveTimeout_ClampedToCeiling(t *testing.T) {
	lt := NewLatencyTracker()

	for i := 0; i < 5; i++ {
		lt.RecordAndCheck("web_fetch", 120*time.Second)
	}

	timeout := lt.computeAdaptiveTimeout("web_fetch")
	if timeout != adaptiveTimeoutCeil {
		t.Errorf("computeAdaptiveTimeout with extreme history = %v, want ceiling %v",
			timeout, adaptiveTimeoutCeil)
	}
}

func TestComputeAdaptiveTimeout_CategoryDefaultFloor(t *testing.T) {
	lt := NewLatencyTracker()

	for i := 0; i < 5; i++ {
		lt.RecordAndCheck("mcp__test__tool", 3*time.Second)
	}

	timeout := lt.computeAdaptiveTimeout("mcp__test__tool")
	expected := categoryDefaultTimeout(catMCP)
	if timeout != expected {
		t.Errorf("computeAdaptiveTimeout(mcp) with fast history = %v, want category default %v",
			timeout, expected)
	}
}

func TestComputeAdaptiveTimeout_NilTracker(t *testing.T) {
	var lt *LatencyTracker

	timeout := lt.computeAdaptiveTimeout("read_file")
	expected := categoryDefaultTimeout(catFileIO)
	if timeout != expected {
		t.Errorf("computeAdaptiveTimeout with nil tracker = %v, want %v", timeout, expected)
	}
}

func TestComputeAdaptiveTimeout_RespectsCategoryBounds(t *testing.T) {
	lt := NewLatencyTracker()

	tools := []string{
		"read_file", "grep", "edit_file", "lsp_hover",
		"web_fetch", "mcp__srv__fn", "git_status", "browser",
	}

	for _, tool := range tools {
		t.Run(tool, func(t *testing.T) {

			d := lt.computeAdaptiveTimeout(tool)
			if d < adaptiveTimeoutFloor {
				t.Errorf("timeout for %s = %v, below floor %v", tool, d, adaptiveTimeoutFloor)
			}
			if d > adaptiveTimeoutCeil {
				t.Errorf("timeout for %s = %v, above ceiling %v", tool, d, adaptiveTimeoutCeil)
			}
		})
	}
}

func TestAdaptiveTimeoutFasterThanFlatDefault(t *testing.T) {
	lt := NewLatencyTracker()

	for i := 0; i < 10; i++ {
		lt.RecordAndCheck("read_file", 100*time.Millisecond)
	}

	timeout := lt.computeAdaptiveTimeout("read_file")

	if timeout >= defaultToolTimeout {
		t.Errorf("adaptive timeout for read_file = %v, should be < flat default %v",
			timeout, defaultToolTimeout)
	}

	if timeout > 60*time.Second {
		t.Errorf("adaptive timeout for read_file = %v, should be <= 60s category default", timeout)
	}
}
