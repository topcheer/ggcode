package agent

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/memory"
	"github.com/topcheer/ggcode/internal/provider"
)

// maybeInjectProjectMemoryForTool inspects a tool call's arguments for file paths
// that might have associated project memory files (AGENTS.md, GGCODE.md, etc.)
// not yet loaded. If found, it injects the memory content into the conversation
// and returns true, signaling the caller to discard pending tool results and
// restart the iteration.
func (a *Agent) maybeInjectProjectMemoryForTool(tc provider.ToolCallDelta, pendingToolResults []provider.ContentBlock) bool {
	content, files, target := a.pendingProjectMemoryForTool(tc)
	if len(files) == 0 || strings.TrimSpace(content) == "" {
		return false
	}
	if len(pendingToolResults) > 0 {
		a.contextManager.Add(provider.Message{
			Role:    "user",
			Content: pendingToolResults,
		})
	}
	a.contextManager.Add(provider.Message{
		Role:    "system",
		Content: []provider.ContentBlock{{Type: "text", Text: "## Project Memory\n" + content}},
	})
	targetLabel := target
	if targetLabel == "" {
		targetLabel = "the pending path"
	}
	a.contextManager.Add(provider.Message{
		Role: "user",
		Content: []provider.ContentBlock{{
			Type: "text",
			Text: fmt.Sprintf("Additional project memory now applies to %s. Review that guidance first, then continue the task with the updated constraints.", targetLabel),
		}},
	})
	a.SetProjectMemoryFiles(files)
	debug.Log("agent", "injected path-scoped project memory for %s (%d files)", targetLabel, len(files))
	return true
}

// pendingProjectMemoryForTool scans a tool call's arguments for candidate file
// paths and returns the content of any unloaded project memory files found.
func (a *Agent) pendingProjectMemoryForTool(tc provider.ToolCallDelta) (content string, files []string, target string) {
	targets := projectMemoryTargetsForTool(tc.Name, tc.Arguments)
	if len(targets) == 0 {
		return "", nil, ""
	}

	a.mu.RLock()
	workingDir := a.workingDir
	loaded := make(map[string]struct{}, len(a.projectMemory))
	for file := range a.projectMemory {
		loaded[file] = struct{}{}
	}
	a.mu.RUnlock()

	for _, candidate := range targets {
		resolved := normalizeProjectMemoryPath(candidate, workingDir)
		if resolved == "" {
			continue
		}
		projectFiles, err := memory.ProjectMemoryFilesForPath(resolved)
		if err != nil || len(projectFiles) == 0 {
			continue
		}
		var unseen []string
		for _, file := range projectFiles {
			normalized := normalizeProjectMemoryPath(file, workingDir)
			if normalized == "" {
				continue
			}
			if _, ok := loaded[normalized]; ok {
				continue
			}
			unseen = append(unseen, normalized)
		}
		if len(unseen) == 0 {
			continue
		}
		content, files, err := memory.ReadProjectMemoryFiles(unseen)
		if err == nil && strings.TrimSpace(content) != "" {
			return content, files, resolved
		}
	}

	return "", nil, ""
}

// projectMemoryTargetsForTool extracts candidate file paths from a tool call's
// JSON arguments by recursively scanning for keys that represent file paths.
func projectMemoryTargetsForTool(toolName string, raw json.RawMessage) []string {
	if !toolCanTriggerProjectMemory(toolName) {
		return nil
	}
	var payload any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil
	}
	var targets []string
	seen := make(map[string]struct{})
	collectProjectMemoryTargets(payload, "", seen, &targets)
	return targets
}

// collectProjectMemoryTargets recursively walks a JSON structure and collects
// string values whose key name suggests a file path.
func collectProjectMemoryTargets(value any, key string, seen map[string]struct{}, out *[]string) {
	switch v := value.(type) {
	case map[string]any:
		for childKey, childValue := range v {
			collectProjectMemoryTargets(childValue, childKey, seen, out)
		}
	case []any:
		for _, item := range v {
			collectProjectMemoryTargets(item, key, seen, out)
		}
	case string:
		if !projectMemoryPathKey(key) {
			return
		}
		trimmed := strings.TrimSpace(v)
		if trimmed == "" || strings.Contains(trimmed, "://") {
			return
		}
		if _, ok := seen[trimmed]; ok {
			return
		}
		seen[trimmed] = struct{}{}
		*out = append(*out, trimmed)
	}
}

// toolCanTriggerProjectMemory returns true for tools whose arguments may
// contain file paths that could trigger project memory loading.
func toolCanTriggerProjectMemory(toolName string) bool {
	switch toolName {
	case "read_file", "write_file", "edit_file", "list_directory", "glob", "search_files":
		return true
	default:
		return strings.HasPrefix(toolName, "lsp_")
	}
}

// projectMemoryPathKey returns true for JSON key names that typically hold
// file or directory paths.
func projectMemoryPathKey(key string) bool {
	switch key {
	case "path", "file_path", "file", "filename", "directory":
		return true
	default:
		return false
	}
}

// normalizeProjectMemoryPath resolves a relative path against the working
// directory and returns a clean absolute path. Returns "" for invalid inputs.
func normalizeProjectMemoryPath(target, workingDir string) string {
	trimmed := strings.TrimSpace(target)
	if trimmed == "" || strings.Contains(trimmed, "://") {
		return ""
	}
	if !filepath.IsAbs(trimmed) {
		base := workingDir
		if strings.TrimSpace(base) == "" {
			base = "."
		}
		trimmed = filepath.Join(base, trimmed)
	}
	return filepath.Clean(trimmed)
}
