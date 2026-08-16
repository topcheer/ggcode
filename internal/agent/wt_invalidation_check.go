package agent

// Working-Tree Invalidation Awareness -- Cross-File State Invalidation Detection
//
// Research basis:
//   - "The Evolution of Tool Use in LLM Agents" (arXiv:2603.22862, 2026):
//     identifies "intermediate state" as a core unsolved dimension in
//     multi-tool orchestration. As agents operate over long trajectories,
//     the environment changes underneath them -- and they lose track of
//     whether their cached observations are still valid.
//   - "Reducing Cost of LLM Agents with Trajectory Reduction" (AgentDiet,
//     arXiv:2509.23586, FSE 2026): shows 39.9%-59.7% of input tokens are
//     waste, with "expired information" (valid-when-produced but now-stale
//     tool output) as a major contributor.
//   - "Before the Tool Call" (arXiv:2603.20953): emphasizes that agents
//     lack mechanisms to track whether their context is consistent with
//     the real world state.
//
// Problem: AI coding agents read files into context (read_file, grep,
// multi_file_read), building a mental model of the codebase. Then they
// issue a git state-changing operation (git_checkout, git_reset,
// git_stash pop/apply, git_pull, git_revert, git_merge) that modifies
// files on disk. After such an operation, the file contents the agent
// "remembers" may no longer match what's on disk. If the agent then
// edits a file based on stale memory, the edit will fail or produce
// incorrect results.
//
// This is distinct from existing detectors:
//   - expired_read_check.go: tracks SELF-invalidation (read → edit same file).
//     This detector tracks CROSS-file invalidation (git op invalidates
//     ALL previously-read files simultaneously).
//   - unread_edit_guard.go: warns when editing a file NOT previously read.
//     This detector warns when a git operation invalidates files that
//     WERE previously read.
//   - search_result_invalidation: tracks search/grep result staleness.
//     This detector tracks file-content staleness from git mutations.
//   - action_annihilate: tracks side-effect cancellation pairs.
//     This detector tracks environment-context divergence.
//
// What it detects: When the agent executes a git state-changing tool call
// after having read files in this run, it warns that all prior file reads
// are potentially stale and should be re-read before editing.
//
// Design:
//   - Zero LLM cost -- pure set tracking + heuristic matching
//   - Fires at most once per run (the warning is cumulative)
//   - Non-blocking: hint appended to tool result, execution proceeds
//   - Tracks both read paths and time-sequence to provide specific guidance

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	// maxWTInvalidationWarnings: cap total warnings per run.
	maxWTInvalidationWarnings = 2

	// minReadsBeforeWarning: minimum number of files read before a git
	// mutation is worth warning about. With 0-1 reads, the blast radius
	// is trivial and re-reading is cheap.
	minReadsBeforeWarning = 2
)

// wtInvalidationState tracks file reads and detects cross-file
// invalidation when git state-changing operations modify the working tree.
type wtInvalidationState struct {
	// readFiles tracks normalized paths of files read in this run.
	// Keyed by path, value is the tool call sequence number of the read.
	readFiles map[string]int

	// warnedCount tracks how many invalidation warnings fired this run.
	warnedCount int

	// lastReadCount tracks the count of reads at the time of the last
	// git mutation, to avoid double-warning if the agent does multiple
	// git ops without new reads in between.
	lastMutationReadCount int
}

// newWTInvalidationState creates a fresh working-tree invalidation tracker.
func newWTInvalidationState() *wtInvalidationState {
	return &wtInvalidationState{
		readFiles: make(map[string]int),
	}
}

// reset clears all tracking state (called at the start of a new user turn).
func (w *wtInvalidationState) reset() {
	w.readFiles = make(map[string]int)
	w.warnedCount = 0
	w.lastMutationReadCount = 0
}

// recordRead tracks a file path read by the agent.
func (w *wtInvalidationState) recordRead(path string) {
	normalized := normalizeWTPath(path)
	if normalized == "" {
		return
	}
	// Only store first read sequence for each path.
	if _, exists := w.readFiles[normalized]; !exists {
		w.readFiles[normalized] = len(w.readFiles) + 1
	}
}

// isWTMutatingTool returns true if the tool name is a git state-changing
// operation that could invalidate previously-read file contents.
func isWTMutatingTool(toolName string) bool {
	switch toolName {
	case "git_checkout", "git_reset", "git_stash", "git_pull",
		"git_revert", "git_merge", "git_rebase", "git_cherry_pick":
		return true
	default:
		return false
	}
}

// readOnlyGitSubcommands are git subcommands that never modify the working
// tree. Their execution must NOT invalidate cached file reads (#544 Bug B).
var readOnlyGitSubcommands = map[string]bool{
	"status": true,
	"list":   true,
	"diff":   true,
	"log":    true,
	"show":   true,
	"branch": true, // guarded further: only without -d/-D/--delete/-m/--move
}

// readOnlyStashActions are git_stash `action` values that only inspect.
var readOnlyStashActions = map[string]bool{
	"list": true,
	"show": true,
}

