package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/topcheer/ggcode/internal/safego"
)

const (
	maxFileOpsPaths = 50
)

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
				"description": "Optional. Brief activity label shown in the UI."
			}
		},
		"required": ["operations"]
	}`)
}

type fileOpsItemResult struct {
	Action      string `json:"action"`
	Source      string `json:"source"`
	Destination string `json:"destination,omitempty"`
	Status      string `json:"status"`
	Detail      string `json:"detail,omitempty"`
}

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
		default:

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

		info, err := os.Lstat(source)
		if err != nil {
			if os.IsNotExist(err) {
				return fileOpsItemResult{Action: action, Source: source, Status: "skipped", Detail: "file does not exist"}
			}
			return fileOpsItemResult{Action: action, Source: source, Status: "error", Detail: fmt.Sprintf("stat error: %v", err)}
		}

		srcKey := normalizePath(source)

		if info.IsDir() && !recursive {

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

		defaultFileTracker.RemoveTracking(source)
		if info.IsDir() {
			removeTrackingSubtree(srcKey)
		}
		return fileOpsItemResult{Action: action, Source: source, Status: "ok"}

	case "move":

		info, err := os.Lstat(source)
		if err != nil {
			if os.IsNotExist(err) {
				return fileOpsItemResult{Action: action, Source: source, Destination: destination, Status: "error", Detail: "source does not exist"}
			}
			return fileOpsItemResult{Action: action, Source: source, Destination: destination, Status: "error", Detail: fmt.Sprintf("stat error: %v", err)}
		}

		srcTreeKey := normalizePath(source)

		dstDir := filepath.Dir(destination)
		if err := os.MkdirAll(dstDir, 0755); err != nil {
			return fileOpsItemResult{Action: action, Source: source, Destination: destination, Status: "error", Detail: fmt.Sprintf("create destination dir error: %v", err)}
		}

		if _, err := os.Lstat(destination); err == nil {
			return fileOpsItemResult{Action: action, Source: source, Destination: destination, Status: "error", Detail: "destination already exists; move refuses to silently overwrite it (delete the destination first or choose a different path)"}
		} else if !os.IsNotExist(err) {
			return fileOpsItemResult{Action: action, Source: source, Destination: destination, Status: "error", Detail: fmt.Sprintf("stat destination error: %v", err)}
		}

		srcSeen := defaultFileTracker.HasBeenSeen(source)
		defaultFileTracker.RemoveTracking(source)

		if err := os.Rename(source, destination); err != nil {

			if srcSeen {
				defaultFileTracker.RecordRead(source)
			}

			if isCrossDeviceError(err) {
				if err := copyRecursive(source, destination); err != nil {

					_ = os.RemoveAll(destination)
					return fileOpsItemResult{Action: action, Source: source, Destination: destination, Status: "error", Detail: fmt.Sprintf("cross-device copy error (destination rolled back): %v", err)}
				}
				if err := os.RemoveAll(source); err != nil {

					return fileOpsItemResult{Action: action, Source: source, Destination: destination, Status: "error", Detail: fmt.Sprintf("moved but failed to remove source: %v", err)}
				}

				if srcSeen {
					defaultFileTracker.RemoveTracking(source)
				}
			} else {
				return fileOpsItemResult{Action: action, Source: source, Destination: destination, Status: "error", Detail: fmt.Sprintf("rename error: %v", err)}
			}
		}

		if srcSeen {
			defaultFileTracker.RecordRead(destination)
		}

		if info.IsDir() {
			migrateTrackingSubtree(srcTreeKey, destination)
		}
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

func isCrossDeviceError(err error) bool {
	if err == nil {
		return false
	}

	if errors.Is(err, syscall.EXDEV) {
		return true
	}
	msg := err.Error()

	return strings.Contains(msg, "cross-device") || strings.Contains(msg, "EXDEV") ||
		strings.Contains(msg, "different disk drive")
}

const maxCopyDepth = 64

func copyDirEntry(src, dst string, info os.FileInfo, depth int, visited map[string]bool) error {
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(src)
		if err != nil {
			return err
		}
		return os.Symlink(target, dst)
	}
	if info.IsDir() {
		return copyDirTree(src, dst, info, depth, visited)
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, info.Mode())
}

func copyDirTree(src, dst string, info os.FileInfo, depth int, visited map[string]bool) error {
	if depth > maxCopyDepth {
		return fmt.Errorf("copy: directory tree exceeds depth limit of %d at %s", maxCopyDepth, src)
	}
	real, err := filepath.EvalSymlinks(src)
	if err != nil {
		return err
	}
	if visited[real] {
		return fmt.Errorf("copy: symlink cycle detected at %s (already copied %s)", src, real)
	}
	visited[real] = true
	defer delete(visited, real)

	if err := os.MkdirAll(dst, info.Mode()); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		child := filepath.Join(src, entry.Name())
		childInfo, err := os.Lstat(child)
		if err != nil {
			return err
		}
		if err := copyDirEntry(child, filepath.Join(dst, entry.Name()), childInfo, depth+1, visited); err != nil {
			return err
		}
	}
	return nil
}

func copyRecursive(src, dst string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return copyDirTree(src, dst, info, 0, make(map[string]bool))
	}
	return copyDirEntry(src, dst, info, 0, nil)
}

func migrateTrackingSubtree(srcPrefix, dst string) {
	prefix := srcPrefix + "/"
	for path := range defaultFileTracker.SnapshotTracked() {
		if strings.HasPrefix(path, prefix) {
			defaultFileTracker.RemoveTracking(path)

			defaultFileTracker.RecordRead(filepath.Join(dst, path[len(prefix):]))
		}
	}
}

func removeTrackingSubtree(dirKey string) {
	prefix := dirKey + "/"
	for path := range defaultFileTracker.SnapshotTracked() {
		if strings.HasPrefix(path, prefix) {
			defaultFileTracker.RemoveTracking(path)
		}
	}
}
