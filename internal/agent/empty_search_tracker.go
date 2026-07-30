package agent

import (
	"fmt"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

// Empty search spiral detection.
//
// When an agent makes multiple consecutive search/query tool calls that all
// return empty results (no matches, no files, no commits), it indicates the
// search strategy itself is wrong — wrong pattern, wrong directory, wrong tool.
// Without guidance, the agent can waste 5-10 iterations trying increasingly
// similar searches before correcting course.
//
// This is distinct from:
//   - Overseer read-only stall (fires at 15 iterations of read-only tools)
//   - Error streak (these results aren't errors — they're "success" with no data)
//   - Tool spam (different tools may be used)
//
// Inspiration: Claude Code and Cursor both detect "futile search" patterns and
// inject alternative strategies (broaden pattern, try different directory,
// check file existence first).

const (
	// emptySearchThreshold is the number of consecutive empty search results
	// before triggering guidance. Set to 3 — at that point the agent is
	// clearly on the wrong track and needs strategic redirection.
	emptySearchThreshold = 3

	// emptySearchMaxFires caps the number of times guidance is injected per
	// run to avoid spamming.
	emptySearchMaxFires = 2
)

// searchTools are tools whose results may be "empty" (no matches, no files).
var searchTools = map[string]bool{
	"grep":         true,
	"glob":         true,
	"search_files": true,
	"code_search":  true,
	"git_log":      true,
	"git_show":     true,
	"git_blame":    true,
	"git_diff":     true,
}

// emptyResultPatterns are substrings that indicate a tool returned no useful data.
var emptyResultPatterns = []string{
	"no matches found",
	"no files found",
	"no results",
	"no commits found",
	"nothing found",
	"no matching",
	"0 matches",
	"0 results",
	"0 files",
	"no changes",
	"nothing to show",
}

// emptySearchState tracks consecutive empty search results within a run.
type emptySearchState struct {
	mu            sync.Mutex
	consecutive   int // current streak of consecutive empty search results
	totalEmpties  int // total empty searches in this run
	guidanceFired int // how many times guidance was injected
	lastTool      string
	lastPattern   string
}

func newEmptySearchState() *emptySearchState {
	return &emptySearchState{}
}

func (e *emptySearchState) reset() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.consecutive = 0
	e.totalEmpties = 0
	e.guidanceFired = 0
	e.lastTool = ""
	e.lastPattern = ""
}

// isEmptyResult checks if a tool result content indicates an empty/no-data
// response. Only called for search/query tools.
func isEmptyResult(content string) bool {
	if strings.TrimSpace(content) == "" {
		return true
	}
	// Short results (< 200 chars) are likely single-line summaries like
	// "No matches found." Long results with actual data won't match.
	if len(content) > 500 {
		return false
	}
	lower := strings.ToLower(content)
	for _, pat := range emptyResultPatterns {
		if strings.Contains(lower, pat) {
			return true
		}
	}
	return false
}

// recordResult tracks a tool result and returns guidance text if the empty
// search spiral threshold has been reached. Returns "" if no guidance needed.
func (e *emptySearchState) recordResult(toolName string, content string, isError bool, pattern string) string {
	e.mu.Lock()
	defer e.mu.Unlock()

	if isError {
		// Errors reset the streak — the error handling system will deal with it.
		e.consecutive = 0
		return ""
	}

	if !searchTools[toolName] {
		// Non-search tool with a non-error result: reset streak (agent made progress).
		e.consecutive = 0
		return ""
	}

	if isEmptyResult(content) {
		e.consecutive++
		e.totalEmpties++
		e.lastTool = toolName
		e.lastPattern = pattern
		debug.Log("empty-search", "empty result from %s (consecutive=%d, total=%d)",
			toolName, e.consecutive, e.totalEmpties)
	} else {
		// Productive search result — reset streak.
		e.consecutive = 0
		return ""
	}

	if e.consecutive >= emptySearchThreshold && e.guidanceFired < emptySearchMaxFires {
		e.guidanceFired++
		return e.buildGuidance()
	}

	return ""
}

func (e *emptySearchState) buildGuidance() string {
	var tips []string

	switch {
	case e.consecutive >= emptySearchThreshold*2:
		// Severe spiral: suggest fundamentally different approach.
		tips = append(tips,
			"Your search strategy is not working. Consider a completely different approach:",
			"1. List the directory structure with list_directory to understand the layout",
			"2. Use code_search with natural-language queries instead of exact patterns",
			"3. Try reading a known file and follow imports/references",
			"4. Ask yourself: does the thing you're searching for actually exist?",
		)
	default:
		tips = append(tips,
			fmt.Sprintf("You have made %d consecutive searches returning no results. Adjust your approach:", e.consecutive),
			"1. Broaden the search pattern (use wildcards, shorter substrings)",
			"2. Search a different or parent directory",
			"3. Try a different search tool (grep vs glob vs code_search)",
			"4. Check if the file/path exists with list_directory first",
		)
	}

	debug.Log("empty-search", "injecting guidance (consecutive=%d, fire #%d)",
		e.consecutive, e.guidanceFired)

	return "Empty search spiral detected: " + strings.Join(tips, "\n")
}
