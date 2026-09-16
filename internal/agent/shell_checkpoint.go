package agent

// Shell-Mutation Checkpoints -- checkpoint coverage for run_command.
//
// Research basis: the 2026 checkpoint/rollback engineering literature
// (Anthropic engineering, "git worktrees and checkpoints: managing code
// changes with Claude Code") identifies "checkpoint exclusions" as the
// canonical failure mode of checkpoint systems: state changes that are
// invisible to the snapshot layer. ggcode's checkpoint Manager snapshots
// only editor-tool writes (edit_file / write_file / multi_file_*), so
// mutations performed THROUGH THE SHELL -- `sed -i`, `go fmt -w`, codegen,
// `patch`, `mv` -- were invisible: undo_edit and checkpoint revert left
// those changes on disk while reporting success. That is a misleading
// restore, the exact exclusion bug the literature warns about.
//
// Fix: before a run_command executes, capture a lightweight pre-state of
// the workspace via git (`git status --porcelain -z`, contents of dirty +
// untracked files, capped). After a successful command, re-capture and
// diff; every file created, modified, or deleted by the command gets a
// NORMAL checkpoint through the same checkpoint.Manager used by editor
// tools, so undo_edit, revert-by-ID, and UndoRun batch-revert all work
// uniformly.
//
// Non-git workspaces: snapshots are skipped (logged once) -- without a
// baseline we cannot restore pre-state, and a content-less undo would
// DESTROY data. Silent-skips keep the feature safe-by-default.

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/checkpoint"
	"github.com/topcheer/ggcode/internal/debug"
)

const (
	// shellCkptMaxFiles caps how many per-file checkpoints a single shell
	// command may produce. A codegen command touching thousands of files
	// must not flood the undo stack or memory.
	shellCkptMaxFiles = 40
	// shellCkptMaxFileBytes caps per-file content capture; larger files
	// (build artifacts, vendored blobs) are skipped.
	shellCkptMaxFileBytes = 1 << 20 // 1 MiB
	// shellCkptGlobalBytes caps the TOTAL pre-state memory held per command.
	shellCkptGlobalBytes = 20 << 20 // 20 MiB
	// shellCkptTimeout bounds each git invocation; a hung git must not stall
	// the agent loop.
	shellCkptTimeout = 10 * time.Second
)

// shellCkptSkipDirs: UNTRACKED entries under these directories are skipped
// (dependency caches, build output -- rarely meaningful to undo, often huge).
// TRACKED modifications are always captured regardless of directory: they
// are real working-tree state, e.g. vendored code the command edited.
var shellCkptSkipDirs = map[string]bool{
	"node_modules": true, "vendor": true, "__pycache__": true,
	".venv": true, "venv": true, "dist": true, "target": true,
	".ggcode": true, ".git": true,
}

// shellFileState is the captured pre/post state of one workspace file.
// captured=false means the file EXISTED but its content could not be
// captured (oversized, budget exhausted, or read failure). Such files are
// NOT restorable and MUST be skipped by checkpointMutations: a checkpoint
// with empty oldContent and existed=true would make undo_edit ZERO the
// file on disk (real data loss, the #2441 review blocker).
type shellFileState struct {
	content  string
	existed  bool
	captured bool
}

// shellSnapshot is a point-in-time map of workspace-relative path -> state,
// restricted to files that are dirty or untracked in git (i.e. the only
// files a shell command can mutate without a commit changing under us).
type shellSnapshot struct {
	root  string
	head  string // commit SHA at snapshot time; pre-state of clean tracked files
	files map[string]shellFileState
}

// takeShellSnapshot captures the pre-state. Returns nil when the workspace
// is not a git repository or git fails -- callers must treat nil as no-op.
func (a *Agent) takeShellSnapshot() *shellSnapshot {
	workDir := a.WorkingDir()
	if workDir == "" {
		return nil
	}
	return takeShellSnapshotIn(workDir)
}

