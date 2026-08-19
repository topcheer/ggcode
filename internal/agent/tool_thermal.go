package agent

// Tool Thermal Profile - Cross-Tool Usage Balance Monitor
//
// Research: "ACE: Agentic Context Engineering" (Zhang et al., ICLR 2026)
// and "Trajectory-Aware Agent Optimization" (TAAS, 2025) both identify that
// an agent's tool-call composition is a leading indicator of task efficiency.
//
// Key insight: healthy agent trajectories show a balanced mix of exploration
// (read, grep, search), modification (edit, write), and verification (build,
// test, git). When one category dominates - especially exploration - it signals
// the agent is "spinning" without making progress. This is distinct from:
//
//   - Budget guard: tracks per-step token COST escalation (intra-step)
//   - Loop detector: catches exact duplicate calls (exact-match)
//   - Confidence scorer: holistic quality score (success-rate based)
//   - Overseer drift: counts iterations without productive action (binary)
//
// (The following commented-out comparison block was removed; see git history.)
//
// Thermal profile tracks the DISTRIBUTION of tool calls across categories:
//
// The profile fires a warning when:
//   1. Enough total calls exist for a meaningful sample (>= 12)
//   2. Exploration calls exceed explorationThreshold (55%) of total calls
//   3. Modification calls are below modificationFloor (15%) of total calls
//      (agent is reading but not producing changes)
//   4. No warning has been given yet this run
//
// This is a zero-LLM-cost heuristic — pure counter-based, O(1) per call.

import (
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	// thermalMinSamples: need at least this many tool calls before evaluating.
	thermalMinSamples = 12

	// explorationThreshold: fraction of total calls that is "too much" reading.
	// At 55%, the agent is spending more than half its turns gathering context
	// without making changes — a common inefficiency pattern.
	explorationThreshold = 0.55

	// modificationFloor: below this fraction of modification calls alongside
	// high exploration is a strong "all read, no write" signal.
	modificationFloor = 0.15

	// thermalWarnCooldown: minimum iterations between thermal warnings for
	// different overload categories. Prevents repeated noise.
	thermalWarnCooldown = 8
)

// thermalCategory classifies tools into behavioral groups.
type thermalCategory int

const (
	thermalExplore thermalCategory = iota
	thermalModify
	thermalExecute
	thermalVerify
	thermalOther
)

// thermalCategoryNames maps categories to display names.
var thermalCategoryNames = map[thermalCategory]string{
	thermalExplore: "exploration",
	thermalModify:  "modification",
	thermalExecute: "execution",
	thermalVerify:  "verification",
	thermalOther:   "other",
}

// toolCategoryMap maps tool names to thermal categories.
var toolCategoryMap = map[string]thermalCategory{
	// Exploration (information gathering)
	"read_file":                  thermalExplore,
	"multi_file_read":            thermalExplore,
	"search_files":               thermalExplore,
	"grep":                       thermalExplore,
	"glob":                       thermalExplore,
	"list_directory":             thermalExplore,
	"code_search":                thermalExplore,
	"code_execution":             thermalExplore,
	"lsp_symbols":                thermalExplore,
	"lsp_definition":             thermalExplore,
	"lsp_references":             thermalExplore,
	"lsp_hover":                  thermalExplore,
	"lsp_workspace_symbols":      thermalExplore,
	"lsp_document_highlights":    thermalExplore,
	"lsp_implementation":         thermalExplore,
	"lsp_prepare_call_hierarchy": thermalExplore,
	"lsp_incoming_calls":         thermalExplore,
	"lsp_outgoing_calls":         thermalExplore,
	"lsp_code_actions":           thermalExplore,
	"web_search":                 thermalExplore,
	"web_fetch":                  thermalExplore,

	// Modification (code changes) -- canonical sourceMutatingTools members
	// (#738): edit_file, write_file, multi_edit_file, multi_file_edit,
	// multi_file_write, batch_replace, lsp_rename, file_ops, notebook_edit.
	"edit_file":        thermalModify,
	"write_file":       thermalModify,
	"multi_edit_file":  thermalModify,
	"multi_file_edit":  thermalModify,
	"multi_file_write": thermalModify,
	"batch_replace":    thermalModify,
	"lsp_rename":       thermalModify,
	"file_ops":         thermalModify,
	"notebook_edit":    thermalModify,

	// Execution (side-effect operations)
	"run_command":         thermalExecute,
	"start_command":       thermalExecute,
	"write_command_input": thermalExecute,
	"stop_command":        thermalExecute,

	// Verification (validation checks)
	"git_diff":        thermalVerify,
	"git_status":      thermalVerify,
	"git_log":         thermalVerify,
	"git_show":        thermalVerify,
	"git_blame":       thermalVerify,
	"lsp_diagnostics": thermalVerify,
	"code_health":     thermalVerify,
	"scan_todos":      thermalVerify,
}

