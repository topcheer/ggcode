package agent

// Exploration Fragmentation Detector -- Scattered Foraging Awareness
//
// Research basis:
//   - AgentDiet (FSE 2026, arXiv:2509.23586): "useless, redundant, and expired
//     information is widespread across trajectories." One key pattern is
//     exploration waste: many small tool calls that individually return little
//     value and collectively fail to build a coherent understanding. The agent
//     "forages" across the codebase without converging on a target.
//   - InferAct (EMNLP 2025, Fang et al.): misaligned actions often stem from
//     incomplete mental models. When the agent doesn't understand the codebase
//     structure, it issues many unfocused exploration calls -- a symptom of
//     reasoning misalignment with the actual problem structure.
//   - Information Foraging Theory (Pirolli & Card): optimal information
//     seeking follows a "patch model" -- explore one area thoroughly, then
//     move on. Fragmented exploration (many calls across many files/dirs
//     without depth) indicates the agent lacks a coherent search strategy.
//
// Problem: AI coding agents sometimes enter a "scattered foraging" mode where
// they issue many narrow exploration tool calls across different files and
// directories without converging on a target. For example:
//
//   iter 1: read_file("a.go", offset=1, limit=20)     // tiny read
//   iter 2: grep("pattern", path="dir1")               // different dir
//   iter 3: read_file("b.go", offset=50, limit=10)     // another tiny read
//   iter 4: glob("*.py")                                // different file type
//   iter 5: search_files("query", dir="dir2")           // yet another dir
//   iter 6: read_file("c.go", offset=100, limit=5)     // minimal read
//
// Each call individually is cheap, but collectively they waste context budget
// and signal the agent doesn't have a coherent understanding of where the
// relevant code lives. This is distinct from:
//   - empty_search_spiral: detects searches that return NO results
//   - serial_read: detects sequential reads of the SAME file
//   - tool_diversity_gate: checks tool variety, not exploration coherence
//   - wasted_explore: tracks exploration without subsequent action
//
// What it detects: When the agent issues N+ exploration tool calls (read_file,
// grep, search_files, glob, lsp_*) across M+ distinct files/directories within
// a sliding window of K iterations, WITHOUT any converging action (edit, write,
// run_command). This scattered pattern indicates the agent is "panicked
// foraging" rather than systematically exploring.
//
// Design:
//   - Sliding window of recent exploration calls (tool name + target path)
//   - Counts unique targets and total exploration calls
//   - Triggers when both threshold (unique targets AND total calls) are exceeded
//   - Resets on any mutating tool call (edit_file, write_file, run_command)
//   - Zero LLM cost, deterministic, max 2 warnings per run

import (
	"fmt"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	// exploreFragWindow: sliding window size for exploration calls.
	exploreFragWindow = 8

	// exploreFragMinCalls: minimum exploration calls in window to trigger.
	exploreFragMinCalls = 6

	// exploreFragMinUniqueTargets: minimum unique targets to indicate scattering.
	exploreFragMinUniqueTargets = 5

	// exploreFragMaxWarnings: max warnings per run.
	exploreFragMaxWarnings = 2
)

// explorationToolNames identifies read-only exploration tools.
var explorationFragToolNames = map[string]bool{
	"read_file":               true,
	"grep":                    true,
	"search_files":            true,
	"glob":                    true,
	"list_directory":          true,
	"code_search":             true,
	"lsp_symbols":             true,
	"lsp_hover":               true,
	"lsp_definition":          true,
	"lsp_references":          true,
	"lsp_workspace_symbols":   true,
	"lsp_implementation":      true,
	"lsp_document_highlights": true,
	"lsp_incoming_calls":      true,
	"lsp_outgoing_calls":      true,
	"multi_file_read":         true,
}

// mutatingToolNamesFrag identifies tools that indicate convergence (the agent
// found what it was looking for and is now acting).
// Derived from the canonical sourceMutatingTools superset plus command/git
// side-effect tools (#738).
var mutatingToolNamesFrag = derivedEditTools(map[string]bool{
	"run_command":   true,
	"start_command": true,
	"git_commit":    true,
	"git_add":       true,
	"git_checkout":  true,
})

// exploreFragEntry records a single exploration tool call.
type exploreFragEntry struct {
	tool      string
	target    string // extracted file path, directory, or search query
	iteration int
}

// exploreFragState tracks exploration calls for fragmentation detection.
type exploreFragState struct {
	mu       sync.Mutex
	entries  []exploreFragEntry
	warnings int
}

