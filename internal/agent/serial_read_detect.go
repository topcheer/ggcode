package agent

import (
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

// Sequential Read Serialization Detector -- Inter-Iteration Read Batching Awareness
//
// Research basis:
//   - "Speculative Actions: A Lossless Framework for Faster AI Agents"
//     (arXiv:2510.04371, 2025): introduces speculative execution where faster
//     models predict likely future actions and execute them in parallel. The
//     core insight: many agent actions are independent and could be parallelized,
//     but agents default to sequential execution, wasting latency and iterations.
//   - "A Self-Improving Coding Agent" (SICA, arXiv:2504.15228, NeurIPS 2025):
//     trajectory waste is the primary bottleneck. Each unnecessary iteration
//     costs tokens, time, and degrades output quality via context bloat.
//   - Anthropic "Context Engineering for AI Agents" (2025): recommends batching
//     independent read operations to reduce round-trips and context fragmentation.
//   - OpenAI function calling best practices (2025): encourage parallel tool
//     calls for independent operations.
//
// Problem: AI coding agents often serialize independent read-only exploration
// across turns:
//
//	Turn 1: read_file("foo.go")        → 1 tool call, waits for response
//	Turn 2: grep("pattern", "bar/")    → 1 tool call, waits for response
//	Turn 3: git_log()                  → 1 tool call, waits for response
//
// Each of these turns could have been a single turn with 3 parallel tool calls.
// The agent wastes 2 extra iterations (extra LLM round-trips, extra context
// tokens, extra latency) for what should have been one batched turn.
//
// This is NOT about single-call efficiency (handled by tool_sequence.go for
// ordering). This is about CROSS-TURN serialization: the model choosing to issue
// one read per turn instead of batching multiple reads in a single turn.
//
// Distinct from existing detectors:
//   - tool_sequence.go: detects suboptimal ORDERING within a single turn (e.g.
//     list_directory before glob on same dir). Serial read detects CROSS-TURN
//     single-read patterns (one read per turn, N turns in a row).
//   - tool_call_storm: detects bursts of diverse tools without reasoning. Serial
//     read detects the OPPOSITE: too FEW tools per turn (undersubscription).
//   - futile_cycle.go / wasted_explore: detect reads that yield no action. Serial
//     read detects reads that ARE useful but should have been batched.
//   - tool_diversity: tracks unique tool variety. Serial read tracks READ COUNT
//     PER TURN (exactly 1 when more was possible).

// serialReadOnlyTools lists tools that are pure reads and safe to batch.
// Mutating tools (edit_file, run_command, git_commit) are excluded because
// batching those would change semantics.
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

// serialReadState tracks per-turn tool call counts to detect cross-turn
// serialization of read-only calls that could have been batched.
type serialReadState struct {
	mu sync.Mutex

	// consecutiveSingleReads counts how many consecutive turns had exactly
	// 1 read-only tool call and zero mutating calls.
	consecutiveSingleReads int

	// currentTurnReadOnly counts read-only calls in the current turn.
	currentTurnReadOnly int

	// currentTurnHasMutation tracks whether the current turn included any
	// mutating tool call. If so, the turn is not a "single read" turn.
	currentTurnHasMutation bool

	// fired tracks whether the detector has already fired this run.
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
	s.fired = false
}

// serialMutatingTools lists tools that MUTATE state — a call to any of
// these legitimately breaks the read-batching streak. #465: the old
// else-branch treated every non-whitelisted tool as a mutation, so
// todo_write/ask_user/task_*/skill/cron_*/MCP reads reset the streak and
// the batch-opportunity guidance never fired for explorer agents.
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

// recordToolCall is called once per tool call within the current turn.
// Unknown tools (MCP, rarely used built-ins) are NEUTRAL per #465: they
// neither advance the read streak nor reset it (fail-open).
func (s *serialReadState) recordToolCall(toolName string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if serialReadOnlyTools[toolName] {
		s.currentTurnReadOnly++
	} else if serialMutatingTools[toolName] {
		s.currentTurnHasMutation = true
	}
}

// endTurn is called when all tool calls in the current turn have been processed.
// It updates the consecutive streak and returns a guidance hint if the
// serialization threshold is met.
func (s *serialReadState) endTurn(iteration int) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	// A "single read" turn: exactly 1 read-only call, zero mutations.
	if s.currentTurnReadOnly == 1 && !s.currentTurnHasMutation {
		s.consecutiveSingleReads++
	} else {
		s.consecutiveSingleReads = 0
	}

	// Reset per-turn counters for the next turn.
	s.currentTurnReadOnly = 0
	s.currentTurnHasMutation = false

	// Fire once per run when 3+ consecutive single-read turns are detected.
	if s.consecutiveSingleReads >= 3 && !s.fired {
		s.fired = true
		debug.Log("agent", "Serial read serialization detected: %d consecutive single-read turns at iteration %d", s.consecutiveSingleReads, iteration)
		return strings.TrimSpace(`
[Batch Opportunity] You have issued exactly one read-only tool call per turn for the last 3+ turns. These independent reads could have been batched into a single turn with parallel tool calls, saving 2+ LLM round-trips and reducing context fragmentation. When you need to explore multiple independent targets (files, searches, symbols), issue them together in one turn. Reserve single-call turns for dependent operations where each result informs the next call.`)
	}

	return ""
}