// thermalState tracks per-run tool call distribution by category.
type thermalState struct {
	// Per-category call counts
	categories [5]int // indexed by thermalCategory

	// Total calls recorded
	total int

	// Iteration of last warning (for cooldown)
	lastWarnIter int

	// Whether any warning has been given this run
	warned bool
}

func newThermalState() *thermalState {
	return &thermalState{
		lastWarnIter: -thermalWarnCooldown, // allow first warning immediately
	}
}

func (t *thermalState) reset() {
	for i := range t.categories {
		t.categories[i] = 0
	}
	t.total = 0
	t.lastWarnIter = -thermalWarnCooldown
	t.warned = false
}

// recordToolCall classifies and records a tool call.
func (t *thermalState) recordToolCall(toolName string) {
	cat, ok := toolCategoryMap[toolName]
	if !ok {
		cat = thermalOther
	}
	t.categories[cat]++
	t.total++
}

// fraction returns the proportion of calls in the given category.
func (t *thermalState) fraction(cat thermalCategory) float64 {
	if t.total == 0 {
		return 0
	}
	return float64(t.categories[cat]) / float64(t.total)
}

// maybeWarn checks whether the tool distribution shows an unhealthy pattern
// and returns guidance text if so. iteration is the current 0-based loop index.
//
// Two detection modes:
//  1. Explore-heavy: exploration > 55% AND modification < 15% (all read, no write)
//  2. Verify-heavy: verification > 30% (excessive checking, not enough doing)
//
// Fires at most once per category per cooldown window.
func (t *thermalState) maybeWarn(iteration int) string {
	if t.total < thermalMinSamples {
		return ""
	}

	exploreFrac := t.fraction(thermalExplore)
	modifyFrac := t.fraction(thermalModify)
	verifyFrac := t.fraction(thermalVerify)

	// Mode 1: Explore-heavy (most common inefficiency)
	if exploreFrac > explorationThreshold && modifyFrac < modificationFloor {
		if iteration-t.lastWarnIter < thermalWarnCooldown {
			return ""
		}
		t.lastWarnIter = iteration
		debug.Log("thermal-profile", "explore-heavy: explore=%.0f%% modify=%.0f%% total=%d",
			exploreFrac*100, modifyFrac*100, t.total)

		return fmt.Sprintf(
			"[tool balance] Exploration calls dominate (%.0f%% of %d calls) with only %.0f%% modification. "+
				"You may be over-reading. Apply what you've already learned: make changes, then verify. "+
				"Avoid re-reading files you've already inspected this run.",
			exploreFrac*100, t.total, modifyFrac*100,
		)
	}

	// Mode 2: Verify-heavy (excessive checking)
	if verifyFrac > 0.30 && modifyFrac < modificationFloor {
		if iteration-t.lastWarnIter < thermalWarnCooldown {
			return ""
		}
		t.lastWarnIter = iteration
		debug.Log("thermal-profile", "verify-heavy: verify=%.0f%% modify=%.0f%% total=%d",
			verifyFrac*100, modifyFrac*100, t.total)

		return fmt.Sprintf(
			"[tool balance] Verification calls high (%.0f%% of %d) with minimal changes (%.0f%%). "+
				"Make your edits first, then verify once. Repeated checking without changes wastes iterations.",
			verifyFrac*100, t.total, modifyFrac*100,
		)
	}

	return ""
}

// categoryBreakdown returns a human-readable summary of the current distribution.
// Used for debugging and potential future TUI display.
func (t *thermalState) categoryBreakdown() string {
	if t.total == 0 {
		return "no tool calls recorded"
	}
	var parts []string
	for cat := thermalExplore; cat <= thermalOther; cat++ {
		if t.categories[cat] > 0 {
			parts = append(parts, fmt.Sprintf("%s=%d(%.0f%%)",
				thermalCategoryNames[cat], t.categories[cat], t.fraction(cat)*100))
		}
	}
	return strings.Join(parts, " ")
}
