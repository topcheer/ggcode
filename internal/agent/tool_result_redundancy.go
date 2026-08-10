package agent

// Tool Result Redundancy Detector
//
// Research basis: AgentDiet (arXiv 2509.23586, "Improving the Efficiency of
// LLM Agent Systems through Trajectory Reduction") identifies "redundant
// information" as a primary source of trajectory waste: "Duplicate or
// near-duplicate content across tool calls. If two queries returned overlapping
// records, only the unique content needs to be retained." AgentDiet showed
// 39.9-59.7% input token reduction by pruning redundant/useless/expired info.
//
// The Alignment for Efficient Tool Calling paper (arXiv 2503.06708) identifies
// "over-tool-reliance" - models invoke tools even when they possess sufficient
// information already in context. Both failure modes share a root cause: the
// agent re-gathers information it already has from prior tool results.
//
// Problem: AI coding agents frequently make tool calls whose results
// substantially overlap with earlier tool results still in context. Examples:
//   - Reading file A, then grep'ing the same file for a pattern (the grep
//     output is a subset of what was already read)
//   - Running git_log then git_show on a commit whose diff was already shown
//     in the log output
//   - Calling lsp_references then grep for the same symbol name
//   - Reading a file then reading it again via a different path or with
//     overlapping offset ranges
//   - Running two searches that return overlapping result sets
//
// Each redundant call wastes: output tokens (generating the call), wall-clock
// latency (waiting for the tool), and input tokens at every subsequent step
// (the duplicate result stays in context). Over a multi-step trajectory this
// compounds significantly.
//
// Gap: No existing detector tracks CONTENT-LEVEL overlap between tool results:
//   - wasted_explore: tracks search → file-path utilization (paths only, not content)
//   - redundant_read: detects exact same file re-read (path match, not content overlap)
//   - futile_cycle: detects circular read working-sets between writes (set pattern)
//   - loop_detect: detects exact argument match loops
//   - tool_output_guard: handles result SIZE, not duplication
//
// Approach: Normalize each substantial tool result to a set of distinctive
// lines (stripped, deduplicated, non-trivial). Compute Jaccard similarity
// against a rolling window of prior result line-sets. If overlap exceeds a
// threshold, inject a targeted nudge to use information already in context.
// Zero LLM cost, deterministic.

import (
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	// trMinLines: minimum lines in a result to make overlap comparison
	// meaningful. Results with fewer lines are too small to be worth tracking.
	trMinLines = 5

	// trOverlapThreshold: Jaccard similarity threshold for declaring results
	// "redundant". 0.6 means 60% of the union is shared content.
	trOverlapThreshold = 0.6

	// trMaxWarnings: cap warnings per run to avoid noise.
	trMaxWarnings = 2

	// trWindowSize: number of recent results to retain for comparison.
	trWindowSize = 8

	// trMaxLineLen: lines longer than this are truncated before storage to
	// bound memory and focus on meaningful code/content lines.
	trMaxLineLen = 200
)

// toolResultEntry stores a normalized line-set from a prior tool result.
type toolResultEntry struct {
	toolName string
	lines    map[string]bool
	iter     int
}

// toolResultRedundancyState tracks tool results for content overlap detection.
type toolResultRedundancyState struct {
	entries        []toolResultEntry
	warningsFired  int
	lastWarnedIter int
}

func newToolResultRedundancyState() *toolResultRedundancyState {
	return &toolResultRedundancyState{}
}

func (t *toolResultRedundancyState) reset() {
	t.entries = nil
	t.warningsFired = 0
	t.lastWarnedIter = 0
}

// recordResult processes a tool result and checks for redundancy with prior results.
// Returns a guidance message if significant overlap is detected, "" otherwise.
func (t *toolResultRedundancyState) recordResult(toolName, content string, iteration int) string {
	if t.warningsFired >= trMaxWarnings {
		t.storeEntry(toolName, content, iteration)
		return ""
	}

	lines := trNormalize(content)
	if len(lines) < trMinLines {
		t.storeEntry(toolName, content, iteration)
		return ""
	}

	// Check against prior entries.
	for _, entry := range t.entries {
		if len(entry.lines) < trMinLines {
			continue
		}
		jaccard := trJaccard(lines, entry.lines)
		if jaccard >= trOverlapThreshold {
			// Avoid re-warning on consecutive iterations.
			if iteration == t.lastWarnedIter && t.warningsFired > 0 {
				t.storeEntry(toolName, content, iteration)
				return ""
			}
			t.warningsFired++
			t.lastWarnedIter = iteration
			t.storeEntry(toolName, content, iteration)

			debug.Log("tool_result_redundancy",
				"warning at iter %d: %s overlaps prior %s (Jaccard=%.2f)",
				iteration, toolName, entry.toolName, jaccard)

			return fmt.Sprintf(
				"[tool-result-redundancy] '%s' overlaps (%.0f%%) with prior '%s' call. Use existing context instead of re-fetching.",
				toolName, jaccard*100, entry.toolName,
			)
		}
	}

	t.storeEntry(toolName, content, iteration)
	return ""
}

// storeEntry adds a result entry to the rolling window.
func (t *toolResultRedundancyState) storeEntry(toolName, content string, iteration int) {
	lines := trNormalize(content)
	if len(lines) < trMinLines {
		return // don't track tiny results
	}
	t.entries = append(t.entries, toolResultEntry{
		toolName: toolName,
		lines:    lines,
		iter:     iteration,
	})
	if len(t.entries) > trWindowSize {
		t.entries = t.entries[1:]
	}
}

// trNormalize converts tool result content into a set of distinctive lines.
// Strips whitespace, skips trivially short lines, and deduplicates.
func trNormalize(content string) map[string]bool {
	lines := make(map[string]bool)
	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(raw)
		if len(line) < 4 {
			continue // skip blank/decorative lines
		}
		if len(line) > trMaxLineLen {
			line = line[:trMaxLineLen]
		}
		lines[line] = true
	}
	return lines
}

// trJaccard computes the Jaccard similarity between two line-sets.
func trJaccard(a, b map[string]bool) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	intersection := 0
	for k := range a {
		if b[k] {
			intersection++
		}
	}
	union := len(a) + len(b) - intersection
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}
