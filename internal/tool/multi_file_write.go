package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// MultiFileWrite creates or overwrites multiple files in a single call.
// Parent directories are created automatically if they do not exist.
type MultiFileWrite struct {
	SandboxCheck AllowedPathChecker
}

func (t MultiFileWrite) Name() string { return "multi_file_write" }

func (t MultiFileWrite) Description() string {
	return "Write content to multiple files in one call, creating or overwriting each file. " +
		"Prefer edit_file when modifying existing files. Default mode is atomic; use mode=partial_success for mixed outcomes."
}

func (t MultiFileWrite) Parameters() json.RawMessage {
	return json.RawMessage(`{
	"type": "object",
	"properties": {
		"mode": {
			"type": "string",
			"enum": ["atomic", "partial_success"],
			"description": "Optional. Defaults to atomic. atomic writes no files if any file fails (e.g. sandbox violation). partial_success writes successful files and reports failures separately."
		},
		"files": {
			"type": "array",
			"description": "Files to write. Prefer unique paths; if a path appears multiple times, the last write wins.",
			"items": {
				"type": "object",
				"properties": {
					"path": {
						"type": "string",
						"description": "Absolute path to the file to create or overwrite."
					},
					"content": {
						"type": "string",
						"description": "Full content to write to the file. Existing contents at this path will be fully replaced."
					}
				},
				"required": ["path", "content"]
			}
		},
		"description": {
			"type": "string",
			"description": "Optional. Brief activity label shown in the UI in the user's language."
		}
	},
	"required": ["files"]
}`)
}

type multiFileWriteArgs struct {
	Files []struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	} `json:"files"`
	Mode        string `json:"mode"`
	Description string `json:"description"`
}

func (t MultiFileWrite) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args multiFileWriteArgs
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	if len(args.Files) == 0 {
		return Result{IsError: true, Content: "no files provided"}, nil
	}

	mode := args.Mode
	if mode == "" {
		mode = "atomic"
	}
	if mode != "atomic" && mode != "partial_success" {
		return Result{IsError: true, Content: fmt.Sprintf("invalid mode %q: must be atomic or partial_success", mode)}, nil
	}

	// Deduplicate paths: last write wins (same semantics as calling write_file twice).
	// This is more forgiving than rejecting duplicates — the LLM may logically
	// group writes but accidentally repeat a path.
	// Build a new slice to avoid mutating the slice we are iterating over.
	seen := make(map[string]int) // cleaned path → index in deduped slice
	dedupedFiles := make([]struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}, 0, len(args.Files))
	for _, f := range args.Files {
		path, err := cleanAbsolutePath(f.Path)
		if err != nil {
			return Result{IsError: true, Content: fmt.Sprintf("invalid path %q: %v", f.Path, err)}, nil
		}
		if idx, ok := seen[path]; ok {
			// Overwrite existing entry (last wins).
			dedupedFiles[idx].Content = f.Content
		} else {
			seen[path] = len(dedupedFiles)
			dedupedFiles = append(dedupedFiles, struct {
				Path    string `json:"path"`
				Content string `json:"content"`
			}{Path: path, Content: f.Content})
		}
	}
	args.Files = dedupedFiles

	// Sandbox validation — check all paths first.
	for _, f := range args.Files {
		if t.SandboxCheck != nil && !t.SandboxCheck(f.Path) {
			if mode == "atomic" {
				return Result{IsError: true, Content: fmt.Sprintf("path not allowed: %s", f.Path)}, nil
			}
		}
	}

	type writeResult struct {
		Path   string `json:"path"`
		Status string `json:"status"` // "written" or "error"
		Bytes  int    `json:"bytes,omitempty"`
		Error  string `json:"error,omitempty"`
	}

	results := make([]writeResult, 0, len(args.Files))
	hasError := false

	// In atomic mode, do all sandbox checks before any writes.
	if mode == "atomic" {
		for _, f := range args.Files {
			if t.SandboxCheck != nil && !t.SandboxCheck(f.Path) {
				hasError = true
				results = append(results, writeResult{
					Path:   f.Path,
					Status: "error",
					Error:  "path not allowed (sandbox)",
				})
			}
		}
		if hasError {
			b, _ := json.MarshalIndent(results, "", "  ")
			return Result{IsError: true, Content: fmt.Sprintf("atomic mode: no files written due to errors\n\n%s", string(b))}, nil
		}
	}

	written := 0
	failed := 0

	for _, f := range args.Files {
		// Check for cancellation before each file write.
		if ctx.Err() != nil {
			failed++
			results = append(results, writeResult{
				Path:   f.Path,
				Status: "error",
				Error:  "cancelled",
			})
			continue
		}

		// Sandbox check for partial_success mode (per-file).
		if t.SandboxCheck != nil && !t.SandboxCheck(f.Path) {
			failed++
			results = append(results, writeResult{
				Path:   f.Path,
				Status: "error",
				Error:  "path not allowed (sandbox)",
			})
			continue
		}

		// Create parent directories.
		dir := filepath.Dir(f.Path)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			failed++
			results = append(results, writeResult{
				Path:   f.Path,
				Status: "error",
				Error:  fmt.Sprintf("failed to create parent directories: %v", err),
			})
			continue
		}

		// Write the file using atomic write (temp+rename) to prevent
		// corruption on crash/mid-write failure. Consistent with all
		// other file writing tools in the package.
		if err := atomicWriteFile(f.Path, []byte(f.Content), 0o644); err != nil {
			failed++
			results = append(results, writeResult{
				Path:   f.Path,
				Status: "error",
				Error:  fmt.Sprintf("failed to write file: %v", err),
			})
			continue
		}

		written++
		results = append(results, writeResult{
			Path:   f.Path,
			Status: "written",
			Bytes:  len(f.Content),
		})
	}

	// Build summary.
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("[multi_file_write] requested=%d written=%d failed=%d\n", len(args.Files), written, failed))
	for _, r := range results {
		switch r.Status {
		case "written":
			sb.WriteString(fmt.Sprintf("  ✓ %s (%d bytes)\n", r.Path, r.Bytes))
		case "error":
			sb.WriteString(fmt.Sprintf("  ✗ %s: %s\n", r.Path, r.Error))
		}
	}

	isError := false
	if mode == "atomic" && failed > 0 {
		// This shouldn't happen in atomic mode (all-or-nothing), but guard anyway.
		isError = true
	}

	return Result{Content: strings.TrimSuffix(sb.String(), "\n"), IsError: isError}, nil
}

func (t MultiFileWrite) Clone() MultiFileWrite {
	return MultiFileWrite{SandboxCheck: t.SandboxCheck}
}
