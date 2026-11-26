package tool

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// review_changes must not report the harness directory: its (often tracked,
// always churning) files are not the user's change. Covers the diff
// pathspec and the untracked scan.
func TestReviewChangesExcludesHarnessState(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		c := exec.Command("git", args...)
		c.Dir = dir
		c.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1",
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(rel, content string) {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("init", "-q")
	write("main.go", "package main\n")
	write(".ggcode/memory/tracked.md", "v1\n")
	run("add", "-f", ".")
	run("commit", "-q", "-m", "base")
	write("main.go", "package main\n\nfunc main() {}\n")
	write(".ggcode/memory/tracked.md", "v2\n")
	write(".ggcode/memory/experience/untracked.md", "x\n")
	write("notes.txt", "user note\n")

	ctx := context.Background()
	diff, err := getReviewDiff(ctx, dir, "all")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(diff, ".ggcode") || !strings.Contains(diff, "main.go") {
		t.Fatalf("diff must exclude harness state and keep the real change:\n%s", diff)
	}
	untracked := getUntrackedFiles(ctx, dir)
	joined := strings.Join(untracked, "\n")
	if strings.Contains(joined, ".ggcode") {
		t.Fatalf("untracked scan must skip harness state: %q", joined)
	}
	if !strings.Contains(joined, "notes.txt") {
		t.Fatalf("untracked scan must keep the user's file: %q", joined)
	}
}
