package agent

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/provider"
)

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
			return true
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

func samePath(a, b string) bool {
	if a == b {
		return true
	}
	aAbs := filepath.IsAbs(a)
	bAbs := filepath.IsAbs(b)
	if aAbs != bAbs {
		return true
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

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
