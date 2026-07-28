package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/vcs"
)

// GitStatus implements the git_status tool.
type GitStatus struct{ WorkingDir string }

func (t GitStatus) Name() string { return "git_status" }

func (t GitStatus) Description() string {
	return "Show git working tree status. Returns porcelain output with file statuses."
}

func (t GitStatus) Parameters() json.RawMessage {
	return json.RawMessage(`{
	"type": "object",
	"properties": {
		"path": {
			"type": "string",
			"description": "Repository path (default: current directory)"
		},
		"description": {
			"type": "string",
			"description": "REQUIRED. Brief activity label shown in the UI. Write in the user's language (e.g. 'Searching for TODO patterns', '检查构建配置'). You MUST always provide this field."
		}
	},
	"required": [
		"description"
	]
}`)
}

func (t GitStatus) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	dir := resolveDir(args.Path, t.WorkingDir)

	// Fast path: if git repo, use the existing optimized command.
	if v := vcs.Detect(dir); v != nil && v.Name() != "git" {
		out, err := v.Status(ctx, dir)
		if err != nil {
			return Result{IsError: true, Content: fmt.Sprintf("%s status failed: %v", v.Name(), err)}, nil
		}
		trimmed := strings.TrimSpace(out)
		if trimmed == "" {
			return Result{Content: "Working tree clean. No changes."}, nil
		}
		return Result{Content: trimmed}, nil
	}

	cmd := gitCommand(ctx, "status", "--porcelain")
	cmd.Dir = dir

	out, err := cmd.CombinedOutput()
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("git status failed: %v\n%s", err, out)}, nil
	}

	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return Result{Content: "Working tree clean. No changes."}, nil
	}

	// Cap output — repos with thousands of untracked files (e.g., missing
	// .gitignore for node_modules) would flood the context.
	const maxGitStatusLines = 200
	lines := strings.Split(trimmed, "\n")
	if len(lines) > maxGitStatusLines {
		// Count by status type for the summary
		var modified, added, deleted, untracked int
		for _, l := range lines {
			if len(l) < 2 {
				continue
			}
			switch {
			case strings.HasPrefix(l, "?? "):
				untracked++
			case strings.Contains(l[:2], "M"):
				modified++
			case strings.Contains(l[:2], "A"):
				added++
			case strings.Contains(l[:2], "D"):
				deleted++
			}
		}
		shown := strings.Join(lines[:maxGitStatusLines], "\n")
		return Result{Content: shown +
			fmt.Sprintf("\n\n... [%d more files hidden. Summary: %d modified, %d added, %d deleted, %d untracked. Use 'git status --short | grep <pattern>' to filter.]",
				len(lines)-maxGitStatusLines, modified, added, deleted, untracked)}, nil
	}

	return Result{Content: trimmed}, nil
}

// Clone returns an independent copy of this tool for use by a different agent.
func (t GitStatus) Clone() Tool {
	return &GitStatus{WorkingDir: t.WorkingDir}
}