func takeShellSnapshotIn(workDir string) *shellSnapshot {
	if !isGitWorktree(workDir) {
		debug.Log("shell-checkpoint", "workspace is not a git repo; shell mutation checkpointing disabled")
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), shellCkptTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "-C", workDir, "status", "--porcelain", "-z").Output()
	if err != nil {
		debug.Log("shell-checkpoint", "git status failed: %v", err)
		return nil
	}
	snap := &shellSnapshot{root: workDir, files: make(map[string]shellFileState)}
	if headOut, herr := exec.CommandContext(ctx, "git", "-C", workDir, "rev-parse", "HEAD").Output(); herr == nil {
		snap.head = strings.TrimSpace(string(headOut))
	}
	budget := shellCkptGlobalBytes
	for _, entry := range splitPorcelainZ(out) {
		if budget <= 0 || len(snap.files) >= shellCkptMaxFiles*4 {
			break // broad cap on enumeration; diff pass applies the tight cap
		}
		status, path := entry.status, entry.path
		if path == "" || strings.HasSuffix(path, "/") {
			continue // directory entry (untracked dir) -- cannot snapshot
		}
		if status == "??" && skipUntrackedPath(path) {
			continue
		}
		content, existed, captured := readSnapshotFile(filepath.Join(workDir, path), &budget)
		snap.files[path] = shellFileState{content: content, existed: existed, captured: captured}
	}
	return snap
}

// porcelainEntry is one parsed `git status --porcelain -z` record.
type porcelainEntry struct {
	status string
	path   string
}

// splitPorcelainZ parses the NUL-separated porcelain format. Rename/copy
// records carry TWO NUL-separated fields (`R  new\0old\0`); the NEW path is
// the worktree state that matters for snapshotting, so the second field is
// dropped (it has no separate worktree content).
func splitPorcelainZ(out []byte) []porcelainEntry {
	var entries []porcelainEntry
	parts := bytes.Split(out, []byte{0})
	for i := 0; i < len(parts); i++ {
		p := parts[i]
		if len(p) < 4 { // "XY " + at least 1 char of path
			continue
		}
		status := string(p[:2])
		path := string(p[3:])
		if status[0] == 'R' || status[0] == 'C' {
			i++ // consume the second field (old path)
		}
		entries = append(entries, porcelainEntry{status: status, path: path})
	}
	return entries
}

func skipUntrackedPath(path string) bool {
	for _, seg := range strings.Split(path, "/") {
		if shellCkptSkipDirs[seg] {
			return true
		}
	}
	return false
}

// readSnapshotFile reads one file into the snapshot budget. Returns
// (content, existed, captured): existed=false only for missing/non-regular
// files -- absence is itself fully captured, so captured=TRUE there (a
// false would make Pass 1 skip deletion checkpoints for clean tracked
// files, regressing delete-undo). captured=false ONLY when the file
// existed but its content could not be captured (oversized, budget
// exhausted, read failure) -- callers must treat uncaptured files as NOT
// restorable and never checkpoint them.
func readSnapshotFile(path string, budget *int) (string, bool, bool) {
	fi, err := os.Stat(path)
	if err != nil {
		return "", false, true // absent: state fully represented
	}
	if !fi.Mode().IsRegular() {
		return "", false, true // non-regular: treated as absent
	}
	if fi.Size() > shellCkptMaxFileBytes {
		return "", true, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", true, false
	}
	if len(data) > *budget {
		return "", true, false
	}
	*budget -= len(data)
	return string(data), true, true
}

