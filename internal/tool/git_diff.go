package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/vcs"
)

// GitDiff implements the git_diff tool.
type GitDiff struct{ WorkingDir string }

func (t GitDiff) Name() string { return "git_diff" }

func (t GitDiff) Description() string {
	return "Show git diff to inspect changes before staging or committing. Supports --cached and file-specific diffs."
}

func (t GitDiff) Parameters() json.RawMessage {
	return json.RawMessage(`{
	"type": "object",
	"properties": {
		"path": {
			"type": "string",
			"description": "Repository path (default: current directory)"
		},
		"cached": {
			"type": "boolean",
			"description": "Show staged changes only (default: false)"
		},
		"file": {
			"type": "string",
			"description": "Specific file to diff (optional)"
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

func (t GitDiff) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		Path   string `json:"path"`
		Cached bool   `json:"cached"`
		File   string `json:"file"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	dir := resolveDir(args.Path, t.WorkingDir)

	// Non-git VCS path.
	if v := vcs.Detect(dir); v != nil && v.Name() != "git" {
		out, err := v.Diff(ctx, dir, args.Cached, args.File)
		if err != nil {
			return Result{IsError: true, Content: fmt.Sprintf("%s diff failed: %v", v.Name(), err)}, nil
		}
		trimmed := strings.TrimSpace(out)
		if trimmed == "" {
			return Result{Content: "No differences found."}, nil
		}
		return Result{Content: trimmed}, nil
	}

	gitArgs := []string{"diff"}
	if args.Cached {
		gitArgs = append(gitArgs, "--cached")
	}
	if args.File != "" {
		gitArgs = append(gitArgs, "--", args.File)
	}

	cmd := gitCommand(ctx, gitArgs...)
	cmd.Dir = dir

	out, err := cmd.CombinedOutput()
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("git diff failed: %v\n%s", err, out)}, nil
	}

	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return Result{Content: "No differences found."}, nil
	}

	// Truncate if very large
	if len(trimmed) > maxOutputSize {
		trimmed = truncateUTF8Safe(trimmed, maxOutputSize) + "\n... [diff truncated]"
	}

	return Result{Content: trimmed}, nil
}

// Clone returns an independent copy of this tool for use by a different agent.
func (t GitDiff) Clone() Tool {
	return &GitDiff{WorkingDir: t.WorkingDir}
}
