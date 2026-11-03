package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

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
			"description": "Optional. Brief activity label shown in the UI."
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

	seen := make(map[string]int)
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

	var totalBytes int
	for _, f := range args.Files {
		totalBytes += len(f.Content)
	}
	if totalBytes > maxMultiFileWritePayloadBytes {
		return Result{IsError: true, Content: fmt.Sprintf("total payload too large: %d bytes, max %d. Split the write into smaller batches.", totalBytes, maxMultiFileWritePayloadBytes)}, nil
	}

	type writeResult struct {
		Path   string `json:"path"`
		Status string `json:"status"`
		Bytes  int    `json:"bytes,omitempty"`
		Error  string `json:"error,omitempty"`
	}

	results := make([]writeResult, 0, len(args.Files))
	hasError := false

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

	type originalState struct {
		existed bool
		content []byte
	}
	snapshots := make(map[string]originalState, len(args.Files))

	for _, f := range args.Files {
		CaptureDiagnosticBaseline(t.WorkingDir, f.Path)
		if mode == "atomic" {
			if oldData, rErr := os.ReadFile(f.Path); rErr == nil {
				snapshots[f.Path] = originalState{existed: true, content: oldData}
			} else {
				snapshots[f.Path] = originalState{existed: false}
			}
		}
	}

	blindWarnings := make(map[string]string, len(args.Files))
	for _, f := range args.Files {
		if w := blindWriteWarning(f.Path); w != "" {
			blindWarnings[f.Path] = w
		}
	}

	for _, f := range args.Files {

		if ctx.Err() != nil {
			failed++
			results = append(results, writeResult{
				Path:   f.Path,
				Status: "error",
				Error:  "cancelled",
			})
			continue
		}

		if t.SandboxCheck != nil && !t.SandboxCheck(f.Path) {
			failed++
			results = append(results, writeResult{
				Path:   f.Path,
				Status: "error",
				Error:  "path not allowed (sandbox)",
			})
			continue
		}

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

		unlockW := LockWritePath(f.Path)

		if info, err := os.Stat(f.Path); err == nil && info.Size() > 0 {
			if stale, since := defaultFileTracker.CheckStale(f.Path); stale {
				unlockW()
				failed++
				results = append(results, writeResult{
					Path:   f.Path,
					Status: "error",
					Error:  fmt.Sprintf("file modified externally since last read (changed after %s) — re-read before writing", since.Format("2006-01-02 15:04:05")),
				})
				continue
			}
		}

		writeData, _ := formatGoBytes(f.Path, []byte(f.Content))

		if oldData, rErr := os.ReadFile(f.Path); rErr == nil && string(writeData) == string(oldData) {
			unlockW()
			skipped++
			results = append(results, writeResult{
				Path:   f.Path,
				Status: "skipped",
				Error:  "no change: content identical",
			})
			continue
		}

		if err := atomicWriteFile(f.Path, writeData, 0o644); err != nil {
			unlockW()
			failed++
			results = append(results, writeResult{
				Path:   f.Path,
				Status: "error",
				Error:  fmt.Sprintf("failed to write file: %v", err),
			})
			continue
		}

		defaultFileTracker.RecordWrite(f.Path)
		unlockW()

		written++
		results = append(results, writeResult{
			Path:   f.Path,
			Status: "written",
			Bytes:  len(writeData),
		})
	}

	if mode == "atomic" && failed > 0 && written > 0 {
		restored := 0
		var rollbackErrs []string
		for i := range results {
			r := &results[i]
			if r.Status != "written" {
				continue
			}
			snap, ok := snapshots[r.Path]
			if !ok {
				continue
			}

			if snap.existed {

				unlockR := LockWritePath(r.Path)
				if err := atomicWriteFile(r.Path, snap.content, 0o644); err != nil {
					rollbackErrs = append(rollbackErrs, fmt.Sprintf("%s: restore failed: %v", r.Path, err))
				} else {
					defaultFileTracker.RecordWrite(r.Path)
				}
				unlockR()
				restored++
			} else {
				if err := os.Remove(r.Path); err != nil && !os.IsNotExist(err) {
					rollbackErrs = append(rollbackErrs, fmt.Sprintf("%s: remove failed: %v", r.Path, err))
				}
				restored++
			}
			defaultFileTracker.RecordWrite(r.Path)

			r.Status = "rolled_back"
		}
		written = 0

		failed = len(args.Files) - skipped
		if len(rollbackErrs) > 0 {
			sbWarning := fmt.Sprintf("atomic rollback INCOMPLETE - %d file(s) could not be restored; disk state is NOT all-or-nothing:\n%s",
				len(rollbackErrs), strings.Join(rollbackErrs, "\n"))
			return Result{IsError: true, Content: "[multi_file_write] " + sbWarning}, nil
		}
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("[multi_file_write] requested=%d written=%d failed=%d skipped=%d\n", len(args.Files), written, failed, skipped))
	for _, r := range results {
		switch r.Status {
		case "written":
			sb.WriteString(fmt.Sprintf("  ✓ %s (%d bytes)\n", r.Path, r.Bytes))
		case "rolled_back":
			sb.WriteString(fmt.Sprintf("  ↩ %s (rolled back)\n", r.Path))
		case "error":
			sb.WriteString(fmt.Sprintf("  ✗ %s: %s\n", r.Path, r.Error))
		case "skipped":
			sb.WriteString(fmt.Sprintf("  ○ %s: %s\n", r.Path, r.Error))
		}
	}

	isError := false
	if mode == "atomic" && failed > 0 {

		isError = true
	}

	for _, f := range args.Files {
		if warning := scanAndWarn(f.Path, f.Content); warning != "" {
			sb.WriteString(warning)
		}
	}

	for _, f := range args.Files {
		if warning := criticalFileWarning(f.Path); warning != "" {
			sb.WriteString(warning)
		}
	}

	for _, f := range args.Files {
		if w, ok := blindWarnings[f.Path]; ok {
			sb.WriteString(w)
		}
	}

	for _, f := range args.Files {
		if syn := syntaxCheck(f.Path, []byte(f.Content)); syn != "" {
			sb.WriteString(syn)
		}
	}

	for _, f := range args.Files {
		if diag := postEditDiagnostics(t.WorkingDir, f.Path); diag != "" {
			sb.WriteString(diag)
		}
	}

	out := MultiFileEditContent{
		Mode:         "multi_file_write(" + mode + ")",
		PlannedFiles: len(args.Files),
		Summary:      sb.String(),
	}
	for _, r := range results {
		switch r.Status {
		case "written":
			out.WrittenPaths = append(out.WrittenPaths, r.Path)
		case "error":
			out.FailedPaths = append(out.FailedPaths, r.Path)
		case "skipped", "rolled_back":
			out.SkippedPaths = append(out.SkippedPaths, r.Path)
		}
	}
	out.WrittenFiles = len(out.WrittenPaths)
	out.FailedFiles = len(out.FailedPaths)
	out.SkippedFiles = len(out.SkippedPaths)
	if content, err := json.Marshal(out); err == nil {
		return Result{Content: string(content), IsError: isError}, nil
	}
	return Result{Content: strings.TrimSuffix(sb.String(), "\n"), IsError: isError}, nil
}

func (t MultiFileWrite) Clone() MultiFileWrite {
	return MultiFileWrite{SandboxCheck: t.SandboxCheck, WorkingDir: t.WorkingDir}
}
