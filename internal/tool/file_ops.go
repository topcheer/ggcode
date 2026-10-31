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
				"description": "Optional. Brief activity label shown in the UI."
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
		default:
			// Unknown status (e.g. panic mid-op recovered with a zero-value
			// result whose Status is "") - count it as an error so the
			// summary never claims success for an unaccounted op.
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
		// Check existence first. #4551: os.Stat FOLLOWS symlinks, so a
		// dangling link (target deleted) reported IsNotExist and was skipped
		// forever - os.Remove would have removed the link itself. Lstat
		// inspects the link, matching copyDirTree's symlink handling.
		info, err := os.Lstat(source)
		if err != nil {
			if os.IsNotExist(err) {
				return fileOpsItemResult{Action: action, Source: source, Status: "skipped", Detail: "file does not exist"}
			}
			return fileOpsItemResult{Action: action, Source: source, Status: "error", Detail: fmt.Sprintf("stat error: %v", err)}
		}
		// #4551: resolve the tracker key while the path still exists -
		// normalizePath falls back to the lexical spelling once the tree is
		// gone, missing the resolved keys children were recorded under.
		srcKey := normalizePath(source)

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
		// #4551: directories are exact-path keyed; also drop every tracked
		// entry under the deleted tree so children don't linger as stale keys.
		defaultFileTracker.RemoveTracking(source)
		if info.IsDir() {
			removeTrackingSubtree(srcKey)
		}
		return fileOpsItemResult{Action: action, Source: source, Status: "ok"}

	case "move":
		// Check source existence. #4551: capture info - the tracking
		// migration below needs to know whether the source is a directory.
		// #4574: Lstat, not Stat - Stat FOLLOWS symlinks, so a dangling
		// link (its target deleted) reported "source does not exist" and
		// the link could never be moved, though os.Rename moves the link
		// entry itself. Lstat sees the link; info.IsDir() is false for a
		// link, so no subtree migration fires (correct - links have no
		// tracked children of their own).
		info, err := os.Lstat(source)
		if err != nil {
			if os.IsNotExist(err) {
				return fileOpsItemResult{Action: action, Source: source, Destination: destination, Status: "error", Detail: "source does not exist"}
			}
			return fileOpsItemResult{Action: action, Source: source, Destination: destination, Status: "error", Detail: fmt.Sprintf("stat error: %v", err)}
		}
		// #4551: resolve the subtree prefix while the source still exists;
		// after the rename normalizePath(source) degrades to the lexical
		// spelling and would miss resolved child keys.
		srcTreeKey := normalizePath(source)

		// Ensure destination parent exists.
		dstDir := filepath.Dir(destination)
		if err := os.MkdirAll(dstDir, 0755); err != nil {
			return fileOpsItemResult{Action: action, Source: source, Destination: destination, Status: "error", Detail: fmt.Sprintf("create destination dir error: %v", err)}
		}

		// #4445: refuse to silently overwrite an existing destination.
		// os.Rename atomically replaces an existing regular file on Unix and
		// the cross-device fallback clobbers it via os.WriteFile, so both
		// paths destroy data with status:"ok" and no warning - unlike
		// write_file, which guards this same case three ways. Fail closed:
		// the caller must remove the destination first or pick another path.
		if _, err := os.Lstat(destination); err == nil {
			return fileOpsItemResult{Action: action, Source: source, Destination: destination, Status: "error", Detail: "destination already exists; move refuses to silently overwrite it (delete the destination first or choose a different path)"}
		} else if !os.IsNotExist(err) {
			return fileOpsItemResult{Action: action, Source: source, Destination: destination, Status: "error", Detail: fmt.Sprintf("stat destination error: %v", err)}
		}

		// Capture the source's tracked-read state BEFORE the rename: once
		// os.Rename succeeds the source no longer exists, normalizePath falls
		// back to the lexical key (macOS /var vs /private/var), and the
		// resolved key recorded at read time would never match (#4445).
		// Removing the old entry here also deletes the resolved key that a
		// post-rename RemoveTracking would miss.
		srcSeen := defaultFileTracker.HasBeenSeen(source)
		defaultFileTracker.RemoveTracking(source)

		// Use rename for atomic move when possible. Fall back to copy+remove
		// for cross-device moves.
		if err := os.Rename(source, destination); err != nil {
			// #4448: every failure return below leaves the file physically at
			// SOURCE, but the pre-rename RemoveTracking already dropped its
			// read-state - a later write_file to the source then false-positived
			// the blind-write warning ("overwriting without having read it")
			// for a file the agent HAD read. Restore the tracked state on
			// failure; on success the conditional RecordRead(destination)
			// below carries it over.
			if srcSeen {
				defaultFileTracker.RecordRead(source)
			}
			// Cross-device link error: fall back to copy + delete.
			if isCrossDeviceError(err) {
				if err := copyRecursive(source, destination); err != nil {
					// #4551: roll back the partially-copied destination. #4445
					// guarantees destination did not exist on entry, so the whole
					// tree is ours; leaving it behind parks a half-written tree
					// that fail-closed move then refuses to retry over
					// ("destination already exists") and that later reads may
					// mistake for complete data.
					_ = os.RemoveAll(destination)
					return fileOpsItemResult{Action: action, Source: source, Destination: destination, Status: "error", Detail: fmt.Sprintf("cross-device copy error (destination rolled back): %v", err)}
				}
				if err := os.RemoveAll(source); err != nil {
					// Source still exists (remove failed): its read-state restore
					// above matches the physical state. The destination copy ALSO
					// exists - leave it untracked (it was never read).
					return fileOpsItemResult{Action: action, Source: source, Destination: destination, Status: "error", Detail: fmt.Sprintf("moved but failed to remove source: %v", err)}
				}
				// #4602: EXDEV success physically removed the source, but the
				// failure-entry restore above re-created its tracked key. Drop it
				// or the tracker keeps a "read" record for a path that no longer
				// exists - recreating a file at the source path would then skip
				// the blind-write warning (#4448's detection surface).
				if srcSeen {
					defaultFileTracker.RemoveTracking(source)
				}
			} else {
				return fileOpsItemResult{Action: action, Source: source, Destination: destination, Status: "error", Detail: fmt.Sprintf("rename error: %v", err)}
			}
		}

		// Carry the read-state over to the new path ONLY if the source was
		// actually tracked. Unconditionally RecordRead(destination) fabricated
		// a read for a file this run never read, masking overwrite footprints
		// (#4445).
		if srcSeen {
			defaultFileTracker.RecordRead(destination)
		}
		// #4551: the tracker is exact-path keyed with no subtree semantics;
		// a directory move must carry every tracked child's read-state from
		// olddir/... to newdir/... or a later write_file to newdir/a.go
		// false-positives the blind-write warning for a file the agent HAD
		// read (directory-shaped repeat of #4448).
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

