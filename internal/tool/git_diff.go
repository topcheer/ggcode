package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// GitDiff implements the git_diff tool.
type GitDiff struct { WorkingDir string }

func (t GitDiff) Name() string { return "git_diff" }

func (t GitDiff) Description() string {
	return "Show git diff. Supports staged (--cached) and specific file diffs."
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
			}
		}
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

	gitArgs := []string{"diff"}
	if args.Cached {
		gitArgs = append(gitArgs, "--cached")
	}
	if args.File != "" {
		gitArgs = append(gitArgs, "--", args.File)
	}

	cmd := gitCommand(ctx, gitArgs...)
	cmd.Dir = resolveDir(args.Path, t.WorkingDir)

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
		trimmed = trimmed[:maxOutputSize] + "\n... [diff truncated]"
	}

	return Result{Content: trimmed}, nil
}
