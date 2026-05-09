package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// GitRemote implements the git_remote tool.
type GitRemote struct{ WorkingDir string }

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
	cmd.Dir = resolveDir(args.Path, t.WorkingDir)

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