// wtArgFields unmarshals the tool-arguments JSON object (best-effort;
// malformed JSON yields nil).
func wtArgFields(argsJSON string) map[string]any {
	var m map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &m); err != nil {
		return nil
	}
	return m
}

// isReadOnlyGitInvocation returns true when a mutating-classified git tool
// call is actually read-only per its arguments (#544): checkMutation used to
// discard arguments entirely, so `git stash list` (read-only) invalidated
// every previously-read file. Classification:
//
//   - git_stash: the dedicated `action` field (enum in the tool schema) —
//     list/show inspect, everything else (incl. the schema default push)
//     mutates. The free-text `description` field is never consulted.
//   - other tools: any string field containing a `git` token — the
//     subcommand after `git` decides (status/list/diff/log/show are
//     read-only; `branch` is read-only unless -d/-D/--delete/-m/--move;
//     `stash` is read-only only as `stash list`/`stash show`).
//
// gitBranchMutatingFlags are `git branch` flags that turn a listing command
// into a mutating one.
var gitBranchMutatingFlags = map[string]bool{
	"-d": true, "-D": true, "--delete": true, "-m": true, "--move": true,
}

// classifyGitCommandLine classifies one candidate command line. Returns
// (readOnly, true) when a `git` token with a classifiable subcommand is
// present; (false, false) when the line contains no git command.
func classifyGitCommandLine(line string) (readOnly, found bool) {
	fields := strings.Fields(line)
	for idx := 0; idx < len(fields); idx++ {
		if filepath.Base(fields[idx]) != "git" || idx+1 >= len(fields) {
			continue
		}
		sub := strings.ToLower(fields[idx+1])
		switch {
		case sub == "stash":
			if idx+2 >= len(fields) {
				return false, true // bare `git stash` defaults to push (mutating)
			}
			return readOnlyStashActions[strings.ToLower(fields[idx+2])], true
		case sub == "branch":
			// `git branch` lists; -d/-D/--delete/-m/--move mutate.
			for _, f := range fields[idx+2:] {
				if gitBranchMutatingFlags[f] {
					return false, true
				}
			}
			return true, true
		case readOnlyGitSubcommands[sub]:
			return true, true
		default:
			return false, true // any other git subcommand: not read-only
		}
	}
	return false, false
}

func isReadOnlyGitInvocation(toolName, argsJSON string) bool {
	m := wtArgFields(argsJSON)
	if m == nil {
		return false
	}

	// The executed command line is the most authoritative signal (#544): a
	// schema-default action=push wrapped around an actual `git stash list`
	// command must still classify as read-only. Scan string fields first.
	for _, v := range m {
		s, ok := v.(string)
		if !ok {
			continue
		}
		if readOnly, found := classifyGitCommandLine(s); found {
			return readOnly
		}
	}

	// No git command found in any string field: for git_stash, fall back to
	// the dedicated `action` field (list/show inspect, everything else —
	// including the schema default push — mutates).
	if toolName == "git_stash" {
		if action, ok := m["action"].(string); ok {
			return readOnlyStashActions[strings.ToLower(strings.TrimSpace(action))]
		}
	}
	return false
}

// checkMutation is called after a git state-changing tool call executes.
// If there are enough previously-read files to warrant a warning, it
// returns a guidance message. Otherwise returns "".
func (w *wtInvalidationState) checkMutation(toolName string, argsJSON string) string {
	// #544 Bug B: read-only invocations (e.g. `git stash list`) never
	// invalidate cached reads — the second parameter was previously
	// discarded, misjudging read-only subcommands as mutating.
	if isReadOnlyGitInvocation(toolName, argsJSON) {
		return ""
	}
	if w.warnedCount >= maxWTInvalidationWarnings {
		return ""
	}
	if len(w.readFiles) < minReadsBeforeWarning {
		return ""
	}
	// If no new reads since last mutation warning, don't repeat.
	if len(w.readFiles) <= w.lastMutationReadCount {
		return ""
	}

	w.warnedCount++
	w.lastMutationReadCount = len(w.readFiles)

	// Build a short list of affected paths for the warning.
	var paths []string
	count := 0
	for p := range w.readFiles {
		if count >= 3 {
			break
		}
		paths = append(paths, p)
		count++
	}

	suffix := ""
	if len(w.readFiles) > 3 {
		suffix = fmt.Sprintf(" and %d more", len(w.readFiles)-3)
	}

	debug.Log("wt_invalidation", "git mutation %s invalidated %d cached reads", toolName, len(w.readFiles))

	toolList := strings.Join(paths, ", ")
	return fmt.Sprintf(
		"[Working-Tree Invalidation] The tool %s may have changed files on disk. "+
			"Your cached reads of %d file(s) are now potentially stale: %s%s. "+
			"Re-read files before editing to avoid operating on outdated content.",
		toolName, len(w.readFiles), toolList, suffix,
	)
}

// extractWTReadPath and extractMultiReadPaths were removed (#500): the
// agent loop records reads via extractFilePathsFromArgs (batch-aware),
// which covers read_file and every file in multi_file_read's files array.

// normalizeWTPath normalizes a file path for deduplication.
func normalizeWTPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	// Remove leading ./
	path = strings.TrimPrefix(path, "./")
	return path
}
