package agent

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/provider"
)

// Conflict-aware partial parallelism for mixed tool batches.
//
// Research basis:
//   - "Parallel Tool Calls in LLM Agents: The Coupling Test You Didn't Know"
//     (tianpan.co, 2026-04): recommends classifying calls by resource coupling
//     and scheduling around conflicts instead of disabling parallelism.
//   - "Parallel Tool Calling and Execution Optimization in AI Agent Systems"
//     (zylos.ai, 2026-04): dependency-graph scheduling - only calls sharing a
//     resource need sequencing; unrelated reads keep their latency win.
//
// The previous guard (#1475-A/#1590-A/#1607-A/#1649/#1829) is all-or-nothing:
// a single mutating tool in a batch drops ALL pre-execution. That was the
// right call before mutation targets were understood, but the code's own
// comment concedes the asymmetry: "FP = lost parallelism only". This file
// recovers the lost parallelism where it is provably safe: a file-scoped
// mutation (edit_file/write_file/multi_edit_file/notebook_edit) only
// invalidates reads whose scan scope covers the mutated path. Reads of
// unrelated paths are pre-executed concurrently as before; the colliding
// reads fall back to the sequential loop where emitted-order semantics are
// preserved (they run after the mutation if the model emitted them after it,
// before it otherwise - exactly the plain serial behavior).
//
// Whole-tree or unknown-scope mutators (shell, delegate, sub-agent spawns,
// warp, git whole-tree ops, undo_edit, file_ops, worktrees) keep the old
// batch-wide skip: their write set is not statically knowable.

// fileScopedMutationPath extracts the single-file write target for mutation
// tools whose effect is bounded to one path. Returns scoped=false for tools
// whose write set is unknown or tree-wide (caller must keep the batch-wide
// skip in that case).
func fileScopedMutationPath(toolName string, args json.RawMessage) (path string, scoped bool) {
	var v struct {
		Path         string `json:"path"`
		FilePath     string `json:"file_path"`
		NotebookPath string `json:"notebook_path"`
	}
	if err := json.Unmarshal(args, &v); err != nil {
		return "", false
	}
	switch toolName {
	case "edit_file", "multi_edit_file":
		if v.FilePath != "" {
			return filepath.Clean(v.FilePath), true
		}
	case "write_file":
		if v.Path != "" {
			return filepath.Clean(v.Path), true
		}
	case "notebook_edit":
		if v.NotebookPath != "" {
			return filepath.Clean(v.NotebookPath), true
		}
	}
	return "", false
}

// readScanScope classifies a read-only call's observation scope.
//
//   - exact: the call observes ONE file (read_file, lsp_*); a mutation only
//     invalidates it when the mutated path equals it.
//   - dir:   the call observes a directory subtree (grep, glob,
//     search_files, list_directory); a mutation invalidates it when the
//     mutated path lives at or under the scanned directory.
//   - tree:  the call observes repository-wide state (git_status/diff/...);
//     any pending mutation invalidates it.
//   - unknown: args unparseable or path missing; conservatively invalidated.
type readScanScope int

const (
	scanUnknown readScanScope = iota
	scanExact
	scanDir
	scanTree
)

func readScanScopeOf(toolName string, args json.RawMessage) (scope readScanScope, exactPath, dirPath string) {
	var v struct {
		Path      string          `json:"path"`
		Directory string          `json:"directory"`
		Files     json.RawMessage `json:"files"`
	}
	if err := json.Unmarshal(args, &v); err != nil {
		return scanUnknown, "", ""
	}
	switch toolName {
	case "read_file":
		if v.Path == "" {
			return scanUnknown, "", ""
		}
		return scanExact, filepath.Clean(v.Path), ""
	case "grep", "search_files", "list_directory", "glob":
		d := v.Path
		if d == "" {
			d = v.Directory
		}
		if d == "" {
			// Defaults to the working directory.
			return scanDir, "", "."
		}
		return scanDir, "", filepath.Clean(d)
	case "multi_file_read":
		if len(v.Files) == 0 {
			return scanUnknown, "", ""
		}
		var objs []struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal(v.Files, &objs); err != nil || len(objs) == 0 {
			return scanUnknown, "", ""
		}
		var paths []string
		for _, f := range objs {
			if f.Path != "" {
				paths = append(paths, filepath.Clean(f.Path))
			}
		}
		if len(paths) == 0 {
			return scanUnknown, "", ""
		}
		return scanExact, strings.Join(paths, "\x00"), ""
	case "lsp_hover", "lsp_definition", "lsp_references", "lsp_symbols",
		"lsp_workspace_symbols", "lsp_diagnostics", "lsp_implementation",
		"lsp_incoming_calls", "lsp_outgoing_calls", "lsp_document_highlights":
		if v.Path == "" {
			return scanUnknown, "", ""
		}
		return scanExact, filepath.Clean(v.Path), ""
	case "git_status", "git_diff", "git_log", "git_branch_list", "git_show", "git_blame":
		return scanTree, "", ""
	}
	return scanUnknown, "", ""
}

// readAffectedByMutation reports whether pre-executing this read-only call
// before the batch's file-scoped mutations land could hand the model stale
// content. When in doubt, it reports affected (the read stays sequential).
func readAffectedByMutation(toolName string, args json.RawMessage, mutatedPaths []string) bool {
	scope, exact, dir := readScanScopeOf(toolName, args)
	switch scope {
	case scanTree, scanUnknown:
		return true
	case scanExact:
		targets := strings.Split(exact, "\x00")
		for _, t := range targets {
			for _, m := range mutatedPaths {
				if samePath(t, m) {
					return true
				}
			}
		}
		return false
	case scanDir:
		if dir == "" || dir == "." {
			return true // scans the whole working directory
		}
		for _, m := range mutatedPaths {
			if samePath(m, dir) || strings.HasPrefix(m, dir+string(filepath.Separator)) {
				return true
			}
		}
		return false
	}
	return true
}

// samePath compares two cleaned paths. An absolute/relative style mismatch is
// treated as a potential collision (conservative) because the relation cannot
// be proven without resolving against the working directory at this layer.
func samePath(a, b string) bool {
	if a == b {
		return true
	}
	aAbs := filepath.IsAbs(a)
	bAbs := filepath.IsAbs(b)
	if aAbs != bAbs {
		return true // unknown relation: assume collision
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

// partitionBatchForParallelism splits the guard loop: it returns the
// file-scoped mutation targets found in the batch, or ok=false when the batch
// contains a whole-tree / unknown-scope mutator (caller must skip
// pre-execution entirely, preserving the #1475-A..#1829 behavior).
func partitionBatchForParallelism(toolCalls []provider.ToolCallDelta) (mutated []string, ok bool) {
	for _, tc := range toolCalls {
		if tc.Name == "run_command" || tc.Name == "start_command" {
			return nil, false
		}
		if tc.Name == "delegate" {
			return nil, false
		}
		if tc.Name == "spawn_agent" || tc.Name == "teammate_spawn" ||
			tc.Name == "send_message" || tc.Name == "swarm_task_create" ||
			tc.Name == "a2a_send_task" || tc.Name == "a2a_remote" {
			return nil, false
		}
		if tc.Name == "warp" {
			return nil, false
		}
		if mutatesSourceTree(tc.Name) {
			p, scoped := fileScopedMutationPath(tc.Name, tc.Arguments)
			if !scoped {
				return nil, false
			}
			mutated = append(mutated, p)
		}
	}
	return mutated, true
}
