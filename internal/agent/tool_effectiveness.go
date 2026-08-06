package agent

import (
	"fmt"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

// Tool effectiveness tracking.
//
// Monitors per-tool success/failure rates during a run. When a specific tool
// has a poor effectiveness score (high error rate, frequent empty results, or
// repeated rejections), the tracker injects guidance suggesting the agent try
// a different tool or approach.
//
// This is distinct from:
//   - empty_search_tracker: only tracks consecutive empty searches (not per-tool rates)
//   - error_cascade: tracks cross-tool error streaks (not individual tool patterns)
//   - tool_redundancy: detects repeated identical calls (not effectiveness)
//
// Inspiration: Windsurf's SWE-1 models have "tool-call reasoning" that learns
// when to use terminal, browser, or editor tools. Claude Code tracks tool
// effectiveness to decide when to suggest alternative approaches. This tracker
// provides similar capability with zero LLM cost - pure statistical tracking.

const (
	// toolEffMinSample is the minimum calls before effectiveness is evaluated.
	// Avoids premature judgment from a single failure.
	toolEffMinSample = 3

	// toolEffThreshold is the effectiveness ratio below which guidance fires.
	// 0.34 means fewer than 1 in 3 calls succeeded.
	toolEffThreshold = 0.34

	// toolEffMaxFires caps guidance injections per run per tool.
	toolEffMaxFires = 2

	// toolEffWindow is the sliding window size for recent results.
	toolEffWindow = 8
)

// toolAltSuggestions maps tool names to alternative approaches.
var toolAltSuggestions = map[string][]string{
	"grep": {
		"Try code_search for semantic/natural-language matching",
		"Try glob if you only need file paths, not content",
		"Widen or simplify your regex pattern",
		"Use search_files which supports context lines and file-type filters",
	},
	"search_files": {
		"Try code_search for semantic ranking instead of regex",
		"Try grep with output_mode=content for inline context",
		"Use glob first to confirm file existence",
	},
	"code_search": {
		"Try grep with a more specific pattern for exact matching",
		"Try glob to find files by name pattern",
		"Use search_files with include_pattern filter",
	},
	"edit_file": {
		"Re-read the file to get exact current content before editing",
		"Try multi_edit_file for multiple changes to the same file",
		"Use write_file if you need to replace the entire file",
	},
	"run_command": {
		"Check the command syntax and try a simpler variant first",
		"Use start_command for long-running or interactive commands",
		"Break complex commands into smaller steps",
	},
	"read_file": {
		"Use offset/limit to read specific line ranges",
		"Try lsp_symbols or lsp_hover for symbol-level info",
		"Try grep to find the exact location first, then read that range",
	},
	"web_fetch": {
		"Try web_search to find the right URL first",
		"The site may require JavaScript rendering — try the browser tool",
	},
}

// toolEffEntry records a single tool call outcome.
type toolEffEntry struct {
	successful bool
}

// toolEffTracker tracks per-tool effectiveness within a run.
type toolEffTracker struct {
	mu         sync.Mutex
	calls      map[string][]toolEffEntry // tool name -> recent results (sliding window)
	errors     map[string]int            // tool name -> total errors
	totals     map[string]int            // tool name -> total calls
	firedCount map[string]int            // tool name -> guidance fires this run
}

func newToolEffTracker() *toolEffTracker {
	return &toolEffTracker{
		calls:      make(map[string][]toolEffEntry),
		errors:     make(map[string]int),
		totals:     make(map[string]int),
		firedCount: make(map[string]int),
	}
}

func (t *toolEffTracker) reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.calls = make(map[string][]toolEffEntry)
	t.errors = make(map[string]int)
	t.totals = make(map[string]int)
	t.firedCount = make(map[string]int)
}

// isPoorResult checks if a non-error result is still effectively a failure
// (e.g., empty search results, truncated output with advisory).
func isPoorResult(toolName, content string) bool {
	if searchTools[toolName] {
		return isEmptyResult(content)
	}
	// Check for truncation advisory markers in tool output
	lower := strings.ToLower(content)
	if strings.Contains(lower, "output truncated") ||
		strings.Contains(lower, "result too large") ||
		strings.Contains(lower, "max results reached") {
		return true
	}
	// Edit/write rejections
	if strings.Contains(lower, "old_text not found") ||
		strings.Contains(lower, "no changes") ||
		strings.Contains(lower, "already exists") {
		return true
	}
	return false
}

// recordCall records a tool call outcome and returns guidance if the tool's
// effectiveness has dropped below threshold. Returns "" if no guidance needed.
func (t *toolEffTracker) recordCall(toolName, content string, isError bool) string {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.totals[toolName]++
	successful := !isError && !isPoorResult(toolName, content)
	if isError {
		t.errors[toolName]++
	}

	// Append to sliding window
	entry := toolEffEntry{successful: successful}
	window := t.calls[toolName]
	window = append(window, entry)
	if len(window) > toolEffWindow {
		window = window[len(window)-toolEffWindow:]
	}
	t.calls[toolName] = window

	// Check if we should fire guidance
	totalCalls := t.totals[toolName]
	if totalCalls < toolEffMinSample {
		return ""
	}
	if t.firedCount[toolName] >= toolEffMaxFires {
		return ""
	}

	// Calculate effectiveness over recent window
	recent := window
	if len(recent) < toolEffMinSample {
		return ""
	}
	successes := 0
	for _, ev := range recent {
		if ev.successful {
			successes++
		}
	}
	rate := float64(successes) / float64(len(recent))

	debug.Log("tool-effectiveness", "tool=%s window_success=%d/%d rate=%.2f total=%d errors=%d",
		toolName, successes, len(recent), rate, totalCalls, t.errors[toolName])

	if rate > toolEffThreshold {
		return ""
	}

	t.firedCount[toolName]++
	return t.buildGuidance(toolName, rate, successes, len(recent))
}

func (t *toolEffTracker) buildGuidance(toolName string, rate float64, successes, total int) string {
	alts, ok := toolAltSuggestions[toolName]
	if !ok {
		// Generic guidance for tools without specific alternatives
		alts = []string{
			"Try a different tool or approach",
			"Verify your inputs (parameters, paths, patterns) are correct",
			"Break the task into smaller, more verifiable steps",
		}
	}

	pct := int(rate * 100)
	header := fmt.Sprintf("Tool effectiveness warning: %s has only %d%% success rate (%d/%d recent calls succeeded).",
		toolName, pct, successes, total)

	lines := append([]string{header, "Consider:"}, alts...)

	debug.Log("tool-effectiveness", "injecting guidance for %s (rate=%.2f, fire #%d)",
		toolName, rate, t.firedCount[toolName])

	return "[Tool Effectiveness] " + strings.Join(lines, "\n  - ")
}

// summary returns a brief effectiveness summary for logging/debugging.
func (t *toolEffTracker) summary() string {
	t.mu.Lock()
	defer t.mu.Unlock()

	if len(t.totals) == 0 {
		return ""
	}

	var parts []string
	for name, total := range t.totals {
		errs := t.errors[name]
		rate := float64(total-errs) / float64(total)
		if total > 0 && rate < toolEffThreshold {
			parts = append(parts, fmt.Sprintf("%s: %d/%d (%.0f%%)", name, total-errs, total, rate*100))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "Low effectiveness tools: " + strings.Join(parts, ", ")
}
