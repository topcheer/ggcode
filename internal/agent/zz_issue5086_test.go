package agent

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// #5086: `git status --porcelain` emits repo-ROOT-relative paths even when the
// agent workingDir is a repo subdirectory. untrackedLineInPaths normalized
// those paths against workingDir, turning "sub/file.go" into a phantom
// <subdir>/sub/file.go that no scope matched - agent-created files under a
// subdir workingDir escaped adversarial review. The fix anchors normalization
// at `git rev-parse --show-toplevel` (fallback: workingDir outside a repo).
func TestIssue5086SubdirWorkingDirUntracked(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-q")
	sub := filepath.Join(root, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(sub, "file.go")
	if err := os.WriteFile(target, []byte("package sub\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Porcelain path is ROOT-relative ("sub/file.go") while the agent's scope
	// is absolute inside the subdir: must match (escaped before the fix).
	line := "?? sub/file.go"
	if !untrackedLineInPaths(sub, line, []string{target}) {
		t.Errorf("subdir abs scope missed: %q vs %s", line, target)
	}
	// Dir-entry reverse containment under the subdir.
	if !untrackedLineInPaths(sub, "?? sub/", []string{filepath.Join(sub, "inner.go")}) {
		t.Errorf("subdir dir-entry containment missed")
	}
	// Unrelated root-level untracked file must NOT match.
	other := filepath.Join(root, "other.go")
	if err := os.WriteFile(other, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if untrackedLineInPaths(sub, "?? other.go", []string{target}) {
		t.Errorf("root-level untracked wrongly matched subdir scope")
	}
}

// Regression: outside a git repo the fallback anchor (workingDir) preserves
// the pre-#5086 frame.
func TestIssue5086NonGitFallback(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	wd := t.TempDir() // no git init here
	f := filepath.Join(wd, "new.go")
	if !untrackedLineInPaths(wd, "?? new.go", []string{f}) {
		t.Errorf("non-git fallback frame broken")
	}
}
