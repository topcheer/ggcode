package agent

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
	shellCkptMaxFiles     = 40
	shellCkptMaxFileBytes = 1 << 20
	shellCkptGlobalBytes  = 20 << 20
	shellCkptTimeout      = 10 * time.Second
)

var shellCkptSkipDirs = map[string]bool{
	"node_modules": true, "vendor": true, "__pycache__": true,
	".venv": true, "venv": true, "dist": true, "target": true,
	".ggcode": true, ".git": true,
}

type shellFileState struct {
	content  string
	existed  bool
	captured bool
}

type shellSnapshot struct {
	root  string
	head  string
	files map[string]shellFileState
}

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
			break
		}
		status, path := entry.status, entry.path
		if path == "" || strings.HasSuffix(path, "/") {
			continue
		}
		if status == "??" && skipUntrackedPath(path) {
			continue
		}
		content, existed, captured := readSnapshotFile(filepath.Join(workDir, path), &budget)
		snap.files[path] = shellFileState{content: content, existed: existed, captured: captured}
	}
	return snap
}

type porcelainEntry struct {
	status string
	path   string
}

func splitPorcelainZ(out []byte) []porcelainEntry {
	var entries []porcelainEntry
	parts := bytes.Split(out, []byte{0})
	for i := 0; i < len(parts); i++ {
		p := parts[i]
		if len(p) < 4 {
			continue
		}
		status := string(p[:2])
		path := string(p[3:])
		if status[0] == 'R' || status[0] == 'C' {
			i++
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

func readSnapshotFile(path string, budget *int) (string, bool, bool) {
	fi, err := os.Stat(path)
	if err != nil {
		return "", false, true
	}
	if !fi.Mode().IsRegular() {
		return "", false, true
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

func (s *shellSnapshot) checkpointMutations(after *shellSnapshot, toolCall string, cpMgr *checkpoint.Manager) int {
	if s == nil || after == nil || cpMgr == nil {
		return 0
	}
	created := 0

	for path, post := range after.files {
		if created >= shellCkptMaxFiles {
			break
		}
		pre, hadPre := s.files[path]
		if hadPre && !pre.captured {

			debug.Log("shell-checkpoint", "pre-state of %s not captured (oversize/budget); not checkpointed", path)
			continue
		}
		if !post.captured {

			debug.Log("shell-checkpoint", "post-state of %s not captured (oversize/budget); not checkpointed", path)
			continue
		}
		if !hadPre {

			cur, existed, curCaptured := readSnapshotFileForDiff(after.root, path)
			if !existed {

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
				continue
			}
			cpMgr.SaveWithExistence(filepath.Join(after.root, path), preContent, cur, toolCall, true)
			created++
			continue
		}
		if pre.content == post.content {
			continue
		}
		cur, existed, curCaptured := readSnapshotFileForDiff(after.root, path)
		if existed && curCaptured && cur == pre.content {
			continue
		}
		if existed && !curCaptured {
			debug.Log("shell-checkpoint", "current content of %s not captured; not checkpointed", path)
			continue
		}
		cpMgr.SaveWithExistence(filepath.Join(after.root, path), pre.content, post.content, toolCall, true)
		created++
	}

	for path, pre := range s.files {
		if created >= shellCkptMaxFiles {
			debug.Log("shell-checkpoint", "cap %d reached; remaining shell mutations are NOT undoable", shellCkptMaxFiles)
			break
		}
		if !pre.captured {

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
				continue
			}

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

func readSnapshotFileForDiff(root, relPath string) (string, bool, bool) {
	full := filepath.Join(root, relPath)
	fi, err := os.Stat(full)
	if err != nil || !fi.Mode().IsRegular() {
		return "", false, true
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
