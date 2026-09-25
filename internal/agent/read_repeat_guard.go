package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

// Read-repeat loop guard (r91, "Infinite Agentic Loops" complement).
//
// Research basis: "When Agents Do Not Stop: Uncovering Infinite Agentic Loops
// in LLM Agents" (arXiv:2607.01641, 2026) identifies unbounded non-progress
// tool repetition as a structural failure mode of agentic systems; community
// harnesses converge on the same deterministic guard (github.com/Ayubjon/
// loopstop, github.com/matantsach/heartbeat, "Agent Stuck Detection" Claude
// Code skill) — pattern-match the trajectory at the harness seam and hand the
// model a corrective nudge, at zero LLM cost.
//
// Scope: the sibling tool_dedup.go ledger is deliberately MUTATING-only
// ("read-only tools are exempt entirely"): replaying a cached read is unsafe
// (external writers change files between calls), so this guard never
// suppresses or replays anything. For information reads the waste is not a
// duplicated side effect but a burned agent iteration: re-issuing the exact
// same grep/read_file/glob with no state change in between returns the same
// bytes and consumes a full LLM round-trip to absorb them. Observed in
// production sessions (one long research run regressed to 11.4x its baseline
// iteration count driven by redundant reads and searches).
//
// Design:
//   - Only INFORMATIONAL (read-only, non-polling) tools are tracked; anything
//     in the mutating set — or a shell command that may rewrite the workspace
//     — bumps a mutation counter instead (reusing tool_dedup's classifiers).
//   - Advisory fires on the Nth identical occurrence (same tool, same
//     canonical arguments — key-order-insensitive JSON) ONLY when the
//     mutation counter is unchanged since the previous identical occurrence:
//     an edit/command in between makes the re-read legitimate (verify loop).
//   - The call EXECUTES normally and the fresh result is returned; the guard
//     appends a budgeted [read-repeat] hint telling the model to act on data
//     it already has. Advisory-only => no stale-data risk, no semantic change.
//   - Polling tools (wait_command, task_output, read_command_output) are
//     exempt: identical repeats are their normal usage pattern.
//
// Kill switch: GGCODE_READ_REPEAT_GUARD=0 disables the guard entirely
// (mirrors GGCODE_TOOL_DEDUP).

const (
	// readRepeatAdvisoryThreshold: identical occurrence number at which the
	// advisory first fires (2 = the first re-issue of an identical read).
	readRepeatAdvisoryThreshold = 2
	// readRepeatMaxAdvisories caps total hints per session so a pathological
	// loop cannot bloat every tool result with repeated guidance.
	readRepeatMaxAdvisories = 6
	// readRepeatMaxEntries bounds tracked signatures (bounded memory).
	readRepeatMaxEntries = 256
)

// informationalToolNames: read-only tools whose identical repeat with no
// intervening state change is near-certainly redundant. Deliberately
// excludes polling tools (wait_command, task_output, read_command_output,
// a2a_get_task) where identical repeats are the normal usage pattern.
var informationalToolNames = map[string]bool{
	"read_file":               true,
	"multi_file_read":         true,
	"grep":                    true,
	"search_files":            true,
	"glob":                    true,
	"list_directory":          true,
	"code_search":             true,
	"web_search":              true,
	"web_fetch":               true,
	"git_status":              true,
	"git_diff":                true,
	"git_log":                 true,
	"git_show":                true,
	"git_blame":               true,
	"git_branch_list":         true,
	"git_stash_list":          true,
	"list_worktree":           true,
	"read_mcp_resource":       true,
	"lsp_diagnostics":         true,
	"lsp_hover":               true,
	"lsp_definition":          true,
	"lsp_references":          true,
	"lsp_implementation":      true,
	"lsp_document_highlights": true,
	"lsp_symbols":             true,
	"lsp_workspace_symbols":   true,
}

type readRepeatEntry struct {
	count    int    // total identical occurrences observed
	mutEpoch uint64 // mutation-counter value at the most recent occurrence
	lastSeq  uint64 // call sequence number of the most recent occurrence
}

type readRepeatGuard struct {
	mu        sync.Mutex
	mutations uint64 // bumped by every mutating / workspace-rewriting call
	seq       uint64 // monotonic tool-call sequence number
	advised   int    // total advisories emitted (capped)
	table     map[string]*readRepeatEntry
}

func newReadRepeatGuard() *readRepeatGuard {
	return &readRepeatGuard{table: make(map[string]*readRepeatEntry, 16)}
}

// repeatGuard returns the agent's guard. NewAgent creates it eagerly; the
// Once evaluates the GGCODE_READ_REPEAT_GUARD kill switch exactly once and
// also covers Agent literals built without NewAgent (field stays nil ->
// guard off; observe has a nil receiver check).
func (a *Agent) repeatGuard() *readRepeatGuard {
	a.readRepeatOnce.Do(func() {
		if os.Getenv("GGCODE_READ_REPEAT_GUARD") == "0" {
			a.readRepeatGuard = nil
		}
	})
	return a.readRepeatGuard
}

// observe records one tool call and returns an advisory hint when the call is
// an identical repeat of a previously observed read with no intervening
// state change. Mutating calls bump the mutation counter and never advise.
func (g *readRepeatGuard) observe(name string, args []byte) string {
	if g == nil {
		return ""
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.seq++
	if isMutatingTool(name) || commandMayRewriteWorkspace(name, string(args)) {
		g.mutations++
		return ""
	}
	if !informationalToolNames[name] {
		return ""
	}
	sig := readRepeatSignature(name, args)
	e, ok := g.table[sig]
	if !ok {
		if len(g.table) < readRepeatMaxEntries {
			g.table[sig] = &readRepeatEntry{count: 1, mutEpoch: g.mutations, lastSeq: g.seq}
		}
		return ""
	}
	e.count++
	prevEpoch, prevSeq := e.mutEpoch, e.lastSeq
	e.mutEpoch, e.lastSeq = g.mutations, g.seq
	if e.count < readRepeatAdvisoryThreshold || g.mutations != prevEpoch || g.advised >= readRepeatMaxAdvisories {
		return ""
	}
	g.advised++
	hint := fmt.Sprintf(
		"[read-repeat] This is identical information read #%d of %q with the same arguments, and no state-changing call happened in between (the previous identical call was %d calls ago), so the result will match what you already have. Act on the data you already collected (edit, build, decide) instead of re-reading; if you need fresh state, make your intended change first, or vary the query scope.",
		e.count, name, g.seq-prevSeq)
	debug.Log("agent", "read-repeat: advisory #%d for %s (identical call #%d)", g.advised, name, e.count)
	return hint
}

// readRepeatSignature hashes tool name + canonical arguments. Arguments are
// canonicalized through a JSON round-trip so two repeats that differ only in
// key order or whitespace share one signature; non-JSON args fall back to the
// raw bytes.
func readRepeatSignature(name string, args []byte) string {
	canonical := string(args)
	var v interface{}
	if err := json.Unmarshal(args, &v); err == nil {
		if b, err := json.Marshal(v); err == nil {
			canonical = string(b)
		}
	}
	sum := sha256.Sum256([]byte(name + "\x00" + canonical))
	return hex.EncodeToString(sum[:])
}
