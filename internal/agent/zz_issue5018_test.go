package agent

import (
	"path/filepath"
	"strings"
	"testing"
)

// #5018: adversarial review must diff only the files this run edited.
// On a shared worktree, another agent's in-flight edits must not leak into
// the diff that gets attributed to this agent as "your diff".

func TestAdversarialDiffScopedToEditedPaths(t *testing.T) {
	dir := initGitRepo(t)
	mine := filepath.Join(dir, "mine.go")
	theirs := filepath.Join(dir, "theirs.go")
	mustWriteCR(t, mine, "package main\n")
	mustWriteCR(t, theirs, "package main\n")
	runGitCR(t, dir, "add", ".")
	runGitCR(t, dir, "commit", "-m", "init")

	mustWriteCR(t, mine, "package main\n// my edit\n")
	mustWriteCR(t, theirs, "package main\n// other agent edit\n")

	a := &Agent{workingDir: dir}
	diff := a.collectAdversarialDiff(adversarialDiffPaths(dir, &RunStats{FilesEdited: []string{mine}}))
	if !strings.Contains(diff, "my edit") {
		t.Fatalf("edited file missing from scoped diff:\n%s", diff)
	}
	if strings.Contains(diff, "other agent edit") {
		t.Fatalf("foreign tracked edit leaked into scoped diff:\n%s", diff)
	}
}

func TestAdversarialDiffUntrackedScopedToEditedPaths(t *testing.T) {
	dir := initGitRepo(t)
	base := filepath.Join(dir, "base.go")
	mustWriteCR(t, base, "package main\n")
	runGitCR(t, dir, "add", ".")
	runGitCR(t, dir, "commit", "-m", "init")

	mineNew := filepath.Join(dir, "mine_new.go")
	theirNew := filepath.Join(dir, "their_new.go")
	mustWriteCR(t, mineNew, "package main\n")
	mustWriteCR(t, theirNew, "package main\n")

	a := &Agent{workingDir: dir}
	diff := a.collectAdversarialDiff(adversarialDiffPaths(dir, &RunStats{FilesEdited: []string{mineNew}}))
	if !strings.Contains(diff, "mine_new.go") {
		t.Fatalf("own untracked file missing from listing:\n%s", diff)
	}
	if strings.Contains(diff, "their_new.go") {
		t.Fatalf("foreign untracked file leaked into listing:\n%s", diff)
	}
}

func TestAdversarialDiffEmptyPathsFallsBackToFullDiff(t *testing.T) {
	dir := initGitRepo(t)
	f := filepath.Join(dir, "a.go")
	mustWriteCR(t, f, "package main\n")
	runGitCR(t, dir, "add", ".")
	runGitCR(t, dir, "commit", "-m", "init")
	mustWriteCR(t, f, "package main\n// changed\n")

	a := &Agent{workingDir: dir}
	diff := a.collectAdversarialDiff(adversarialDiffPaths(dir, &RunStats{}))
	if !strings.Contains(diff, "changed") {
		t.Fatalf("nil paths must keep full-worktree diff fallback:\n%s", diff)
	}
}

func TestAdversarialDiffPathsNormalizeAndDedup(t *testing.T) {
	dir := initGitRepo(t)
	abs := filepath.Join(dir, "x.go")
	got := adversarialDiffPaths(dir, &RunStats{FilesEdited: []string{abs, "x.go", ""}})
	if len(got) != 1 || got[0] != "x.go" {
		t.Fatalf("want normalized deduped [x.go], got %v", got)
	}
	if adversarialDiffPaths(dir, nil) != nil {
		t.Fatal("nil runStats must yield nil paths")
	}
}
