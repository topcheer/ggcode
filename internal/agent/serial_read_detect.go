package agent

import (
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

var serialReadOnlyTools = map[string]bool{
	"read_file":               true,
	"multi_file_read":         true,
	"grep":                    true,
	"search_files":            true,
	"glob":                    true,
	"list_directory":          true,
	"code_search":             true,
	"git_status":              true,
	"git_log":                 true,
	"git_diff":                true,
	"git_show":                true,
	"git_blame":               true,
	"git_branch_list":         true,
	"git_remote":              true,
	"git_stash_list":          true,
	"lsp_symbols":             true,
	"lsp_hover":               true,
	"lsp_definition":          true,
	"lsp_references":          true,
	"lsp_document_highlights": true,
	"lsp_workspace_symbols":   true,
	"lsp_implementation":      true,
	"lsp_diagnostics":         true,
	"lsp_code_actions":        true,
	"lsp_incoming_calls":      true,
	"lsp_outgoing_calls":      true,
	"dep_graph":               true,
	"code_health":             true,
	"scan_todos":              true,
	"web_search":              true,
	"web_fetch":               true,
}

type serialReadState struct {
	mu sync.Mutex

	consecutiveSingleReads int

	currentTurnReadOnly int

	currentTurnHasMutation bool

	lspStreak int

	currentTurnTool string

	fired bool
}

func newSerialReadState() *serialReadState {
	return &serialReadState{}
}

func (s *serialReadState) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.consecutiveSingleReads = 0
	s.currentTurnReadOnly = 0
	s.currentTurnHasMutation = false
	s.lspStreak = 0
	s.currentTurnTool = ""
	s.fired = false
}

var serialMutatingTools = map[string]bool{
	"edit_file": true, "multi_edit_file": true, "multi_file_edit": true,
	"write_file": true, "multi_file_write": true, "batch_replace": true,
	"file_ops": true, "notebook_edit": true, "undo_edit": true,
	"run_command": true, "start_command": true, "stop_command": true,
	"write_command_input": true,
	"git_add":             true, "git_commit": true, "git_checkout": true,
	"git_revert": true, "git_reset": true, "git_stash": true,
	"git_tag": true, "lsp_rename": true,
	"scaffold_project": true, "enter_worktree": true, "teammate_shutdown": true,
	"team_delete": true,
}

func (s *serialReadState) recordToolCall(toolName string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if serialReadOnlyTools[toolName] {
		s.currentTurnReadOnly++
		if s.currentTurnTool == "" {
			s.currentTurnTool = toolName
		}
	} else if serialMutatingTools[toolName] {
		s.currentTurnHasMutation = true
	}
}

func (s *serialReadState) endTurn(iteration int) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.currentTurnReadOnly == 1 && !s.currentTurnHasMutation {
		s.consecutiveSingleReads++
		if strings.HasPrefix(s.currentTurnTool, "lsp_") {
			s.lspStreak++
		} else {
			s.lspStreak = 0
		}
	} else {
		s.consecutiveSingleReads = 0
		s.lspStreak = 0
	}

	s.currentTurnReadOnly = 0
	s.currentTurnHasMutation = false
	s.currentTurnTool = ""

	if s.consecutiveSingleReads >= 3 && !s.fired {
		if s.lspStreak >= s.consecutiveSingleReads {
			debug.Log("agent", "Serial read: skipping fire - streak is the mandated LSP serial chain (iteration %d)", iteration)
			return ""
		}
		s.fired = true
		debug.Log("agent", "Serial read serialization detected: %d consecutive single-read turns at iteration %d", s.consecutiveSingleReads, iteration)
		return strings.TrimSpace(`
[Batch Opportunity] You have issued exactly one read-only tool call per turn for the last 3+ turns. These independent reads could have been batched into a single turn with parallel tool calls, saving 2+ LLM round-trips and reducing context fragmentation. When you need to explore multiple independent targets (files, searches, symbols), issue them together in one turn. Reserve single-call turns for dependent operations where each result informs the next call.`)
	}

	return ""
}
