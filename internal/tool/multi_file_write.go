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
	WorkingDir   string
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
	if len(args.Files) > maxMultiFileWriteFiles {
		return Result{IsError: true, Content: fmt.Sprintf("too many files: got %d, max %d. Split the write into smaller batches.", len(args.Files), maxMultiFileWriteFiles)}, nil
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

	// Enforce total payload size to prevent context window flooding.
	var totalBytes int
	for _, f := range args.Files {
		totalBytes += len(f.Content)
	}
	if totalBytes > maxMultiFileWritePayloadBytes {
		return Result{IsError: true, Content: fmt.Sprintf("total payload too large: %d bytes, max %d. Split the write into smaller batches.", totalBytes, maxMultiFileWritePayloadBytes)}, nil
	}

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
	skipped := 0

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

		// Stale-read guard: refuse to overwrite if the file was modified
		// externally since the agent's last read/write.
		if info, err := os.Stat(f.Path); err == nil && info.Size() > 0 {
			if stale, since := defaultFileTracker.CheckStale(f.Path); stale {
				failed++
				results = append(results, writeResult{
					Path:   f.Path,
					Status: "error",
					Error:  fmt.Sprintf("file modified externally since last read (changed after %s) — re-read before writing", since.Format("2006-01-02 15:04:05")),
				})
				continue
			}
		}

		// Write the file using atomic write (temp+rename) to prevent
		// corruption on crash/mid-write failure. Consistent with all
		// other file writing tools in the package.
		writeData, _ := formatGoBytes(f.Path, []byte(f.Content))

		// No-op guard: skip if existing content is identical.
		if oldData, rErr := os.ReadFile(f.Path); rErr == nil && string(writeData) == string(oldData) {
			skipped++
			results = append(results, writeResult{
				Path:   f.Path,
				Status: "skipped",
				Error:  "no change: content identical",
			})
			continue
		}

		if err := atomicWriteFile(f.Path, writeData, 0o644); err != nil {
			failed++
			results = append(results, writeResult{
				Path:   f.Path,
				Status: "error",
				Error:  fmt.Sprintf("failed to write file: %v", err),
			})
			continue
		}

		// Record the new mtime so subsequent writes don't see false staleness.
		defaultFileTracker.RecordWrite(f.Path)

		written++
		results = append(results, writeResult{
			Path:   f.Path,
			Status: "written",
			Bytes:  len(writeData),
		})
	}

	// Build summary.
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("[multi_file_write] requested=%d written=%d failed=%d skipped=%d\n", len(args.Files), written, failed, skipped))
	for _, r := range results {
		switch r.Status {
		case "written":
			sb.WriteString(fmt.Sprintf("  ✓ %s (%d bytes)\n", r.Path, r.Bytes))
		case "error":
			sb.WriteString(fmt.Sprintf("  ✗ %s: %s\n", r.Path, r.Error))
		case "skipped":
			sb.WriteString(fmt.Sprintf("  ○ %s: %s\n", r.Path, r.Error))
		}
	}

	isError := false
	if mode == "atomic" && failed > 0 {
		// This shouldn't happen in atomic mode (all-or-nothing), but guard anyway.
		isError = true
	}

	// Scan written files for potential secrets.
	for _, f := range args.Files {
		if warning := scanAndWarn(f.Path, f.Content); warning != "" {
			sb.WriteString(warning)
		}
	}

	// Syntax validation for written source files.
	for _, f := range args.Files {
		if syn := syntaxCheck(f.Path, []byte(f.Content)); syn != "" {
			sb.WriteString(syn)
		}
	}

	// Post-edit LSP diagnostics for written source files.
	for _, f := range args.Files {
		if diag := postEditDiagnostics(t.WorkingDir, f.Path); diag != "" {
			sb.WriteString(diag)
		}
	}

	return Result{Content: strings.TrimSuffix(sb.String(), "\n"), IsError: isError}, nil
}

func (t MultiFileWrite) Clone() MultiFileWrite {
	return MultiFileWrite{SandboxCheck: t.SandboxCheck, WorkingDir: t.WorkingDir}
}
