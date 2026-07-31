package agent

import (
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

// AdaptiveEffort automatically adjusts reasoning effort per LLM turn based on
// the complexity of recent tool interactions. This implements the per-request
// effort toggle pattern pioneered by Claude Opus 5 and GPT-5.5, where routine
// operations (file reads, searches) use lower effort to reduce cost and latency,
// while complex operations (code edits, error recovery, planning) use higher
// effort for better quality.
//
// The adapter only activates when the user has NOT explicitly set a reasoning
// effort. When the user sets effort via /effort or config, that setting always
// wins and the adapter stays dormant.
//
// Effort classification heuristics (simple, deterministic — no LLM calls):
//   - Error recovery: recent edit failures or repeated errors → high effort
//   - Code editing: file edits in recent turns → medium effort
//   - Exploration: only reads/searches → low effort
//   - Default: no data yet → empty (provider default)

const (
	// adaptiveEffortWindow controls how many recent tool interactions to
	// consider when classifying the current effort context. A small window
	// keeps the adapter responsive to context shifts (e.g. from exploration
	// to editing) without over-weighting stale history.
	adaptiveEffortWindow = 6
)

// toolComplexity classifies how much reasoning a tool typically needs.
type toolComplexity int

const (
	complexityLow    toolComplexity = iota // read-only, search, status
	complexityMedium                       // file edits, git operations
	complexityHigh                         // error recovery, verification
)

// effortReadOnlyTools are tools that only read state — no mutations. After a
// run of these, the next LLM turn can safely use lower effort. Extended from
// overseer's readOnlyTools with additional status/metadata tools.
var effortReadOnlyTools = map[string]bool{
	"read_file":                  true,
	"read_file_range":            true,
	"multi_file_read":            true,
	"search_files":               true,
	"grep":                       true,
	"glob":                       true,
	"list_directory":             true,
	"list_dir":                   true,
	"git_status":                 true,
	"git_diff":                   true,
	"git_log":                    true,
	"git_show":                   true,
	"git_blame":                  true,
	"git_branch_list":            true,
	"git_remote":                 true,
	"git_stash_list":             true,
	"lsp_symbols":                true,
	"lsp_hover":                  true,
	"lsp_references":             true,
	"lsp_definition":             true,
	"lsp_diagnostics":            true,
	"code_search":                true,
	"code_health":                true,
	"web_search":                 true,
	"web_fetch":                  true,
	"runtime":                    true,
	"todo_write":                 true,
	"task_list":                  true,
	"task_get":                   true,
	"debug_log":                  true,
	"lsp_workspace_symbols":      true,
	"lsp_implementation":         true,
	"lsp_code_actions":           true,
	"lsp_prepare_call_hierarchy": true,
	"lsp_incoming_calls":         true,
	"lsp_outgoing_calls":         true,
	"lsp_document_highlights":    true,
}

// editTools are tools that modify files — higher stakes, benefit from more reasoning.
var editTools = map[string]bool{
	"edit_file":        true,
	"write_file":       true,
	"multi_edit_file":  true,
	"multi_file_edit":  true,
	"multi_file_write": true,
	"notebook_edit":    true,
}

// errorRecoverySignals are tools or patterns that indicate the agent is
// recovering from a failure — high effort helps avoid repeating mistakes.
var errorRecoverySignals = map[string]bool{
	"edit_file":       true, // edit retry after failure
	"multi_edit_file": true,
	"multi_file_edit": true,
}

// effortEntry records a single tool interaction for effort classification.
type effortEntry struct {
	toolName string
	isError  bool
}

// adaptiveEffortState tracks recent tool interactions and recommends a
// reasoning effort level for the next LLM turn.
type adaptiveEffortState struct {
	mu              sync.Mutex
	entries         []effortEntry // ring of recent tool results
	userOverrideSet bool          // true when user explicitly set effort via slash/config
}

func newAdaptiveEffortState() *adaptiveEffortState {
	return &adaptiveEffortState{}
}

// recordToolResult appends a tool interaction to the sliding window.
func (s *adaptiveEffortState) recordToolResult(toolName string, isError bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = append(s.entries, effortEntry{toolName: toolName, isError: isError})
	if len(s.entries) > adaptiveEffortWindow {
		s.entries = s.entries[len(s.entries)-adaptiveEffortWindow:]
	}
}

// setUserOverride marks that the user has explicitly set effort — the adapter
// should stay dormant.
func (s *adaptiveEffortState) setUserOverride(set bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.userOverrideSet = set
}

// hasUserOverride returns whether the user has explicitly set effort.
func (s *adaptiveEffortState) hasUserOverride() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.userOverrideSet
}

