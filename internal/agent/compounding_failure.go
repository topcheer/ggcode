package agent

// Compounding Failure Detection — Sliding-Window Cross-Tool Failure Rate
//
// Research basis: AgentDebug (arXiv:2509.25370) and trajectory analysis from
// SWE-bench studies show that agents often fail across MULTIPLE different tools
// in an interleaved pattern: an edit fails, then a search returns nothing, then
// a build fails, then another edit fails - with occasional read_file successes
// interspersed. The existing errorStreakCheck (loop_detect.go) only tracks
// CONSECUTIVE errors and resets to 0 on any success. This means:
//
//   fail, fail, SUCCEED, fail, fail, SUCCEED, fail, fail
//
// ...never triggers error-streak guidance (max consecutive = 2), despite a 75%
// failure rate that clearly indicates a fundamentally wrong approach.
//
// Competitor approaches:
//   - Claude Code: no rolling failure-rate detection (relies on consecutive streaks)
//   - Cursor: no runtime failure pattern analysis (user-driven correction)
//   - OpenHands: separate "critic" LLM evaluates trajectory (costs tokens)
//   - Devin: SICA overseer tracks productive vs. unproductive steps, but not
//     cross-tool failure diversity
//
// Our approach: deterministic sliding-window failure-rate analysis. Zero LLM cost.
// Maintains a rolling window of the last N tool call results (success/failure)
// plus the set of distinct failure categories observed. When the failure rate
// exceeds a threshold AND multiple distinct categories are involved, injects a
// "strategy reset" message — a stronger intervention than the consecutive-streak
// guidance because it fires on patterns that consecutive detection structurally
// cannot catch.
//
// Key design decisions:
//   - Window size = 10: large enough to smooth noise, small enough to react quickly
//   - Threshold = 70%: 7+ failures in 10 calls is clearly a systemic problem
//   - Multi-category requirement: ensures the failures are diverse (not just the
//     same tool failing repeatedly, which recurring_error.go already handles)
//   - Fires at most once per run (advisory, doesn't block)
//   - Resets per run (consistent with all other monitoring systems)

import (
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	// compoundingWindow: number of recent tool calls to evaluate.
	compoundingWindow = 10

	// compoundingFailThreshold: minimum failure rate (0.0-1.0) in the window
	// to trigger compounding-failure guidance. 0.70 = 7/10 failures.
	compoundingFailThreshold = 0.70

	// compoundingMinCategories: minimum number of distinct failure categories
	// in the window. This ensures the failures are diverse - if all failures
	// are the same tool/category, that's handled by edit_fail_recovery or
	// recurring_error instead.
	compoundingMinCategories = 2
)

// toolCategory maps a tool name to a broad category for failure-diversity analysis.
// When failures span multiple categories, it indicates a systemic approach problem
// rather than a single tool issue.
func toolCategory(toolName string) string {
	switch {
	case toolName == "edit_file" || toolName == "multi_edit_file" || toolName == "multi_file_edit" || toolName == "write_file" || toolName == "notebook_edit":
		return "editing"
	case toolName == "run_command" || toolName == "start_command":
		return "command"
	case toolName == "grep" || toolName == "search_files" || toolName == "code_search" || toolName == "glob":
		return "search"
	case toolName == "read_file" || toolName == "multi_file_read" || toolName == "list_directory":
		return "reading"
	case strings.HasPrefix(toolName, "lsp_"):
		return "lsp"
	case strings.HasPrefix(toolName, "git_"):
		return "git"
	default:
		return "other"
	}
}

// compoundingFailureState tracks tool call results in a sliding window to detect
// high failure rates across diverse tool categories — a pattern indicating a
// fundamentally wrong approach that intermittent successes mask from
// consecutive-error detection.
type compoundingFailureState struct {
	// window is a ring buffer of (toolName, isError) for the last N calls.
	window []compoundingEntry

	// failedCategories accumulates the set of distinct categories that had
	// failures within the current window. Reset when the guidance fires.
	failedCategories map[string]bool

	// fired tracks whether guidance has been injected this run.
	fired bool
}

type compoundingEntry struct {
	toolName string
	isError  bool
	category string
}

func newCompoundingFailureState() *compoundingFailureState {
	return &compoundingFailureState{
		failedCategories: make(map[string]bool),
	}
}

func (c *compoundingFailureState) reset() {
	c.window = c.window[:0]
	c.failedCategories = make(map[string]bool)
	c.fired = false
}

// recordResult adds a tool call result to the sliding window. If the result is
// an error, the tool's category is added to failedCategories. When the window
// exceeds compoundingWindow entries, the oldest is evicted (and its category
// may be removed from failedCategories if no other entries in the window share it).
func (c *compoundingFailureState) recordResult(toolName string, isError bool) {
	entry := compoundingEntry{
		toolName: toolName,
		isError:  isError,
		category: toolCategory(toolName),
	}

	// Evict oldest entry if window is full.
	if len(c.window) >= compoundingWindow {
		old := c.window[0]
		c.window = c.window[1:]
		// If the evicted entry was an error, check if its category still has
		// other failures in the window. If not, remove it.
		if old.isError {
			stillPresent := false
			for _, e := range c.window {
				if e.isError && e.category == old.category {
					stillPresent = true
					break
				}
			}
			if !stillPresent {
				delete(c.failedCategories, old.category)
			}
		}
	}

	c.window = append(c.window, entry)
	if isError {
		c.failedCategories[entry.category] = true
	}
}

// check evaluates the sliding window and returns guidance if the compounding
// failure pattern is detected. Returns empty string otherwise.
func (c *compoundingFailureState) check() string {
	if c.fired {
		return ""
	}

	// Need a full window to evaluate meaningfully.
	if len(c.window) < compoundingWindow {
		return ""
	}

	failCount := 0
	for _, e := range c.window {
		if e.isError {
			failCount++
		}
	}

	failRate := float64(failCount) / float64(len(c.window))
	if failRate < compoundingFailThreshold {
		return ""
	}

	categoryCount := len(c.failedCategories)
	if categoryCount < compoundingMinCategories {
		return ""
	}

	c.fired = true

	// Build a list of failed categories for the guidance message.
	cats := make([]string, 0, categoryCount)
	for cat := range c.failedCategories {
		cats = append(cats, cat)
	}

	debug.Log("compounding-failure",
		"sliding-window failure rate %.0f%% (%d/%d) across %d categories [%s] - strategy reset",
		failRate*100, failCount, len(c.window), categoryCount, strings.Join(cats, ", "))

	return fmt.Sprintf(
		"[strategy-reset] %d/%d tool calls failed (%.0f%%) across %d areas: %s. Change approach: re-read files, find root cause.",
		failCount, len(c.window), failRate*100, categoryCount, strings.Join(cats, ", "),
	)
}
