package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// GitRemote implements the git_remote tool.
type GitRemote struct{}

func (t GitRemote) Name() string { return "git_remote" }

func (t GitRemote) Description() string {
	return "List remote repositories and their URLs."
}

func (t GitRemote) Parameters() json.RawMessage {
	return json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": {
					"type": "string",
					"description": "Repository path (default: current directory)"
				},
				"verbose": {
					"type": "boolean",
					"description": "Show remote URLs (default: true)"
				}
			}
		}`)
}

func (t GitRemote) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		Path    string `json:"path"`
		Verbose *bool  `json:"verbose"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	verbose := true
	if args.Verbose != nil {
		verbose = *args.Verbose
	}

	gitArgs := []string{"remote"}
	if verbose {
		gitArgs = []string{"remote", "-v"}
	}

	cmd := gitCommand(ctx, gitArgs...)
	cmd.Dir = args.Path

	out, err := cmd.CombinedOutput()
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("git remote failed: %v\n%s", err, out)}, nil
	}

	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return Result{Content: "No remotes configured."}, nil
	}

	return Result{Content: trimmed}, nil
}
