package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// GitCommit implements the git_commit tool.
type GitCommit struct{ WorkingDir string }

func (t GitCommit) Name() string { return "git_commit" }

func (t GitCommit) Description() string {
	return "Commit staged changes with a message. A Co-Authored-By trailer is appended automatically."
}

func (t GitCommit) Parameters() json.RawMessage {
	return json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": {
					"type": "string",
					"description": "Repository path (default: current directory)"
				},
				"message": {
					"type": "string",
					"description": "Commit message"
				},
				"all": {
					"type": "boolean",
					"description": "Automatically stage modified/deleted files before committing (git commit -a)"
				}
			},
			"required": ["message"]
		}`)
}

func (t GitCommit) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		Path    string `json:"path"`
		Message string `json:"message"`
		All     bool   `json:"all"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	if args.Message == "" {
		return Result{IsError: true, Content: "message is required"}, nil
	}

	// Append Co-Authored-By trailer
	fullMessage := args.Message + "\n\n" + coAuthorTrailer

	gitArgs := []string{"commit", "-m", fullMessage}
	if args.All {
		gitArgs = []string{"commit", "-a", "-m", fullMessage}
	}

	cmd := gitCommand(ctx, gitArgs...)
	cmd.Dir = resolveDir(args.Path, t.WorkingDir)

	out, err := cmd.CombinedOutput()
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("git commit failed: %v\n%s", err, out)}, nil
	}

	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return Result{Content: "Committed successfully."}, nil
	}

	return Result{Content: trimmed}, nil
}
