package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/safego"
)

const (
	maxFileOpsPaths = 50
)

// FileOps performs batch file-system operations (delete, move/rename, mkdir)
// in a single tool call with sandbox enforcement and file-tracker integration.
//
// Without this tool, agents resort to run_command("rm -rf ...") which bypasses
// the sandbox policy, file-integrity tracker, and protected-path guard. This
// tool brings those operations under the same safety envelope as edit_file and
// write_file.
type FileOps struct {
	SandboxCheck AllowedPathChecker
	WorkingDir   string
}

func (t FileOps) Name() string { return "file_ops" }

func (t FileOps) Description() string {
	return "Batch file-system operations: delete files/dirs, move/rename, or create directories. " +
		"All paths are validated against the sandbox policy before any mutation. " +
		"Use this instead of run_command('rm'/'mv'/'mkdir') to get safety checks and file tracking. " +
		"Supports multiple operations in one call. Returns a per-path result summary."
}

func (t FileOps) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"operations": {
				"type": "array",
				"description": "Operations to execute in order. Each has 'action', 'source', and optionally 'destination'.",
				"items": {
					"type": "object",
					"properties": {
						"action": {
							"type": "string",
							"enum": ["delete", "move", "mkdir"],
							"description": "delete: remove a file or directory (dirs must be empty unless recursive). move: rename or move a file/directory. mkdir: create a directory (including parents)."
						},
						"source": {
							"type": "string",
							"description": "Absolute path of the file/directory to operate on. For 'move', this is the path being moved."
						},
						"destination": {
							"type": "string",
							"description": "Required for 'move'. The new path for the file/directory."
						},
						"recursive": {
							"type": "boolean",
							"description": "For 'delete': if true, remove non-empty directories recursively. Default false.",
							"default": false
						}
					},
					"required": ["action", "source"]
				}
			},
			"description": {
				"type": "string",
				"description": "Optional. Brief activity label shown in the UI in the user's language."
			}
		},
		"required": ["operations"]
	}`)
}

// fileOpsItemResult is the per-operation outcome.
type fileOpsItemResult struct {
	Action      string `json:"action"`
	Source      string `json:"source"`
	Destination string `json:"destination,omitempty"`
	Status      string `json:"status"` // "ok", "skipped", "error"
	Detail      string `json:"detail,omitempty"`
}

// fileOpsContent is the full structured output.
type fileOpsContent struct {
	Summary string              `json:"summary"`
	Total   int                 `json:"total"`
	OK      int                 `json:"ok"`
	Skipped int                 `json:"skipped"`
	Errors  int                 `json:"errors"`
	Results []fileOpsItemResult `json:"results"`
}

func (t FileOps) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	_ = ctx
	var args struct {
		Operations []struct {
			Action      string `json:"action"`
			Source      string `json:"source"`
			Destination string `json:"destination"`
			Recursive   bool   `json:"recursive"`
		} `json:"operations"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	if len(args.Operations) == 0 {
		return Result{IsError: true, Content: "operations array must not be empty"}, nil
	}
	if len(args.Operations) > maxFileOpsPaths {
		return Result{IsError: true, Content: fmt.Sprintf("too many operations: got %d, max %d", len(args.Operations), maxFileOpsPaths)}, nil
	}

	// Validate all operations first (sandbox + argument checks).
	// This prevents partial execution when an early op is invalid.
	type validatedOp struct {
		action      string
		source      string
		destination string
		recursive   bool
	}
	ops := make([]validatedOp, 0, len(args.Operations))
	for i, a := range args.Operations {
		action := strings.ToLower(a.Action)
		if action != "delete" && action != "move" && action != "mkdir" {
			return Result{IsError: true, Content: fmt.Sprintf("operations[%d]: invalid action %q (must be delete, move, or mkdir)", i, a.Action)}, nil
		}
		src, err := cleanAbsolutePath(expandHomePath(a.Source))
		if err != nil {
			return Result{IsError: true, Content: fmt.Sprintf("operations[%d]: invalid source path %q: %v", i, a.Source, err)}, nil
		}
		var dst string
		if action == "move" {
			if a.Destination == "" {
				return Result{IsError: true, Content: fmt.Sprintf("operations[%d]: move requires a destination", i)}, nil
			}
			dst, err = cleanAbsolutePath(expandHomePath(a.Destination))
			if err != nil {
				return Result{IsError: true, Content: fmt.Sprintf("operations[%d]: invalid destination path %q: %v", i, a.Destination, err)}, nil
			}
		}

		// Sandbox check on source (and destination for move).
		if t.SandboxCheck != nil {
			if !t.SandboxCheck(src) {
				return Result{IsError: true, Content: fmt.Sprintf("operations[%d]: source path not allowed by sandbox policy: %s", i, src)}, nil
			}
			if dst != "" && !t.SandboxCheck(dst) {
				return Result{IsError: true, Content: fmt.Sprintf("operations[%d]: destination path not allowed by sandbox policy: %s", i, dst)}, nil
			}
		}

		ops = append(ops, validatedOp{action: action, source: src, destination: dst, recursive: a.Recursive})
	}

	// Execute operations sequentially (move/delete may have ordering dependencies).
	results := make([]fileOpsItemResult, 0, len(ops))

	for _, op := range ops {
		r := t.executeOne(op.action, op.source, op.destination, op.recursive)
		results = append(results, r)
	}

	out := fileOpsContent{
		Total:   len(results),
		Results: results,
	}
	for _, r := range results {
		switch r.Status {
		case "ok":
			out.OK++
		case "skipped":
			out.Skipped++
		case "error":
			out.Errors++
		}
	}

	if out.Errors > 0 {
		out.Summary = fmt.Sprintf("%d ops: %d ok, %d skipped, %d errors", out.Total, out.OK, out.Skipped, out.Errors)
	} else if out.Skipped > 0 {
		out.Summary = fmt.Sprintf("%d ops: %d ok, %d skipped", out.Total, out.OK, out.Skipped)
	} else {
		out.Summary = fmt.Sprintf("%d ops: all succeeded", out.Total)
	}

	content, err := json.Marshal(out)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("error marshaling result: %v", err)}, nil
	}
	return Result{Content: string(content), IsError: out.Errors > 0}, nil
}

