package agent

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

const companionGitTimeout = 5 * time.Second

// worktreeBaseCommit resolves the commit a ggcode-managed worktree was created
// from: the merge-base of the checked-out branch and main. Outside a worktree
// (or when git is unavailable) it returns "" and provenance refresh is skipped.
func worktreeBaseCommit(workDir string) string {
	if workDir == "" {
		return ""
	}
	branch, err := runGitCommandWithTimeout(gitCommand(workDir, "branch", "--show-current"), companionGitTimeout)
	if err != nil || strings.TrimSpace(branch) == "" || strings.TrimSpace(branch) == "main" {
		return ""
	}
	if !isGGCodeWorktree(workDir) {
		return ""
	}
	baseOut, err := runGitCommandWithTimeout(gitCommand(workDir, "merge-base", "HEAD", "main"), companionGitTimeout)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(baseOut)
}

// isGGCodeWorktree reports whether workDir is a worktree living under the
// repo's .ggcode/worktrees/ directory (enter_worktree / spawn_agent layout).
func isGGCodeWorktree(workDir string) bool {
	abs, err := filepath.Abs(workDir)
	if err != nil {
		abs = filepath.Clean(workDir)
	}
	for cur := abs; ; cur = filepath.Dir(cur) {
		if filepath.Base(cur) == "worktrees" && filepath.Base(filepath.Dir(cur)) == ".ggcode" {
			return true
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return false
		}
	}
}

// gitChangedSince lists files changed in the working tree relative to base
// (committed-on-branch plus uncommitted), slash-separated and repo-relative.
func gitChangedSince(workDir, base string) []string {
	out, err := runGitCommandWithTimeout(gitCommand(workDir, "diff", "--name-only", base), companionGitTimeout)
	if err != nil {
		debug.Log("agent", "companion provenance: git diff --name-only %s failed: %v", base, err)
		return nil
	}
	files := make([]string, 0, 8)
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			files = append(files, l)
		}
	}

	if st, err := runGitCommandWithTimeout(gitCommand(workDir, "status", "--porcelain", "-uall"), companionGitTimeout); err == nil {
		seen := make(map[string]bool, len(files))
		for _, f := range files {
			seen[f] = true
		}
		for _, line := range strings.Split(st, "\n") {
			if !strings.HasPrefix(line, "??") {
				continue
			}
			f := strings.TrimSpace(line[2:])
			if f == "" || seen[f] {
				continue
			}
			seen[f] = true
			files = append(files, f)
		}
	}
	return files
}
