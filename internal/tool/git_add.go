package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// GitAdd implements the git_add tool.
type GitAdd struct {
	WorkingDir string
}

func (t GitAdd) Name() string { return "git_add" }

func (t GitAdd) Description() string {
	return "Add file contents to the index (staging area)."
}

func (t GitAdd) Parameters() json.RawMessage {
	return json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": {
					"type": "string",
					"description": "Repository path (default: current directory)"
				},
				"files": {
					"type": "array",
					"items": {"type": "string"},
					"description": "File paths to stage. Use [\".\"] to stage all changes."
				}
			},
			"required": ["files"]
		}`)
}

func (t GitAdd) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		Path  string   `json:"path"`
		Files []string `json:"files"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	if len(args.Files) == 0 {
		return Result{IsError: true, Content: "files is required"}, nil
	}

	gitArgs := []string{"add", "--"}
	gitArgs = append(gitArgs, args.Files...)

	cmd := gitCommand(ctx, gitArgs...)
	cmd.Dir = resolveDir(args.Path, t.WorkingDir)

	out, err := cmd.CombinedOutput()
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("git add failed: %v\n%s", err, out)}, nil
	}

	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return Result{Content: fmt.Sprintf("Staged %d file(s).", len(args.Files))}, nil
	}

	return Result{Content: trimmed}, nil
}