// checkpointMutations diffs the post-command snapshot against the captured
// pre-state and records one checkpoint per mutated file through the SAME
// manager editor tools use. Returns the number of checkpoints created.
// nil snapshots (non-git workspace, git failure) are a safe no-op.
func (s *shellSnapshot) checkpointMutations(after *shellSnapshot, toolCall string, cpMgr *checkpoint.Manager) int {
	if s == nil || after == nil || cpMgr == nil {
		return 0
	}
	created := 0
	// Pass 1: paths present in the post-state -- created or modified.
	for path, post := range after.files {
		if created >= shellCkptMaxFiles {
			break
		}
		pre, hadPre := s.files[path]
		if hadPre && !pre.captured {
			// Pre-state uncaptured (oversize/budget/read-failure): a checkpoint
			// here would carry empty oldContent with existed=true and undo_edit
			// would ZERO the file. Not restorable => skip (#2441 review fix).
			debug.Log("shell-checkpoint", "pre-state of %s not captured (oversize/budget); not checkpointed", path)
			continue
		}
		if !post.captured {
			// Post-state uncaptured (file grew past the cap): recording it would
			// store empty newContent and a redo would zero the file. Skip.
			debug.Log("shell-checkpoint", "post-state of %s not captured (oversize/budget); not checkpointed", path)
			continue
		}
		if !hadPre {
			// Not dirty before, but appears dirty now (created, or a clean
			// tracked file the command modified). The pre-state of a clean
			// tracked file is its committed content at the recorded HEAD; a
			// path absent from git is a file the command CREATED.
			cur, existed, curCaptured := readSnapshotFileForDiff(after.root, path)
			if !existed {
				// Gone from disk. If it was tracked at HEAD, the command
				// DELETED it - record a deletion checkpoint so undo restores
				// the committed content instead of reporting a misleading
				// success.
				if preContent, preExisted := gitShowFile(s.root, s.head, path); preExisted {
					cpMgr.SaveWithExistence(filepath.Join(after.root, path), preContent, "", toolCall, true)
					created++
				}
				continue
			}
			if !curCaptured {
				debug.Log("shell-checkpoint", "current content of %s not captured; not checkpointed", path)
				continue
			}
			preContent, preExisted := gitShowFile(s.root, s.head, path)
			if !preExisted {
				cpMgr.SaveWithExistence(filepath.Join(after.root, path), "", cur, toolCall, false)
				created++
				continue
			}
			if cur == preContent {
				continue // matches recorded HEAD content -- nothing to undo
			}
			cpMgr.SaveWithExistence(filepath.Join(after.root, path), preContent, cur, toolCall, true)
			created++
			continue
		}
		if pre.content == post.content {
			continue // content unchanged (e.g. index-only churn)
		}
		cur, existed, curCaptured := readSnapshotFileForDiff(after.root, path)
		if existed && curCaptured && cur == pre.content {
			continue // reverted to exactly the pre-state -- nothing to undo
		}
		if existed && !curCaptured {
			debug.Log("shell-checkpoint", "current content of %s not captured; not checkpointed", path)
			continue
		}
		cpMgr.SaveWithExistence(filepath.Join(after.root, path), pre.content, post.content, toolCall, true)
		created++
	}
	// Pass 2: paths dirty before but absent after -- deleted or cleaned.
	for path, pre := range s.files {
		if created >= shellCkptMaxFiles {
			debug.Log("shell-checkpoint", "cap %d reached; remaining shell mutations are NOT undoable", shellCkptMaxFiles)
			break
		}
		if !pre.captured {
			// Uncaptured pre-state: a deletion checkpoint would restore an
			// EMPTY file on undo, a modification checkpoint would zero an
			// existing one. Not restorable => skip (#2441 review fix).
			debug.Log("shell-checkpoint", "pre-state of %s not captured (oversize/budget); not checkpointed", path)
			continue
		}
		if _, stillListed := after.files[path]; stillListed {
			continue
		}
		cur, existed, curCaptured := readSnapshotFileForDiff(after.root, path)
		if existed {
			if !curCaptured {
				debug.Log("shell-checkpoint", "current content of %s not captured; not checkpointed", path)
				continue
			}
			if cur == pre.content {
				continue // restored to exactly the pre-state, nothing to undo
			}
			// Restored to some other state (typically clean HEAD): a real
			// mutation pre-content -> cur. Record it as a MODIFICATION, not a
			// deletion - a deletion checkpoint would make undo DELETE a file
			// that exists on disk.
			cpMgr.SaveWithExistence(filepath.Join(after.root, path), pre.content, cur, toolCall, true)
			created++
			continue
		}
		cpMgr.SaveWithExistence(filepath.Join(after.root, path), pre.content, "", toolCall, true)
		created++
	}
	if created > 0 {
		debug.Log("shell-checkpoint", "run_command %s mutated %d file(s); checkpoints recorded", toolCall, created)
	}
	return created
}

// readSnapshotFileForDiff reads the CURRENT content of a workspace file
// with the same size/budget guards, without consuming the pre-state budget.
// captured=false marks oversized/unreadable files -- callers must not
// checkpoint them (empty newContent would zero the file on redo).
func readSnapshotFileForDiff(root, relPath string) (string, bool, bool) {
	full := filepath.Join(root, relPath)
	fi, err := os.Stat(full)
	if err != nil || !fi.Mode().IsRegular() {
		return "", false, true // absent: state fully represented
	}
	if fi.Size() > shellCkptMaxFileBytes {
		return "", true, false
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return "", true, false
	}
	return string(data), true, true
}

// gitShowFile returns a path's committed content at the given revision.
// ok=false means the path was not tracked at that revision.
func gitShowFile(root, rev, relPath string) (string, bool) {
	if rev == "" {
		return "", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), shellCkptTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "-C", root, "show", rev+":"+relPath).Output()
	if err != nil {
		return "", false
	}
	return string(out), true
}

func isGitWorktree(workDir string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "-C", workDir, "rev-parse", "--is-inside-work-tree").Output()
	return err == nil && strings.TrimSpace(string(out)) == "true"
}
