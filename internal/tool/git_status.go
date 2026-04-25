package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
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
			}
		}
	}`)
}

func (t GitStatus) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	cmd := gitCommand(ctx, "status", "--porcelain")
	cmd.Dir = resolveDir(args.Path, t.WorkingDir)

	out, err := cmd.CombinedOutput()
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("git status failed: %v\n%s", err, out)}, nil
	}

	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return Result{Content: "Working tree clean. No changes."}, nil
	}

	return Result{Content: trimmed}, nil
}
