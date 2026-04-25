package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// GitStash implements the git_stash tool for push/pop/apply/drop operations.
type GitStash struct { WorkingDir string }

func (t GitStash) Name() string { return "git_stash" }

func (t GitStash) Description() string {
	return "Manage git stash entries. Supports push, pop, apply, and drop actions."
}

func (t GitStash) Parameters() json.RawMessage {
	return json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": {
					"type": "string",
					"description": "Repository path (default: current directory)"
				},
				"action": {
					"type": "string",
					"description": "Stash action: push, pop, apply, drop (default: push)",
					"enum": ["push", "pop", "apply", "drop"]
				},
				"message": {
					"type": "string",
					"description": "Stash message (only used with push action)"
				},
				"index": {
					"type": "integer",
					"description": "Stash index for pop/apply/drop (default: 0, meaning latest)"
				}
			}
		}`)
}

func (t GitStash) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		Path    string `json:"path"`
		Action  string `json:"action"`
		Message string `json:"message"`
		Index   int    `json:"index"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	action := args.Action
	if action == "" {
		action = "push"
	}

	var gitArgs []string
	switch action {
	case "push":
		gitArgs = []string{"stash", "push"}
		if args.Message != "" {
			gitArgs = append(gitArgs, "-m", args.Message)
		}
	case "pop":
		gitArgs = []string{"stash", "pop"}
		if args.Index > 0 {
			gitArgs = append(gitArgs, fmt.Sprintf("stash@{%d}", args.Index))
		}
	case "apply":
		gitArgs = []string{"stash", "apply"}
		if args.Index > 0 {
			gitArgs = append(gitArgs, fmt.Sprintf("stash@{%d}", args.Index))
		}
	case "drop":
		gitArgs = []string{"stash", "drop"}
		if args.Index > 0 {
			gitArgs = append(gitArgs, fmt.Sprintf("stash@{%d}", args.Index))
		}
	default:
		return Result{IsError: true, Content: fmt.Sprintf("unsupported action %q: must be push, pop, apply, or drop", action)}, nil
	}

	cmd := gitCommand(ctx, gitArgs...)
	cmd.Dir = resolveDir(args.Path, t.WorkingDir)

	out, err := cmd.CombinedOutput()
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("git stash %s failed: %v\n%s", action, err, out)}, nil
	}

	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return Result{Content: fmt.Sprintf("git stash %s completed.", action)}, nil
	}

	return Result{Content: trimmed}, nil
}
