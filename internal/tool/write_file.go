package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func expandHomePath(path string) string {
	if len(path) == 0 || path[0] != '~' {
		return path
	}
	if len(path) == 1 || path[1] == '/' {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return path
		}
		if len(path) == 1 {
			return home
		}
		return filepath.Join(home, path[2:])
	}
	return path
}

func resolveToolPath(path, workingDir string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("path is required")
	}
	path = expandHomePath(path)
	if !filepath.IsAbs(path) {
		path = filepath.Join(workingDir, path)
	}
	return filepath.Clean(path), nil
}

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
			"description": "Optional. Brief activity label shown in the UI."
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

	resolvedPath, err := resolveToolPath(args.Path, t.WorkingDir)
	if err != nil {
		return Result{IsError: true, Content: "Error: " + err.Error()}, nil
	}
	args.Path = resolvedPath

	if t.SandboxCheck != nil && !t.SandboxCheck(args.Path) {
		return Result{IsError: true, Content: "Error: path not allowed by sandbox policy"}, nil
	}

	if dir := filepath.Dir(args.Path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return Result{IsError: true, Content: fmt.Sprintf("error creating parent directory: %v", err)}, nil
		}

		if t.SandboxCheck != nil && !t.SandboxCheck(args.Path) {
			return Result{IsError: true, Content: "Error: path not allowed by sandbox policy (parent directory changed)"}, nil
		}
	}

	fileExists := false
	var oldSize int64
	if info, err := os.Stat(args.Path); err == nil {
		fileExists = true
		oldSize = info.Size()
	}

	unlockWrite := LockWritePath(args.Path)
	defer unlockWrite()

	// Vacated-path gate (#5026, #5019 option 2): this tracked path was
	// vacated by an external mv/rm during a run_command in this session and
	// is still missing. Refuse so the agent does not silently recreate a
	// truncated twin at the old location; one read_file (the ack route,
	// which returns not-found + move hint and clears the marker) unblocks
	// an intentional recreate. Paths that exist again are not refused.
	if defaultFileTracker.VacancyCheck(args.Path) {
		defaultFileTracker.ClearVacated(args.Path)
		return Result{IsError: true, Content: fmt.Sprintf(
			"refusing to write %s: this path was vacated by an external move during this session "+
				"(a run_command mv/rm removed it; the original content now lives at the new location). "+
				"Re-read the current location or write to the new path. If you intentionally want to recreate "+
				"this file at the old path, the refusal itself acknowledges the move -- the next write proceeds "+
				"as fresh creation.",
			args.Path)}, nil
	}

	if fileExists {
		if stale, since := defaultFileTracker.CheckStale(args.Path); stale {
			return Result{IsError: true, Content: fmt.Sprintf(
				"file was modified externally since last read (changed after %s). "+
					"Re-read the file with read_file before writing to avoid overwriting external changes.",
				since.Format("2006-01-02 15:04:05"),
			)}, nil
		}
	}

	var oldContent string
	if oldSize > 0 {
		if data, err := os.ReadFile(args.Path); err == nil {
			oldContent = string(data)
		}
	}

	writeData := []byte(args.Content)
	writeData, fmtChanged := formatGoBytes(args.Path, writeData)

	if oldSize > 0 && string(writeData) == oldContent {
		return Result{Content: fmt.Sprintf(
			"No change: %s already contains the requested content (%d bytes). Skipping write.",
			args.Path, len(writeData),
		)}, nil
	}

	CaptureDiagnosticBaseline(t.WorkingDir, args.Path)

	blindWarn := blindWriteWarning(args.Path)

	if err := atomicWriteFile(args.Path, writeData, 0644); err != nil {
		ClearDiagnosticBaseline(args.Path)
		return Result{IsError: true, Content: fmt.Sprintf("error writing file: %v", err)}, nil
	}

	newSize := len(writeData)
	var msg string
	if oldSize > 0 {

		msg = fmt.Sprintf("Overwrote %s: %d bytes → %d bytes", args.Path, oldSize, newSize)
	} else {
		msg = fmt.Sprintf("Created %s (%d bytes)", args.Path, newSize)
	}
	if fmtChanged {
		msg += " (auto-formatted)"
	}
	msg += scanAndWarn(args.Path, string(writeData))

	msg += CheckContentForConflicts(string(writeData))
	msg += criticalFileWarning(args.Path)
	msg += blindWarn

	defaultFileTracker.RecordWrite(args.Path)
	msg += compactDiff(oldContent, string(writeData))
	msg += syntaxCheck(args.Path, writeData)
	msg += postEditDiagnostics(t.WorkingDir, args.Path)
	return Result{Content: msg}, nil
}
