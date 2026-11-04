package agent

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Issue #5018: the adversarial review gate is per-agent but
// collectAdversarialDiff ran a bare `git diff HEAD` over the whole shared
// working tree, so other agents' in-flight changes were attributed to this
// run. The diff must be scoped to RunStats.FilesEdited.

func gitCmd5018(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func TestIssue5018DiffScopedToEditedPaths(t *testing.T) {
	dir := t.TempDir()
	gitCmd5018(t, dir, "init", "-q")
	os.WriteFile(filepath.Join(dir, "mine.go"), []byte("package p\n\nconst A = 1\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "theirs.go"), []byte("package p\n\nconst B = 2\n"), 0o644)
	gitCmd5018(t, dir, "add", ".")
	gitCmd5018(t, dir, "commit", "-q", "-m", "base")

	// Simulate a shared tree: this agent edits mine.go, another agent edits
	// theirs.go and leaves an untracked leftover.
	os.WriteFile(filepath.Join(dir, "mine.go"), []byte("package p\n\nconst A = 2\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "theirs.go"), []byte("package p\n\nconst B = 99\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "mynew.go"), []byte("package p\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "theirleftover.txt"), []byte("junk\n"), 0o644)

	a := &Agent{}
	a.SetWorkingDir(dir)

	diff := a.collectAdversarialDiff([]string{"mine.go", "mynew.go"})
	if !strings.Contains(diff, "const A = 2") {
		t.Fatalf("scoped diff must contain this run's edit to mine.go:\n%s", diff)
	}
	if strings.Contains(diff, "const B = 99") {
		t.Fatalf("scoped diff must NOT attribute another agent's theirs.go edit (#5018):\n%s", diff)
	}
	if !strings.Contains(diff, "mynew.go") {
		t.Fatalf("scoped diff must list this run's untracked mynew.go:\n%s", diff)
	}
	if strings.Contains(diff, "theirleftover.txt") {
		t.Fatalf("scoped diff must NOT list another agent's untracked leftover (#5018):\n%s", diff)
	}
}

func TestIssue5018WholeTreeFallbackWhenNoPaths(t *testing.T) {
	dir := t.TempDir()
	gitCmd5018(t, dir, "init", "-q")
	os.WriteFile(filepath.Join(dir, "x.go"), []byte("package p\n\nconst X = 1\n"), 0o644)
	gitCmd5018(t, dir, "add", ".")
	gitCmd5018(t, dir, "commit", "-q", "-m", "base")
	os.WriteFile(filepath.Join(dir, "x.go"), []byte("package p\n\nconst X = 2\n"), 0o644)

	a := &Agent{}
	a.SetWorkingDir(dir)

	// Empty path list keeps the historical whole-tree behavior.
	diff := a.collectAdversarialDiff(nil)
	if !strings.Contains(diff, "const X = 2") {
		t.Fatalf("unscoped diff must still include the edit:\n%s", diff)
	}
}

func TestUntrackedLineInPaths(t *testing.T) {
	wd := t.TempDir()
	paths := []string{"a.go", "pkg"}
	cases := []struct {
		line string
		want bool
	}{
		{"?? a.go", true},
		{"?? pkg/file.go", true},
		{"?? pkgx/file.go", false}, // prefix must not match past a path separator
		{"?? other.go", false},
		{"?? ", false},
		{"M  a.go", false}, // not an untracked line
	}
	for _, tc := range cases {
		if got := untrackedLineInPaths(wd, tc.line, paths); got != tc.want {
			t.Errorf("untrackedLineInPaths(%q) = %v, want %v", tc.line, got, tc.want)
		}
	}
}
