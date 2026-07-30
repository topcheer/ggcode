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
	WorkingDir   string
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

	if msg := CheckRequired("path", args.Path); msg != "" {
		return Result{IsError: true, Content: "Error: " + msg}, nil
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

	// Check if file already exists (for overwrite awareness)
	var oldSize int64
	if info, err := os.Stat(args.Path); err == nil {
		oldSize = info.Size()
	}

	// Stale-read guard: if the file was modified externally since the agent's
	// last read/write, refuse the overwrite to prevent silent data loss.
	// This is critical in multi-agent scenarios where concurrent edits can
	// cause lost updates.
	if oldSize > 0 {
		if stale, since := defaultFileTracker.CheckStale(args.Path); stale {
			return Result{IsError: true, Content: fmt.Sprintf(
				"file was modified externally since last read (changed after %s). "+
					"Re-read the file with read_file before writing to avoid overwriting external changes.",
				since.Format("2006-01-02 15:04:05"),
			)}, nil
		}
	}

	// Capture old content before overwriting (for diff feedback).
	var oldContent string
	if oldSize > 0 {
		if data, err := os.ReadFile(args.Path); err == nil {
			oldContent = string(data)
		}
	}

	writeData := []byte(args.Content)
	writeData, fmtChanged := formatGoBytes(args.Path, writeData)

	// No-op guard: if the file already exists with identical content,
	// skip the write to avoid unnecessary mtime changes, checkpoint saves,
	// and cache invalidation (e.g. the LLM retried a write_file with the
	// same content it just wrote).
	if oldSize > 0 && string(writeData) == oldContent {
		return Result{Content: fmt.Sprintf(
			"No change: %s already contains the requested content (%d bytes). Skipping write.",
			args.Path, len(writeData),
		)}, nil
	}

	if err := atomicWriteFile(args.Path, writeData, 0644); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("error writing file: %v", err)}, nil
	}

	newSize := len(writeData)
	var msg string
	if oldSize > 0 {
		msg = fmt.Sprintf("Overwrote %s: %d bytes → %d bytes (was %d bytes)", args.Path, oldSize, newSize, oldSize)
	} else {
		msg = fmt.Sprintf("Created %s (%d bytes)", args.Path, newSize)
	}
	if fmtChanged {
		msg += " (auto-formatted)"
	}
	msg += scanAndWarn(args.Path, string(writeData))
	msg += compactDiff(oldContent, string(writeData))
	msg += postEditDiagnostics(t.WorkingDir, args.Path)
	return Result{Content: msg}, nil
}
