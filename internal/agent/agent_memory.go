package agent

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/memory"
	"github.com/topcheer/ggcode/internal/provider"
)

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
