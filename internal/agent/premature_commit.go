package agent

import (
	"strconv"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

// Premature Commitment Detector
//
// Research basis: ECLoop (arXiv:2607.28815, "Preventing Premature Commitment in
// Coding Agents with an Evidence-Conditioned Execution Layer") identifies that
// LLM-based coding agents frequently edit source code before examining enough
// repository evidence to justify the change. This "premature commitment" is a
// distinct failure mode from editing the wrong file or exploring too narrowly:
//   - The agent may read the target file (satisfying unread_edit_guard)
//   - The agent may explore broadly (satisfying tunnel_vision)
//   - But it commits to an edit approach without checking related implementations,
//     callers, tests, or broader context that would change the fix
//
// ECLoop shows that gating edits behind evidence sufficiency improves Pass@1 by
// 4.8-11.8 percentage points on SWE-bench Verified while reducing token usage
// by up to 12.1%. The key insight: separating "is this a plausible action" from
// "has the agent gathered enough evidence to commit to it."
//
// Unlike unread_edit_guard (file-level read check) and tunnel_vision (overall
// breadth over many iterations), this detector fires at the FIRST edit and
// checks whether the agent's pre-edit investigation was sufficient:
//   1. Did it do enough exploratory actions (reads, searches)?
//   2. Did it perform at least one SEARCH-type exploration (grep, code_search,
//      lsp_symbols, etc.) - not just direct file reads?
//   3. For multi-file tasks: did it examine files beyond the edit target?
//
// Design:
//   - Tracks exploration actions (read, search, lsp, glob) before first edit.
//   - Fires once at the first edit if evidence is insufficient.
//   - Non-blocking: advisory guidance appended to the edit's tool result.
//   - Zero LLM cost - pure heuristic.
//   - Resets each run.

const (
	// pcMinExplorationCalls is the minimum number of exploratory tool calls
	// before an edit is considered evidence-sufficient. Below this, the agent
	// hasn't investigated enough context.
	pcMinExplorationCalls = 3

	// pcMinSearchCalls is the minimum number of search-type explorations
	// (grep, code_search, lsp_symbols, search_files, etc.). Direct file reads
	// alone are insufficient because the agent may not know what else is relevant.
	pcMinSearchCalls = 1
)

// pcExploratoryTools are tools that gather evidence without modifying state.
var pcExploratoryTools = map[string]bool{
	"read_file":               true,
	"multi_file_read":         true,
	"grep":                    true,
	"search_files":            true,
	"code_search":             true,
	"glob":                    true,
	"list_directory":          true,
	"lsp_symbols":             true,
	"lsp_workspace_symbols":   true,
	"lsp_definition":          true,
	"lsp_references":          true,
	"lsp_hover":               true,
	"lsp_implementation":      true,
	"lsp_document_highlights": true,
	"lsp_incoming_calls":      true,
	"lsp_outgoing_calls":      true,
	"lsp_call_hierarchy":      true,
}

// pcSearchTools are tools that explore the codebase beyond direct file reads.
// These indicate the agent is looking for related context, not just reading
// a file it already knows about.
var pcSearchTools = map[string]bool{
	"grep":                  true,
	"search_files":          true,
	"code_search":           true,
	"glob":                  true,
	"lsp_symbols":           true,
	"lsp_workspace_symbols": true,
	"lsp_references":        true,
	"lsp_implementation":    true,
	"lsp_incoming_calls":    true,
	"lsp_outgoing_calls":    true,
	"list_directory":        true,
}

// prematureCommitState tracks evidence-gathering before the first edit.
type prematureCommitState struct {
	// explorationCount tracks total exploratory tool calls before first edit.
	explorationCount int

	// searchCount tracks search-type explorations before first edit.
	searchCount int

	// filesRead tracks unique files read before first edit (excluding edit targets).
	filesRead map[string]bool

	// warned indicates the detector has fired this run.
	warned bool

	// firstEditDone indicates the agent has made its first edit.
	firstEditDone bool
}

func newPrematureCommitState() *prematureCommitState {
	return &prematureCommitState{
		filesRead: make(map[string]bool),
	}
}

func (s *prematureCommitState) reset() {
	s.explorationCount = 0
	s.searchCount = 0
	s.filesRead = make(map[string]bool)
	s.warned = false
	s.firstEditDone = false
}

// recordExploration tracks an exploratory tool call before the first edit.
func (s *prematureCommitState) recordExploration(toolName string, fileHint string) {
	if s.firstEditDone || s.warned {
		return
	}
	if pcExploratoryTools[toolName] {
		s.explorationCount++
	}
	if pcSearchTools[toolName] {
		s.searchCount++
	}
	if fileHint != "" && (toolName == "read_file" || toolName == "multi_file_read") {
		s.filesRead[normalizeFilePath(fileHint)] = true
	}
}

// checkFirstEdit evaluates evidence sufficiency at the moment of first edit.
// Returns guidance string if premature commitment is detected, "" otherwise.
func (s *prematureCommitState) checkFirstEdit(editPaths []string) string {
	if s.firstEditDone || s.warned {
		return ""
	}
	s.firstEditDone = true

	// Don't fire if exploration was sufficient.
	if s.explorationCount >= pcMinExplorationCalls && s.searchCount >= pcMinSearchCalls {
		return ""
	}

	// Check if the agent read files beyond the edit targets.
	readBeyondEdit := false
	editSet := make(map[string]bool)
	for _, ep := range editPaths {
		editSet[normalizeFilePath(ep)] = true
	}
	for fr := range s.filesRead {
		if !editSet[fr] {
			readBeyondEdit = true
			break
		}
	}

	// If the agent explored at least 2 files beyond the edit target, consider
	// it sufficient even without search tools.
	if readBeyondEdit && len(s.filesRead) >= 3 {
		return ""
	}

	s.warned = true

	debug.Log("agent", "premature-commit: first edit after %d exploration calls, %d search calls", s.explorationCount, s.searchCount)

	var reasons []string
	if s.explorationCount < pcMinExplorationCalls {
		reasons = append(reasons, strings.TrimSpace(strings.Repeat(" ", 0)+
			"only "+strconv.Itoa(s.explorationCount)+" exploratory action(s) before this edit"))
	}
	if s.searchCount < pcMinSearchCalls {
		reasons = append(reasons, "no repository-wide search performed (grep/code_search/lsp) to find related code, callers, or tests")
	}

	msg := "[premature-commitment] First edit attempted after insufficient evidence gathering (" +
		strings.Join(reasons, "; ") +
		"). ECLoop research (arXiv:2607.28815) shows that editing before examining callers, related implementations, and tests leads to incorrect patches in 20-27% of cases. " +
		"Consider: (1) search for callers/references of the symbols you're modifying, " +
		"(2) check existing tests related to this code, " +
		"(3) verify no related implementations or interfaces are affected before committing to this edit approach."

	return msg
}
