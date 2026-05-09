package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// GitStashList implements the git_stash_list tool.
type GitStashList struct{ WorkingDir string }

func (t GitStashList) Name() string { return "git_stash_list" }

func (t GitStashList) Description() string {
	return "List stash entries."
}

func (t GitStashList) Parameters() json.RawMessage {
	return json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": {
					"type": "string",
					"description": "Repository path (default: current directory)"
				}
			,
			"description": {
				"type": "string",
				"description": "Brief activity label shown in the UI. Write in the user's language (e.g. 'Checking git status', '查看仓库状态')."
			}
		}`)
}

func (t GitStashList) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	cmd := gitCommand(ctx, "stash", "list")
	cmd.Dir = resolveDir(args.Path, t.WorkingDir)

	out, err := cmd.CombinedOutput()
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("git stash list failed: %v\n%s", err, out)}, nil
	}

	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return Result{Content: "No stash entries."}, nil
	}

	return Result{Content: trimmed}, nil
}
