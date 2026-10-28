//go:build goolm

package a2a

import (
	"os"
	"path/filepath"
	"testing"
)

// Issue #4562: in a git worktree or submodule, .git is a FILE ("gitdir: ...")
// rather than a directory; the old IsDir() check reported HasGit=false there,
// which blocked the git-ops skill and published a wrong agent card.
func TestIssue4562HasGitWorktreeFile(t *testing.T) {
	dir := t.TempDir()

	// Regular .git directory -> HasGit true (pre-existing behavior).
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if meta := detectWorkspaceMeta(dir); !meta.HasGit {
		t.Errorf("directory .git should set HasGit=true")
	}

	// Worktree/submodule .git file -> must also be HasGit=true (#4562).
	wt := t.TempDir()
	if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: /elsewhere/main/.git/worktrees/wt1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if meta := detectWorkspaceMeta(wt); !meta.HasGit {
		t.Errorf(".git file (worktree/submodule) should set HasGit=true, got false")
	}

	// No .git at all -> HasGit false.
	none := t.TempDir()
	if meta := detectWorkspaceMeta(none); meta.HasGit {
		t.Errorf("no .git should keep HasGit=false")
	}
}
