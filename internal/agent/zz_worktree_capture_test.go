package agent

import (
	"os"
	"path/filepath"
	"testing"
)

// #5524 integration: capturePreRunState must not attribute changes inside
// .ggcode/worktrees/<name>/ to the main working tree, while main-tree edits
// still count. Exercises the real git pipeline (runGitCR/mustWriteCR).
func TestCapturePreRunStateSkipsWorktreePaths(t *testing.T) {
	dir := t.TempDir()
	runGitCR(t, dir, "init")
	runGitCR(t, dir, "config", "user.email", "t@t")
	runGitCR(t, dir, "config", "user.name", "t")
	mustWriteCR(t, filepath.Join(dir, "main.go"), "package main\n")
	runGitCR(t, dir, "add", ".")
	runGitCR(t, dir, "commit", "-m", "init")

	// Main-tree edit: must count.
	mustWriteCR(t, filepath.Join(dir, "other.go"), "package main\n")
	// Worktree-scoped edit: must NOT count (#5524).
	wt := filepath.Join(dir, ".ggcode", "worktrees", "fix-5524")
	if err := os.MkdirAll(wt, 0755); err != nil {
		t.Fatal(err)
	}
	mustWriteCR(t, filepath.Join(wt, "w.go"), "package main\n")

	c := newChangeReconcileState()
	c.capturePreRunState(dir)
	if n := c.dirtyFileCount(); n != 1 {
		t.Fatalf("expected exactly 1 main-tree dirty file (worktree edit excluded), got %d", n)
	}
	if c.preRunDirty["other.go"] != true {
		t.Fatalf("main-tree edit missing from preRunDirty: %+v", c.preRunDirty)
	}
	for k := range c.preRunDirty {
		if isWorktreeScopedPath(k) {
			t.Fatalf("worktree-scoped path leaked into preRunDirty: %s", k)
		}
	}
}
