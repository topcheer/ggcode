package agent

import (
	"testing"
	"time"
)

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
		{"web_search", catWeb},
		{"web_fetch", catWeb},
		{"git_status", catGit},
		{"git_diff", catGit},
		{"browser", catBrowser},
		{"screenshot", catBrowser},
		{"mobile_device", catBrowser},
		{"mcp__filesystem__read", catMCP},
		{"mcp__github__create_issue", catMCP},
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

	// With no latency history, should return category default
	timeout := lt.computeAdaptiveTimeout("read_file")
	expected := categoryDefaultTimeout(catFileIO)
	if timeout != expected {
		t.Errorf("computeAdaptiveTimeout(read_file) with no history = %v, want %v", timeout, expected)
	}

	// MCP tool with no history
	timeout = lt.computeAdaptiveTimeout("mcp__test__tool")
	expected = categoryDefaultTimeout(catMCP)
	if timeout != expected {
		t.Errorf("computeAdaptiveTimeout(mcp__test__tool) with no history = %v, want %v", timeout, expected)
	}
}

func TestComputeAdaptiveTimeout_WithHistory(t *testing.T) {
	lt := NewLatencyTracker()

	// Record several fast read_file calls (~50ms each)
	for i := 0; i < 5; i++ {
		lt.RecordAndCheck("read_file", 50*time.Millisecond)
	}

	// With history, adaptive timeout should be mean*5 = 250ms, but clamped to floor (10s)
	timeout := lt.computeAdaptiveTimeout("read_file")
	if timeout != adaptiveTimeoutFloor {
		t.Errorf("computeAdaptiveTimeout(read_file) with fast history = %v, want floor %v",
			timeout, adaptiveTimeoutFloor)
	}
}

func TestComputeAdaptiveTimeout_SlowTool(t *testing.T) {
	lt := NewLatencyTracker()

	// Record several slow web_fetch calls (~20s each)
	for i := 0; i < 5; i++ {
		lt.RecordAndCheck("web_fetch", 20*time.Second)
	}

	// With history, adaptive timeout should be mean*5 = 100s
	// but category default for web is 120s, and adaptive < catDefault → use catDefault
	timeout := lt.computeAdaptiveTimeout("web_fetch")
	expected := categoryDefaultTimeout(catWeb) // 120s
	if timeout != expected {
		t.Errorf("computeAdaptiveTimeout(web_fetch) with 20s history = %v, want %v (category default, since adaptive 100s < cat default 120s)",
			timeout, expected)
	}
}

func TestComputeAdaptiveTimeout_ClampedToCeiling(t *testing.T) {
	lt := NewLatencyTracker()

	// Record extremely slow web_fetch calls (120s each)
	// mean*5 = 600s, but should be clamped to ceiling (5min = 300s)
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

	// Record moderately fast MCP tool calls (~3s each)
	for i := 0; i < 5; i++ {
		lt.RecordAndCheck("mcp__test__tool", 3*time.Second)
	}

	// mean*5 = 15s, but category default for MCP is 120s
	// Since adaptive (15s) < category default (120s) and > floor (10s),
	// it should use the category default (the higher value)
	timeout := lt.computeAdaptiveTimeout("mcp__test__tool")
	expected := categoryDefaultTimeout(catMCP) // 120s
	if timeout != expected {
		t.Errorf("computeAdaptiveTimeout(mcp) with fast history = %v, want category default %v",
			timeout, expected)
	}
}

func TestComputeAdaptiveTimeout_NilTracker(t *testing.T) {
	var lt *LatencyTracker

	// Should not panic, should return category default
	timeout := lt.computeAdaptiveTimeout("read_file")
	expected := categoryDefaultTimeout(catFileIO)
	if timeout != expected {
		t.Errorf("computeAdaptiveTimeout with nil tracker = %v, want %v", timeout, expected)
	}
}

func TestComputeAdaptiveTimeout_RespectsCategoryBounds(t *testing.T) {
	lt := NewLatencyTracker()

	// Verify that for each category, the timeout stays within reasonable bounds
	tools := []string{
		"read_file", "grep", "edit_file", "lsp_hover",
		"web_fetch", "mcp__srv__fn", "git_status", "browser",
	}

	for _, tool := range tools {
		t.Run(tool, func(t *testing.T) {
			// No history → category default
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

// TestAdaptiveTimeoutFasterThanFlatDefault verifies that for typical fast tools,
// the adaptive timeout is significantly lower than the old flat 5-minute default.
func TestAdaptiveTimeoutFasterThanFlatDefault(t *testing.T) {
	lt := NewLatencyTracker()

	// Record typical read_file latency (~100ms)
	for i := 0; i < 10; i++ {
		lt.RecordAndCheck("read_file", 100*time.Millisecond)
	}

	timeout := lt.computeAdaptiveTimeout("read_file")

	// Should be the category default (60s), not 5 minutes
	if timeout >= defaultToolTimeout {
		t.Errorf("adaptive timeout for read_file = %v, should be < flat default %v",
			timeout, defaultToolTimeout)
	}

	// Should be at most 60s (category default for file I/O)
	if timeout > 60*time.Second {
		t.Errorf("adaptive timeout for read_file = %v, should be <= 60s category default", timeout)
	}
}
