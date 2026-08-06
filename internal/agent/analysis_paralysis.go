package agent

import (
	"fmt"
	"strings"
	"sync"
)

// analysisParalysisState detects when the agent is stuck in prolonged
// read-only exploration loops without making any modifications — a failure
// mode documented in SICA (Self-Improving Coding Agent, arXiv 2504.15228)
// and Agent-R iterative self-training research. The agent reads, searches,
// and inspects endlessly without ever taking action.
//
// This is distinct from tool diversity (which checks category balance)
// and verification debt (which tracks unverified edits). Analysis paralysis
// specifically catches the exploration-heavy / action-starved imbalance
// where NO modification tools have been called at all.
type analysisParalysisState struct {
	mu sync.Mutex

	totalCalls   int
	modifyCalls  int // edit, write, commit, etc.
	exploreCalls int // read, search, grep, glob, lsp, etc.
	verifyCalls  int // build, test, lint, etc.

	// Sliding window of recent tool categories (for consecutive-run detection)
	recentWindow []string // "explore" | "modify" | "verify" | "other"
	windowSize   int

	warnCount   int // how many times we've warned this run
	maxWarnings int
	fired       bool
}

func newAnalysisParalysisState() *analysisParalysisState {
	return &analysisParalysisState{
		recentWindow: make([]string, 0, 16),
		windowSize:   12,
		maxWarnings:  2,
	}
}

// apReadOnlyTools are pure exploration tools — they gather information
// without changing any state. Overlaps partially with effortReadOnlyTools
// but curated for the analysis-paralysis signal.
var apReadOnlyTools = map[string]bool{
	"read_file":               true,
	"read_file_range":         true,
	"multi_file_read":         true,
	"search_files":            true,
	"grep":                    true,
	"glob":                    true,
	"list_directory":          true,
	"list_dir":                true,
	"git_status":              true,
	"git_diff":                true,
	"git_log":                 true,
	"git_show":                true,
	"git_blame":               true,
	"git_branch_list":         true,
	"git_remote":              true,
	"git_stash_list":          true,
	"lsp_symbols":             true,
	"lsp_hover":               true,
	"lsp_references":          true,
	"lsp_definition":          true,
	"lsp_diagnostics":         true,
	"lsp_implementation":      true,
	"lsp_workspace_symbols":   true,
	"lsp_code_actions":        true,
	"lsp_document_highlights": true,
	"lsp_incoming_calls":      true,
	"lsp_outgoing_calls":      true,
	"code_search":             true,
	"code_execution":          true, // read-only sandbox
	"web_search":              true,
	"web_fetch":               true,
	"code_health":             true,
	"scan_todos":              true,
	"dep_graph":               true,
	"review_changes":          true,
	"task_list":               true,
	"task_get":                true,
	"list_mcp_capabilities":   true,
	"read_mcp_resource":       true,
}

var apModifyTools = map[string]bool{
	"edit_file":        true,
	"multi_edit_file":  true,
	"multi_file_edit":  true,
	"write_file":       true,
	"multi_file_write": true,
	"batch_replace":    true,
	"notebook_edit":    true,
	"file_ops":         true,
	"git_add":          true,
	"git_commit":       true,
	"git_checkout":     true,
	"git_revert":       true,
	"git_reset":        true,
	"git_stash":        true,
	"git_tag":          true,
}

var apVerifyTools = map[string]bool{
	"run_command":   true,
	"start_command": true,
	"stop_command":  true,
}

// recordCall classifies a tool call and updates state.
func (s *analysisParalysisState) recordCall(toolName string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.totalCalls++

	category := "other"
	if apReadOnlyTools[toolName] {
		category = "explore"
		s.exploreCalls++
	} else if apModifyTools[toolName] {
		category = "modify"
		s.modifyCalls++
	} else if apVerifyTools[toolName] {
		category = "verify"
		s.verifyCalls++
	}

	// Slide window
	s.recentWindow = append(s.recentWindow, category)
	if len(s.recentWindow) > s.windowSize {
		s.recentWindow = s.recentWindow[len(s.recentWindow)-s.windowSize:]
	}
}

// check returns a guidance message if analysis paralysis is detected.
// Conditions (any one triggers):
//  1. No modify calls after N consecutive explore calls (pure exploration)
//  2. Explore-to-modify ratio exceeds threshold with high absolute count
//  3. All recent window entries are "explore" with no modifications at all
func (s *analysisParalysisState) check() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.fired || s.warnCount >= s.maxWarnings {
		return ""
	}

	// Need at least 8 total calls before judging
	if s.totalCalls < 8 {
		return ""
	}

	var msg string

	// Condition 1: Long consecutive explore streak with zero modifications
	if s.modifyCalls == 0 && s.exploreCalls >= 8 {
		msg = s.buildMessage(s.exploreCalls, "zero modifications")
	}

	// Condition 2: All entries in recent window are explore, no modify/verify
	if msg == "" && len(s.recentWindow) >= s.windowSize {
		allExplore := true
		for _, c := range s.recentWindow {
			if c != "explore" {
				allExplore = false
				break
			}
		}
		if allExplore {
			msg = s.buildMessage(s.exploreCalls, "recent window all-explore")
		}
	}

	// Condition 3: Extreme explore-to-modify ratio
	if msg == "" && s.modifyCalls > 0 && s.exploreCalls >= 12 {
		ratio := float64(s.exploreCalls) / float64(s.modifyCalls)
		if ratio >= 10.0 {
			msg = s.buildMessage(s.exploreCalls, fmt.Sprintf("%.1fx explore-to-modify ratio", ratio))
		}
	}

	if msg != "" {
		s.warnCount++
		if s.warnCount >= s.maxWarnings {
			s.fired = true
		}
	}

	return msg
}

func (s *analysisParalysisState) buildMessage(exploreCount int, reason string) string {
	return fmt.Sprintf(
		"[Analysis Paralysis] %d exploration calls detected with %s. "+
			"You have been reading, searching, and inspecting extensively without taking action. "+
			"Research shows prolonged exploration without modification is a leading indicator of trajectory failure "+
			"(SICA arXiv:2504.15228, Agent-R self-training). "+
			"PAUSE exploring. ACT NOW: make your best-guess edit to the most relevant file, "+
			"then verify with a build/test. You likely already have enough context to proceed.",
		exploreCount, reason,
	)
}

// reset clears state for a new user turn.
func (s *analysisParalysisState) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.totalCalls = 0
	s.modifyCalls = 0
	s.exploreCalls = 0
	s.verifyCalls = 0
	s.recentWindow = s.recentWindow[:0]
	s.warnCount = 0
	s.fired = false
}

// summary returns a compact string for logging/debugging.
func (s *analysisParalysisState) summary() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return fmt.Sprintf("total=%d explore=%d modify=%d verify=%d warns=%d",
		s.totalCalls, s.exploreCalls, s.modifyCalls, s.verifyCalls, s.warnCount)
}

// dedup ensures the message is unique vs nearby messages
func (s *analysisParalysisState) dedup(other string) bool {
	return strings.Contains(other, "[Analysis Paralysis]")
}
