package agent

// Tool Argument Precision Decay Detector (Anchor Erosion)
//
// Research basis:
//   - "A Comprehensive Survey of Self-Evolving AI Agents" (arXiv:2508.07407,
//     Aug 2025): identifies capability degradation over long interaction
//     sequences as a key challenge. Agents start with careful, well-specified
//     actions but degrade to imprecise, lower-quality ones as runs progress.
//   - "The Agent Last Mile Failure Problem" (2026): agents maintain quality
//     in early phases but degrade in late phases, with edit precision dropping
//     as context pressure and cognitive load increase.
//   - ACE: Agentic Context Engineering (ICLR 2026, arXiv:2510.04618): under
//     context pressure, agents shed "non-essential" details from tool calls,
//     including the context lines that make edit anchors reliable.
//   - "Measuring AI Ability to Complete Long Software Tasks" (arXiv:2503.14499):
//     per-step reliability degrades over long horizons, with edit argument
//     quality being a primary casualty.
//
// Problem: AI coding agents provide high-quality edit_file anchors early in a
// run (generous old_text with 3-5 surrounding context lines for reliable
// matching). But as the run progresses - especially under context pressure or
// after multiple failures - the agent tends to provide minimal anchors:
// sometimes just the target line itself, or even a single token. This "anchor
// erosion" leads to:
//   1. Higher edit failure rates (non-unique old_text matches)
//   2. More retry cycles (edit fails → re-read → re-edit with more context)
//   3. Greater risk of editing the wrong location (ambiguous match)
//   4. Cascading context waste (each failed edit + retry consumes tokens)
//
// Existing detectors that are RELATED but do NOT cover this:
//   - arg_size_guard.go: checks ABSOLUTE argument oversize (too BIG). This
//     detector checks the OPPOSITE: arguments becoming too SMALL/imprecise.
//   - diminishing_edit.go: tracks edit SUBSTANCE size (how much CODE changes).
//     This detector tracks ANCHOR QUALITY (how well-specified the edit is).
//   - edit_fail_recovery.go: reacts to failures AFTER they happen. This
//     detector is PROACTIVE: warns before precision drops cause failures.
//   - adaptive_effort.go: adjusts reasoning effort, not argument precision.
//
// Approach: Track old_text line counts in edit_file/multi_edit_file calls.
// Establish a baseline from the first few edits. Compare recent edits against
// the baseline. If average precision drops by 50%+ after sufficient data,
// inject a targeted nudge to provide more context lines. Zero LLM cost.

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	// anchorErosionMinBaseline: need at least this many early edits to
	// establish a reliable baseline before comparison is meaningful.
	anchorErosionMinBaseline = 3

	// anchorErosionMinRecent: need at least this many recent edits to
	// confirm the trend isn't a one-off.
	anchorErosionMinRecent = 3

	// anchorErosionWindow: number of recent edits to average for comparison.
	anchorErosionWindow = 5

	// anchorErosionThreshold: if recent avg lines < this fraction of baseline,
	// trigger the warning. 0.5 = 50% drop.
	anchorErosionThreshold = 0.5

	// anchorErosionMinDrop: absolute minimum line drop to trigger (avoid
	// false positives when baseline is already tiny, e.g., 2→1 lines).
	anchorErosionMinDrop = 3.0
)

// anchorErosionState tracks edit anchor precision over the course of a run.
type anchorErosionState struct {
	// baselineAnchorLines: average old_text line count from early edits.
	baselineAnchorLines float64
	baselineCount       int

	// recentAnchorLines: sliding window of recent edit anchor line counts.
	recentAnchorLines []float64

	// fired: whether the warning has already been issued this run.
	fired bool
}

func newAnchorErosionState() *anchorErosionState {
	return &anchorErosionState{
		recentAnchorLines: make([]float64, 0, anchorErosionWindow+1),
	}
}

func (a *anchorErosionState) reset() {
	a.baselineAnchorLines = 0
	a.baselineCount = 0
	a.recentAnchorLines = a.recentAnchorLines[:0]
	a.fired = false
}

