package tui

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestRemoteSlashDiff_CleanTreeSaysNoChanges(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir

		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	git("commit", "--allow-empty", "-m", "init", "--no-gpg-sign")

	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(oldWD) }()

	m := newTestModel()
	d := tuiSlashDeps{m: &m}
	resp, err := d.GitDiff(nil)
	if err != nil {
		t.Fatalf("GitDiff on clean tree: %v", err)
	}
	if resp != "No changes." {
		t.Fatalf("expected 'No changes.', got %q", resp)
	}
	if strings.TrimSpace(resp) == "" {
		t.Fatal("unreachable guard: empty response on clean tree")
	}
}