// reset clears the window for a new user turn.
func (s *adaptiveEffortState) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = s.entries[:0]
}

// recommendedEffort analyzes recent tool interactions and returns the
// recommended reasoning effort level for the next LLM turn.
//
// Returns "" (empty) when no adaptation is needed — the provider's default
// applies. Returns "low", "medium", or "high" otherwise.
func (s *adaptiveEffortState) recommendedEffort() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.userOverrideSet {
		return ""
	}
	if len(s.entries) == 0 {
		return ""
	}

	// Count patterns in the window.
	recentErrors := 0
	editCount := 0
	readOnlyCount := 0

	for _, e := range s.entries {
		if e.isError {
			recentErrors++
			continue
		}
		if editTools[e.toolName] {
			editCount++
		} else if effortReadOnlyTools[e.toolName] {
			readOnlyCount++
		}
	}

	// Decision priority:
	// 1. Error recovery → high effort (the agent needs to think carefully
	//    about what went wrong and how to fix it).
	// 2. Active editing → medium effort (edits benefit from more reasoning
	//    to avoid mistakes, but don't need the full thinking budget).
	// 3. Pure exploration → low effort (reading/searching is routine; save
	//    tokens and latency).
	// 4. Mixed/unknown → empty (let the provider use its default).

	if recentErrors > 0 {
		return "high"
	}
	if editCount > 0 {
		return "medium"
	}
	// If all recent tools were read-only, use low effort.
	if readOnlyCount == len(s.entries) {
		return "low"
	}
	return ""
}

// applyAdaptiveEffort checks whether adaptive effort should override the
// provider's current effort for this turn, and applies it if so. Returns
// the effort that was applied (or "" if no change was made) and the
// previous effort so it can be restored.
//
// This is called before each streamChatResponse in the agent loop.
func (a *Agent) applyAdaptiveEffort() (applied string, previous string) {
	if a.effortAdapter == nil {
		return "", ""
	}
	// Skip if user has an explicit override.
	if a.effortAdapter.hasUserOverride() {
		return "", ""
	}

	recommended := a.effortAdapter.recommendedEffort()
	if recommended == "" {
		return "", ""
	}

	// Get the provider's current effort so we can restore it after the call.
	p, ok := a.provider.(interface {
		ReasoningEffort() string
	})
	if !ok {
		return "", ""
	}
	previous = p.ReasoningEffort()

	// Only apply if the recommendation differs from the current setting.
	if recommended == previous {
		return "", previous
	}

	if a.SetReasoningEffort(recommended) {
		debug.Log("adaptive-effort", "adjusted effort: %s → %s", previous, recommended)
		return recommended, previous
	}
	return "", ""
}

// restoreEffort restores the provider's reasoning effort to a previous value
// after an adaptive adjustment.
func (a *Agent) restoreEffort(previous string) {
	if previous == "" {
		return
	}
	a.SetReasoningEffort(previous)
	debug.Log("adaptive-effort", "restored effort to: %s", previous)
}

// effortLevelDisplayName returns a user-friendly name for the current effort.
func effortLevelDisplayName(effort string) string {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "low":
		return "Low (fast)"
	case "medium":
		return "Medium (balanced)"
	case "high":
		return "High (thorough)"
	default:
		return "Auto"
	}
}
