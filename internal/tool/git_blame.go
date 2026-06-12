package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// GitBlame implements the git_blame tool.
type GitBlame struct{ WorkingDir string }

func (t GitBlame) Name() string { return "git_blame" }

func (t GitBlame) Description() string {
	return "Show what revision and author last modified each line of a file."
}

func (t GitBlame) Parameters() json.RawMessage {
	return json.RawMessage(`{
	"type": "object",
	"properties": {
		"path": {
			"type": "string",
			"description": "Repository path (default: current directory)"
		},
		"file": {
			"type": "string",
			"description": "File to blame"
		},
		"revision": {
			"type": "string",
			"description": "Revision to blame from (default: HEAD)"
		},
		"description": {
			"type": "string",
			"description": "REQUIRED. Brief activity label shown in the UI. Write in the user's language (e.g. 'Searching for TODO patterns', '检查构建配置'). You MUST always provide this field."
		}
	},
	"required": [
		"file",
		"description"
	]
}`)
}

func (t GitBlame) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		Path     string `json:"path"`
		File     string `json:"file"`
		Revision string `json:"revision"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	if args.File == "" {
		return Result{IsError: true, Content: "file is required"}, nil
	}

	revision := args.Revision
	if revision == "" {
		revision = "HEAD"
	}

	cmd := gitCommand(ctx, "blame", "--date=short", revision, "--", args.File)
	cmd.Dir = resolveDir(args.Path, t.WorkingDir)

	out, err := cmd.CombinedOutput()
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("git blame failed: %v\n%s", err, out)}, nil
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

// Clone returns an independent copy of this tool for use by a different agent.
func (t GitBlame) Clone() Tool {
	return &GitBlame{WorkingDir: t.WorkingDir}
}