// isCrossDeviceError checks if an os.Rename error is due to cross-device linking.
func isCrossDeviceError(err error) bool {
	if err == nil {
		return false
	}
	// #1684 case 2: errno check first (Windows returns
	// ERROR_NOT_SAME_DEVICE(17), whose text mentions neither keyword),
	// text fallback second.
	if errors.Is(err, syscall.EXDEV) {
		return true
	}
	msg := err.Error()
	// "invalid cross-device link" (Linux) or "cross-device renamed" or errno EXDEV.
	return strings.Contains(msg, "cross-device") || strings.Contains(msg, "EXDEV") ||
		strings.Contains(msg, "different disk drive")
}

// copyDirEntry copies one child of a directory tree during copyDirTree.
// Symlinks are recreated as symlinks (os.Symlink) instead of followed
// (#1323: Stat followed ancestor-pointing links into infinite recursion
// - unbounded disk writes or an unrecoverable stack-overflow fatal).
// A dangling link yields a plain skip note rather than a ReadFile error.
// maxCopyDepth bounds pathological directory nesting during recursive
// copy (#1323 defense-in-depth alongside cycle detection).
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

// copyDirTree copies one directory level. Realpath-based cycle detection
// (#1323) guards against directory symlink loops that survive even Lstat
// handling at the entry level (e.g. bind-mount-like constructs); a depth
// cap bounds pathological trees.
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
		childInfo, err := os.Lstat(child) // Lstat: do not follow symlinks (#1323)
		if err != nil {
			return err
		}
		if err := copyDirEntry(child, filepath.Join(dst, entry.Name()), childInfo, depth+1, visited); err != nil {
			return err
		}
	}
	return nil
}

// copyRecursive copies a file or directory tree from src to dst.
// Used as a fallback when os.Rename fails due to cross-device constraints.
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

// migrateTrackingSubtree carries every tracked read-state entry under the
// (pre-resolved) subtree prefix srcPrefix over to the moved location dst
// (#4551). Without this, read_file olddir/a.go -> move olddir newdir ->
// write_file newdir/a.go false-positives the blind-write warning.
func migrateTrackingSubtree(srcPrefix, dst string) {
	prefix := srcPrefix + "/"
	for path := range defaultFileTracker.SnapshotTracked() {
		if strings.HasPrefix(path, prefix) {
			defaultFileTracker.RemoveTracking(path)
			// RecordRead re-stats the moved file (present after the move);
			// its normalizePath resolves the new location cleanly.
			defaultFileTracker.RecordRead(filepath.Join(dst, path[len(prefix):]))
		}
	}
}

// removeTrackingSubtree drops every tracked entry under the (pre-resolved)
// subtree prefix dirKey so deleted children do not linger as stale keys (#4551).
func removeTrackingSubtree(dirKey string) {
	prefix := dirKey + "/"
	for path := range defaultFileTracker.SnapshotTracked() {
		if strings.HasPrefix(path, prefix) {
			defaultFileTracker.RemoveTracking(path)
		}
	}
}