// countAnchorLines extracts and counts lines in old_text from edit tool args.
// Returns 0 if no old_text found or args can't be parsed.
func countAnchorLines(args string) float64 {
	// Try JSON parse for structured args
	var fields map[string]interface{}
	if err := json.Unmarshal([]byte(args), &fields); err != nil {
		return 0
	}

	// edit_file: single old_text
	if oldText, ok := fields["old_text"].(string); ok && oldText != "" {
		return float64(strings.Count(oldText, "\n") + 1)
	}

	// multi_edit_file / multi_file_edit: array of edits
	totalLines := 0.0
	for _, key := range []string{"edits", "files"} {
		if arr, ok := fields[key].([]interface{}); ok {
			for _, item := range arr {
				if m, ok := item.(map[string]interface{}); ok {
					if ot, ok := m["old_text"].(string); ok && ot != "" {
						totalLines += float64(strings.Count(ot, "\n") + 1)
					}
					// multi_file_edit nests edits inside file entries
					if nestedEdits, ok := m["edits"].([]interface{}); ok {
						for _, ne := range nestedEdits {
							if nm, ok := ne.(map[string]interface{}); ok {
								if ot, ok := nm["old_text"].(string); ok && ot != "" {
									totalLines += float64(strings.Count(ot, "\n") + 1)
								}
							}
						}
					}
				}
			}
			if totalLines > 0 {
				return totalLines
			}
		}
	}

	return 0
}

// recordEditAnchor tracks an edit's anchor precision. Returns a non-empty
// hint string if precision decay is detected.
func (a *anchorErosionState) recordEditAnchor(toolName, args string) string {
	if toolName != "edit_file" && toolName != "multi_edit_file" && toolName != "multi_file_edit" {
		return ""
	}

	lineCount := countAnchorLines(args)
	if lineCount == 0 {
		return ""
	}

	// Already fired - don't nag.
	if a.fired {
		return ""
	}

	// Build baseline from early edits.
	if a.baselineCount < anchorErosionMinBaseline {
		// Accumulate into baseline using running average.
		a.baselineAnchorLines = (a.baselineAnchorLines*float64(a.baselineCount) + lineCount) / float64(a.baselineCount+1)
		a.baselineCount++
		// Still track in recent window too so we don't lose data.
		a.pushRecent(lineCount)
		return ""
	}

	a.pushRecent(lineCount)

	// Need enough recent data points to confirm the trend.
	if len(a.recentAnchorLines) < anchorErosionMinRecent {
		return ""
	}

	recentAvg := avgFloat64(a.recentAnchorLines)

	// Check for significant drop.
	drop := a.baselineAnchorLines - recentAvg
	if drop < anchorErosionMinDrop {
		return ""
	}

	ratio := recentAvg / a.baselineAnchorLines
	if ratio > anchorErosionThreshold {
		return ""
	}

	// Decay confirmed.
	a.fired = true
	debug.Log("agent", "anchor-erosion: baseline=%.1f lines, recent=%.1f lines (%.0f%% of baseline)",
		a.baselineAnchorLines, recentAvg, ratio*100)

	return fmt.Sprintf(
		"[Anchor precision decay] Your edit anchors have become significantly less precise over this run "+
			"(early edits averaged %.0f context lines, recent edits average %.0f - a %.0f%% drop). "+
			"Shorter old_text anchors lead to ambiguous matches, edit failures, and wrong-location edits. "+
			"Include 3-5 surrounding lines in old_text to ensure reliable, unique matches.",
		a.baselineAnchorLines, recentAvg, (1-ratio)*100,
	)
}

// pushRecent adds a value to the sliding window, evicting the oldest if full.
func (a *anchorErosionState) pushRecent(v float64) {
	a.recentAnchorLines = append(a.recentAnchorLines, v)
	if len(a.recentAnchorLines) > anchorErosionWindow {
		a.recentAnchorLines = a.recentAnchorLines[1:]
	}
}

func avgFloat64(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range vals {
		sum += v
	}
	return sum / float64(len(vals))
}
