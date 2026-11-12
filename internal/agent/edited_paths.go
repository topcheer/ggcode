package agent

import (
	"encoding/json"

	"github.com/topcheer/ggcode/internal/provider"
)

// extractEditedPaths returns the source paths a tool call touches, for
// index/churn tracking. Extracted from agent.go and decomposed per tool
// family (#5521 PR3 seam; each branch body verbatim, behavior unchanged).
func extractEditedPaths(tc provider.ToolCallDelta) []string {
	if len(tc.Arguments) == 0 {
		return nil
	}
	var args map[string]any
	if json.Unmarshal(tc.Arguments, &args) != nil {
		return nil
	}
	args = normalizeApplyPatchArgs(tc.Name, args) // #5321: nested apply_patch edits feed index/churn tracking
	var paths []string
	switch tc.Name {
	case "write_file", "edit_file", "multi_edit_file", "lsp_rename":
		paths = append(paths, editedPathsFromSingleFileArgs(args)...)
	case "multi_file_edit", "multi_file_write":
		paths = append(paths, editedPathsFromFileList(args)...)
	case "apply_patch": // #5321: normalized above; nested form feeds index/churn like the rest
		paths = append(paths, editedPathsFromSingleFileArgs(args)...)
	case "batch_replace":
		paths = append(paths, editedPathsFromBatchReplace(args)...)
	case "file_ops":
		paths = append(paths, editedPathsFromFileOps(args)...)
	case "notebook_edit":
		paths = append(paths, editedPathsFromNotebook(args)...)
	}
	return paths
}

func editedPathsFromSingleFileArgs(args map[string]any) []string {
	var paths []string
	if p, ok := args["path"].(string); ok {
		paths = append(paths, p)
	}
	if p, ok := args["file_path"].(string); ok {
		paths = append(paths, p)
	}
	return paths
}

func editedPathsFromFileList(args map[string]any) []string {
	var paths []string
	if files, ok := args["files"].([]any); ok {
		for _, f := range files {
			if fm, ok := f.(map[string]any); ok {
				if p, ok := fm["path"].(string); ok {
					paths = append(paths, p)
				}
			}
		}
	}
	return paths
}

func editedPathsFromBatchReplace(args map[string]any) []string {
	var paths []string
	if files, ok := args["files"].([]any); ok {
		for _, f := range files {
			if p, ok := f.(string); ok {
				paths = append(paths, p)
			}
		}
	}
	return paths
}

func editedPathsFromFileOps(args map[string]any) []string {
	var paths []string
	if ops, ok := args["operations"].([]any); ok {
		for _, o := range ops {
			om, ok := o.(map[string]any)
			if !ok {
				continue
			}
			if p, ok := om["source"].(string); ok && p != "" {
				paths = append(paths, p)
			}
			if p, ok := om["destination"].(string); ok && p != "" {
				paths = append(paths, p)
			}
		}
	}
	return paths
}

func editedPathsFromNotebook(args map[string]any) []string {
	if p, ok := args["notebook_path"].(string); ok {
		return []string{p}
	}
	return nil
}
