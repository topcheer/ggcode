package tui

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// GitDiff on a clean tree must return a non-empty response ("No changes.")
// rather than the empty string git prints with exit 0 - the one-shot command
// contract (parity test + IM bridge) requires every handled command to say
// something. 44bee8bb.
func TestRemoteSlashDiff_CleanTreeSaysNoChanges(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		// CI runners may lack a global git identity (Release workflow flake:
		// git commit exit 128 "Please tell me who you are"). Provide one
		// explicitly so the test never depends on ambient config.
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

	// workingDirFromModel resolves via os.Getwd, so chdir into the repo for
	// the duration of the test.
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
