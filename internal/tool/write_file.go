package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// WriteFile implements the write_file tool.
type WriteFile struct {
	SandboxCheck AllowedPathChecker
}

func (t WriteFile) Name() string { return "write_file" }

func (t WriteFile) Description() string {
	return "Write content to a file, creating it if missing or fully OVERWRITING any existing file at that path. " +
		"Prefer edit_file or multi_edit_file when modifying an existing file — write_file destroys all current content. " +
		"Parent directories are created automatically if they do not exist."
}

func (t WriteFile) Parameters() json.RawMessage {
	return json.RawMessage(`{
	"type": "object",
	"properties": {
		"path": {
			"type": "string",
			"description": "Path to the file to write. Parent directories are created automatically. Prefer an absolute path when available."
		},
		"content": {
			"type": "string",
			"description": "Content to write. Existing file contents at this path will be fully replaced; use edit_file for targeted changes to existing files."
		},
		"description": {
			"type": "string",
			"description": "Optional. Brief activity label shown in the UI in the user's language."
		}
	},
	"required": [
		"path",
		"content"
	]
}`)
}

func (t WriteFile) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	if t.SandboxCheck != nil && !t.SandboxCheck(args.Path) {
		return Result{IsError: true, Content: "Error: path not allowed by sandbox policy"}, nil
	}

	// Create parent directories so weak LLMs don't have to issue an extra
	// run_command(mkdir) call for new files in fresh subdirectories.
	if dir := filepath.Dir(args.Path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return Result{IsError: true, Content: fmt.Sprintf("error creating parent directory: %v", err)}, nil
		}
	}

	if err := atomicWriteFile(args.Path, []byte(args.Content), 0644); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("error writing file: %v", err)}, nil
	}

	return Result{Content: fmt.Sprintf("Successfully wrote %d bytes to %s", len(args.Content), args.Path)}, nil
}
