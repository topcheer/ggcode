package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// GitBranchList implements the git_branch_list tool.
type GitBranchList struct{}

func (t GitBranchList) Name() string { return "git_branch_list" }

func (t GitBranchList) Description() string {
	return "List branches in a GitHub repository"
}

func (t GitBranchList) Parameters() json.RawMessage {
	return json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": {
					"type": "string",
					"description": "Repository path (default: current directory)"
				},
				"remote": {
					"type": "boolean",
					"description": "Show remote-tracking branches (default: false, local only)"
				}
			}
		}`)
}

func (t GitBranchList) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		Path   string `json:"path"`
		Remote bool   `json:"remote"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	gitArgs := []string{"branch", "--list"}
	if args.Remote {
		gitArgs = []string{"branch", "--list", "--remotes"}
	}

	cmd := gitCommand(ctx, gitArgs...)
	cmd.Dir = args.Path

	out, err := cmd.CombinedOutput()
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("git branch failed: %v\n%s", err, out)}, nil
	}

	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return Result{Content: "No branches found."}, nil
	}

	return Result{Content: trimmed}, nil
}
