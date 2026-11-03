package agent

import (
	"fmt"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	toolEffMinSample = 3

	toolEffThreshold = 0.34

	toolEffMaxFires = 2

	toolEffWindow = 8
)

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

type toolEffEntry struct {
	successful bool
}

type toolEffTracker struct {
	mu         sync.Mutex
	calls      map[string][]toolEffEntry
	errors     map[string]int
	successes  map[string]int
	totals     map[string]int
	firedCount map[string]int
}

func newToolEffTracker() *toolEffTracker {
	return &toolEffTracker{
		calls:      make(map[string][]toolEffEntry),
		errors:     make(map[string]int),
		successes:  make(map[string]int),
		totals:     make(map[string]int),
		firedCount: make(map[string]int),
	}
}

func (t *toolEffTracker) reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.calls = make(map[string][]toolEffEntry)
	t.errors = make(map[string]int)
	t.successes = make(map[string]int)
	t.totals = make(map[string]int)
	t.firedCount = make(map[string]int)
}

var editResultTools = map[string]bool{
	"edit_file":       true,
	"multi_edit_file": true,
	"write_file":      true,
}

var filePayloadTools = map[string]bool{
	"read_file":       true,
	"multi_file_read": true,
}

func runCommandPoorResult(content string) bool {
	lower := strings.ToLower(content)

	if strings.Contains(lower, "truncated, showing tail]") {
		return true
	}
	if strings.HasPrefix(lower, "output truncated") ||
		strings.HasPrefix(lower, "result too large") ||
		strings.HasPrefix(lower, "max results reached") {
		return true
	}
	for _, line := range strings.Split(lower, "\n") {
		line = strings.TrimLeft(line, " \t")
		candidates := []string{line}
		if strings.HasPrefix(line, "...") {
			candidates = append(candidates, strings.TrimLeft(line[3:], " "))
		}
		for _, ln := range candidates {
			if lineStartsWithAdvisoryMarker(ln) {
				return true
			}
		}
	}
	return false
}

func lineStartsWithAdvisoryMarker(ln string) bool {
	for _, m := range []string{
		"[output truncated]",
		"[result too large]",
		"[max results reached]",
		"[lsp output truncated]",
		"[... mcp result truncated:",
		"[... mcp resource truncated:",
		"[output truncated at",
		"[... truncated:",
	} {
		if strings.HasPrefix(ln, m) {
			return true
		}
	}
	return false
}

var sourceEchoTools = map[string]bool{
	"git_show":      true,
	"git_diff":      true,
	"batch_replace": true,
	"browser":       true,
}

func isPoorResult(toolName, content string) bool {
	if searchTools[toolName] {
		return isEmptyResult(content)
	}

	if filePayloadTools[toolName] || sourceEchoTools[toolName] {
		return false
	}

	if toolName == "run_command" {
		return runCommandPoorResult(content)
	}

	lower := strings.ToLower(content)
	if strings.Contains(lower, "[output truncated]") ||
		strings.Contains(lower, "[result too large]") ||
		strings.Contains(lower, "[max results reached]") ||
		strings.Contains(lower, "[lsp output truncated]") ||
		strings.Contains(lower, "[... mcp result truncated:") ||
		strings.Contains(lower, "[... mcp resource truncated:") ||
		strings.Contains(lower, "[output truncated at") ||

		strings.Contains(lower, "truncated, showing tail]") ||
		strings.Contains(lower, "[... truncated:") ||
		strings.HasPrefix(lower, "output truncated") ||
		strings.HasPrefix(lower, "result too large") ||
		strings.HasPrefix(lower, "max results reached") {
		return true
	}

	if editResultTools[toolName] {
		if strings.Contains(lower, "old_text not found") ||
			strings.Contains(lower, "no changes applied") ||
			strings.Contains(lower, "no modifications were") ||
			strings.Contains(lower, "file already exists; use edit") {
			return true
		}
	}
	return false
}

func (t *toolEffTracker) recordCall(toolName, content string, isError bool) string {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.totals[toolName]++
	successful := !isError && !isPoorResult(toolName, content)
	if isError {
		t.errors[toolName]++
	}
	if successful {
		t.successes[toolName]++
	}

	entry := toolEffEntry{successful: successful}
	window := t.calls[toolName]
	window = append(window, entry)
	if len(window) > toolEffWindow {
		window = window[len(window)-toolEffWindow:]
	}
	t.calls[toolName] = window

	totalCalls := t.totals[toolName]
	if totalCalls < toolEffMinSample {
		return ""
	}
	if t.firedCount[toolName] >= toolEffMaxFires {
		return ""
	}

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

func (t *toolEffTracker) summary() string {
	t.mu.Lock()
	defer t.mu.Unlock()

	if len(t.totals) == 0 {
		return ""
	}

	var parts []string
	for name, total := range t.totals {
		succ := t.successes[name]
		rate := float64(succ) / float64(total)
		if total > 0 && rate < toolEffThreshold {
			parts = append(parts, fmt.Sprintf("%s: %d/%d (%.0f%%)", name, succ, total, rate*100))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "Low effectiveness tools: " + strings.Join(parts, ", ")
}
