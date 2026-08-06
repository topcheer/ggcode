package agent

// Tool Diversity Stagnation Detector
//
// Research basis:
//   - "Fast, slow, and metacognitive thinking in AI" (Nature, s44387-025-00027-5):
//     Metacognitive knowledge includes self-assessment of strategies. When an agent
//     over-relies on one strategy (one tool type), it's failing to assess its approach.
//   - SRMA / "Trajectory-Informed Memory Generation" (arXiv:2603.10600): agents
//     "repeat inefficient patterns" -- detectable via tool-type concentration.
//   - AgentForesight (arXiv:2605.08715): "Online Auditing for Early Failure Prediction"
//     -- tool-type imbalance is a leading indicator of trajectory failure.
//
// What it detects: When one tool CATEGORY (edit, search, command, read) dominates
// recent calls beyond a threshold, the agent is likely stuck in a strategy that
// isn't working. For example:
//   - 8+ edits in last 10 calls with no build/test → blindly editing
//   - 6+ searches in last 10 calls with no reads → searching without acting
//   - 6+ commands in last 10 calls with no reads → shell-reliant debugging
//
// This is different from loopDetector (identical calls), convergenceLock
// (post-verify edit drift), or compoundingFailure (cross-tool failure rate).
// It catches STRATEGY IMBALANCE -- over-concentration in one tool category.
//
// Zero LLM cost. Non-blocking. Fires at most once per run.

import (
	"fmt"
	"strings"
)

const (
	// diversityWindowSize: sliding window of recent tool calls to analyze.
	diversityWindowSize = 10

	// diversityDominanceThreshold: if one category accounts for >= this fraction
	// of the window, trigger guidance.
	diversityDominanceThreshold = 0.7

	// diversityMinCalls: need at least this many total calls before analyzing.
	diversityMinCalls = 8

	// diversityCategoryMin: the dominant category must have at least this many
	// calls in the window (prevents triggering on small windows).
	diversityCategoryMin = 7
)

// diversityToolCategory classifies a tool into a broad strategy category.
func diversityToolCategory(toolName string) string {
	switch {
	case isEditTool(toolName):
		return "edit"
	case isSearchTool(toolName):
		return "search"
	case isReadTool(toolName):
		return "read"
	case isCommandTool(toolName):
		return "command"
	case isVerifyTool(toolName):
		return "verify"
	default:
		return "other"
	}
}

func isSearchTool(name string) bool {
	switch name {
	case "grep", "search_files", "code_search", "glob", "lsp_workspace_symbols",
		"lsp_symbols", "find_references", "lsp_references":
		return true
	}
	return false
}

func isReadTool(name string) bool {
	switch name {
	case "read_file", "multi_file_read", "read_command_output", "list_directory",
		"lsp_hover", "lsp_definition", "lsp_implementation", "git_show",
		"git_diff", "git_log", "git_status", "git_blame":
		return true
	}
	return false
}

func isCommandTool(name string) bool {
	switch name {
	case "run_command", "start_command", "wait_command", "write_command_input",
		"stop_command":
		return true
	}
	return false
}

func isVerifyTool(name string) bool {
	switch name {
	case "review_changes", "code_health", "dep_graph", "scan_todos",
		"lsp_diagnostics", "ci_status":
		return true
	}
	return false
}

// diversityState tracks the sliding window of tool categories.
type diversityState struct {
	window     []string // recent tool categories
	fired      bool     // guidance already injected this run
	totalCalls int
}

func newDiversityState() *diversityState {
	return &diversityState{
		window: make([]string, 0, diversityWindowSize+1),
	}
}

func (d *diversityState) reset() {
	d.window = d.window[:0]
	d.fired = false
	d.totalCalls = 0
}

func (d *diversityState) recordCall(toolName string) {
	d.totalCalls++
	cat := diversityToolCategory(toolName)
	d.window = append(d.window, cat)
	if len(d.window) > diversityWindowSize {
		d.window = d.window[1:]
	}
}

// check returns non-empty guidance if one category dominates the window.
func (d *diversityState) check() string {
	if d.fired || d.totalCalls < diversityMinCalls {
		return ""
	}
	if len(d.window) < diversityWindowSize {
		return ""
	}

	// Count categories in window.
	counts := make(map[string]int)
	for _, cat := range d.window {
		counts[cat]++
	}

	// Find dominant category.
	var dominantCat string
	var dominantCount int
	for cat, cnt := range counts {
		if cnt > dominantCount {
			dominantCat = cat
			dominantCount = cnt
		}
	}

	fraction := float64(dominantCount) / float64(len(d.window))
	if fraction < diversityDominanceThreshold || dominantCount < diversityCategoryMin {
		return ""
	}

	d.fired = true
	return d.formatGuidance(dominantCat, dominantCount, len(d.window))
}

func (d *diversityState) formatGuidance(cat string, count, total int) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("[Tool Diversity Alert] %d of your last %d tool calls "+
		"were in the '%s' category. ", count, total, cat))

	switch cat {
	case "edit":
		sb.WriteString("You are editing heavily without adequate exploration or verification. ")
		sb.WriteString("Consider: (1) run a build/test to check if your edits work, ")
		sb.WriteString("(2) read the files you're about to edit next to avoid breaking dependencies, ")
		sb.WriteString("(3) search for related code that may need updating. ")
		sb.WriteString("Blind sequential editing without verification is a leading indicator of cascading failures.")
	case "search":
		sb.WriteString("You are searching extensively without reading results or making changes. ")
		sb.WriteString("Consider: (1) read the files you've found, ")
		sb.WriteString("(2) start implementing based on what you've found, ")
		sb.WriteString("(3) if searches return nothing, try a different search strategy or tool. ")
		sb.WriteString("Prolonged searching without action suggests the query strategy isn't working.")
	case "command":
		sb.WriteString("You are running many shell commands without reading files or inspecting code. ")
		sb.WriteString("Consider using native tools (read_file, grep, lsp_diagnostics) for richer, structured output. ")
		sb.WriteString("Shell-reliant debugging misses context that native tools provide.")
	case "read":
		sb.WriteString("You are reading extensively without making changes. ")
		sb.WriteString("Consider: (1) start implementing based on what you've learned, ")
		sb.WriteString("(2) if unsure where to edit, search for the specific symbol, ")
		sb.WriteString("(3) create a plan if the task is complex. ")
		sb.WriteString("Excessive reading without action may indicate uncertainty about the approach.")
	default:
		sb.WriteString("Your tool usage is heavily concentrated. ")
		sb.WriteString("Diversify your approach: mix exploration, editing, and verification.")
	}

	return sb.String()
}
