package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// GitShow implements the git_show tool.
type GitShow struct{}

func (t GitShow) Name() string { return "git_show" }

func (t GitShow) Description() string {
	return "Get details for a commit from a GitHub repository"
}

func (t GitShow) Parameters() json.RawMessage {
	return json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": {
					"type": "string",
					"description": "Repository path (default: current directory)"
				},
				"revision": {
					"type": "string",
					"description": "Commit SHA, branch name, or tag name"
				},
				"file": {
					"type": "string",
					"description": "Specific file path to show (optional)"
				},
				"stat": {
					"type": "boolean",
					"description": "Show diffstat instead of full diff (default: false)"
				}
			},
			"required": ["revision"]
		}`)
}

func (t GitShow) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		Path     string `json:"path"`
		Revision string `json:"revision"`
		File     string `json:"file"`
		Stat     bool   `json:"stat"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	if args.Revision == "" {
		return Result{IsError: true, Content: "revision is required"}, nil
	}

	gitArgs := []string{"show"}
	if args.Stat {
		gitArgs = append(gitArgs, "--stat")
	} else {
		gitArgs = append(gitArgs, "--format=fuller", "--patch")
	}
	gitArgs = append(gitArgs, args.Revision)
	if args.File != "" {
		gitArgs = append(gitArgs, "--", args.File)
	}

	cmd := gitCommand(ctx, gitArgs...)
	cmd.Dir = args.Path

	out, err := cmd.CombinedOutput()
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("git show failed: %v\n%s", err, out)}, nil
	}

	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return Result{Content: "No output."}, nil
	}

	if len(trimmed) > maxOutputSize {
		trimmed = trimmed[:maxOutputSize] + "\n... [output truncated]"
	}

	return Result{Content: trimmed}, nil
}