func (t FileOps) executeOne(action, source, destination string, recursive bool) fileOpsItemResult {
	defer safego.Recover("tool.file_ops")

	switch action {
	case "delete":
		// Check existence first.
		info, err := os.Stat(source)
		if err != nil {
			if os.IsNotExist(err) {
				return fileOpsItemResult{Action: action, Source: source, Status: "skipped", Detail: "file does not exist"}
			}
			return fileOpsItemResult{Action: action, Source: source, Status: "error", Detail: fmt.Sprintf("stat error: %v", err)}
		}

		if info.IsDir() && !recursive {
			// Check if directory is empty.
			entries, err := os.ReadDir(source)
			if err != nil {
				return fileOpsItemResult{Action: action, Source: source, Status: "error", Detail: fmt.Sprintf("read dir error: %v", err)}
			}
			if len(entries) > 0 {
				return fileOpsItemResult{Action: action, Source: source, Status: "error", Detail: "directory not empty (use recursive=true to remove non-empty dirs)"}
			}
		}

		if recursive {
			err = os.RemoveAll(source)
		} else {
			err = os.Remove(source)
		}
		if err != nil {
			return fileOpsItemResult{Action: action, Source: source, Status: "error", Detail: fmt.Sprintf("delete error: %v", err)}
		}

		// Remove from the file tracker so it doesn't think the file is stale.
		defaultFileTracker.RemoveTracking(source)
		return fileOpsItemResult{Action: action, Source: source, Status: "ok"}

	case "move":
		// Check source existence.
		if _, err := os.Stat(source); err != nil {
			if os.IsNotExist(err) {
				return fileOpsItemResult{Action: action, Source: source, Destination: destination, Status: "error", Detail: "source does not exist"}
			}
			return fileOpsItemResult{Action: action, Source: source, Destination: destination, Status: "error", Detail: fmt.Sprintf("stat error: %v", err)}
		}

		// Ensure destination parent exists.
		dstDir := filepath.Dir(destination)
		if err := os.MkdirAll(dstDir, 0755); err != nil {
			return fileOpsItemResult{Action: action, Source: source, Destination: destination, Status: "error", Detail: fmt.Sprintf("create destination dir error: %v", err)}
		}

		// Use rename for atomic move when possible. Fall back to copy+remove
		// for cross-device moves.
		if err := os.Rename(source, destination); err != nil {
			// Cross-device link error: fall back to copy + delete.
			if isCrossDeviceError(err) {
				if err := copyRecursive(source, destination); err != nil {
					return fileOpsItemResult{Action: action, Source: source, Destination: destination, Status: "error", Detail: fmt.Sprintf("cross-device copy error: %v", err)}
				}
				if err := os.RemoveAll(source); err != nil {
					return fileOpsItemResult{Action: action, Source: source, Destination: destination, Status: "error", Detail: fmt.Sprintf("moved but failed to remove source: %v", err)}
				}
			} else {
				return fileOpsItemResult{Action: action, Source: source, Destination: destination, Status: "error", Detail: fmt.Sprintf("rename error: %v", err)}
			}
		}

		// Update tracker: remove old path, record new path.
		defaultFileTracker.RemoveTracking(source)
		defaultFileTracker.RecordRead(destination)
		return fileOpsItemResult{Action: action, Source: source, Destination: destination, Status: "ok"}

	case "mkdir":
		if err := os.MkdirAll(source, 0755); err != nil {
			return fileOpsItemResult{Action: action, Source: source, Status: "error", Detail: fmt.Sprintf("mkdir error: %v", err)}
		}
		return fileOpsItemResult{Action: action, Source: source, Status: "ok"}

	default:
		return fileOpsItemResult{Action: action, Source: source, Status: "error", Detail: fmt.Sprintf("unknown action: %s", action)}
	}
}

// isCrossDeviceError checks if an os.Rename error is due to cross-device linking.
func isCrossDeviceError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	// "invalid cross-device link" (Linux) or "cross-device renamed" or errno EXDEV.
	return strings.Contains(msg, "cross-device") || strings.Contains(msg, "EXDEV")
}

// copyRecursive copies a file or directory tree from src to dst.
// Used as a fallback when os.Rename fails due to cross-device constraints.
func copyRecursive(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if info.IsDir() {
		if err := os.MkdirAll(dst, info.Mode()); err != nil {
			return err
		}
		entries, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err := copyRecursive(filepath.Join(src, entry.Name()), filepath.Join(dst, entry.Name())); err != nil {
				return err
			}
		}
		return nil
	}
	// Regular file (or symlink: we copy the target content).
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, info.Mode())
}
