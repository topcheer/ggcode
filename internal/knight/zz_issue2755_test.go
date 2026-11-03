package knight

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestIssue2755RenameBothSidesStrippedInSubdir(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "t@t")
	run("config", "user.name", "t")

	sub := filepath.Join(repo, "mobile", ".ggcode")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	old := filepath.Join(sub, "a.md")
	if err := os.WriteFile(old, []byte("x"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	run("add", "-A")
	run("commit", "-qm", "init")

	mv := exec.Command("git", "mv", ".ggcode/a.md", ".ggcode/b.md")
	mv.Dir = filepath.Join(repo, "mobile")
	if out, err := mv.CombinedOutput(); err != nil {
		t.Fatalf("git mv: %v\n%s", err, out)
	}

	snap := gitStatusSnapshot(filepath.Join(repo, "mobile"))
	var renameLine string
	for _, line := range strings.Split(snap, "\n") {
		if strings.HasPrefix(line, "R") && strings.Contains(line, "->") {
			renameLine = line
		}
	}
	if renameLine == "" {
		t.Fatalf("no staged rename line in snapshot:\n%s", snap)
	}
	if strings.Contains(renameLine, "mobile/") {
		t.Fatalf("rename line keeps show-prefix on a side: %q (want both sides dir-relative)", renameLine)
	}
	if renameLine != "R  .ggcode/a.md -> .ggcode/b.md" {
		t.Fatalf("unexpected normalized rename line: %q", renameLine)
	}
}