func newExploreFragState() *exploreFragState {
	return &exploreFragState{
		entries: make([]exploreFragEntry, 0, exploreFragWindow+2),
	}
}

func (s *exploreFragState) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = s.entries[:0]
	// Do NOT reset warnings -- max per run
}

// extractTarget pulls a meaningful target identifier from tool arguments.
func extractExploreTarget(toolName string, args []byte) string {
	argsStr := string(args)
	if argsStr == "" {
		return ""
	}

	// Try JSON extraction for common fields
	// We do lightweight string search to avoid full JSON parsing overhead.
	// Look for "path", "file", "directory", "pattern", "query", "url" fields.

	// path field (read_file, glob, list_directory, grep, etc.)
	if val := extractExploreJSONField(argsStr, "path"); val != "" {
		return val
	}
	// file_path field (edit_file style)
	if val := extractExploreJSONField(argsStr, "file_path"); val != "" {
		return val
	}
	// directory field (search_files, list_directory)
	if val := extractExploreJSONField(argsStr, "directory"); val != "" {
		return val
	}
	// pattern field (grep, glob, search_files)
	if val := extractExploreJSONField(argsStr, "pattern"); val != "" {
		return "pattern:" + val
	}
	// query field (code_search, lsp_workspace_symbols)
	if val := extractExploreJSONField(argsStr, "query"); val != "" {
		return "query:" + val
	}

	// Fallback: first 60 chars of args
	if len(argsStr) > 60 {
		return argsStr[:60]
	}
	return argsStr
}

// extractExploreJSONField does a lightweight scan for "field":"value" in JSON.
func extractExploreJSONField(jsonStr, field string) string {
	needle := "\"" + field + "\":\""
	idx := strings.Index(jsonStr, needle)
	if idx < 0 {
		// Also try with space after colon
		needle = "\"" + field + "\": \""
		idx = strings.Index(jsonStr, needle)
		if idx < 0 {
			return ""
		}
	}
	start := idx + len(needle)
	end := strings.IndexByte(jsonStr[start:], '"')
	if end < 0 {
		return ""
	}
	return jsonStr[start : start+end]
}

// recordToolCall checks if this tool call contributes to fragmentation.
// Returns a non-empty warning string if fragmentation is detected.
func (s *exploreFragState) recordToolCall(toolName string, args []byte, iteration int) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	// If this is a mutating tool, reset the exploration window --
	// the agent has converged and started acting.
	if mutatingToolNamesFrag[toolName] {
		s.entries = s.entries[:0]
		return ""
	}

	// Only track exploration tools
	if !explorationFragToolNames[toolName] {
		return ""
	}

	// Already warned enough
	if s.warnings >= exploreFragMaxWarnings {
		return ""
	}

	target := extractExploreTarget(toolName, args)
	s.entries = append(s.entries, exploreFragEntry{
		tool:      toolName,
		target:    target,
		iteration: iteration,
	})

	// Trim to window size (keep most recent)
	if len(s.entries) > exploreFragWindow {
		s.entries = s.entries[len(s.entries)-exploreFragWindow:]
	}

	// Check if we have enough entries to evaluate
	if len(s.entries) < exploreFragMinCalls {
		return ""
	}

	// Count unique targets
	uniqueTargets := make(map[string]bool, len(s.entries))
	for _, e := range s.entries {
		if e.target != "" {
			uniqueTargets[e.target] = true
		}
	}

	// Trigger if scattered across many unique targets
	if len(uniqueTargets) < exploreFragMinUniqueTargets {
		return ""
	}

	s.warnings++
	debug.Log("agent", "Iteration %d: exploration fragmentation detected (%d calls, %d unique targets)",
		iteration, len(s.entries), len(uniqueTargets))

	return fmt.Sprintf(
		"[Exploration Fragmentation] %d exploration tool calls across %d+ distinct targets "+
			"in the last %d iterations without any converging action (edit, write, command). "+
			"This scattered foraging pattern suggests the codebase mental model is incomplete. "+
			"Consider: (1) Use code_search or lsp_workspace_symbols for semantic discovery instead of many narrow reads. "+
			"(2) Read a key file fully (without offset/limit) to understand structure before exploring specifics. "+
			"(3) If you have enough context, start acting (edit/build) rather than exploring further.",
		len(s.entries), len(uniqueTargets), exploreFragWindow)
}
