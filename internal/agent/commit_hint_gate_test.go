package agent

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommitHint_NoCodeChanged(t *testing.T) {
	a := &Agent{commitHint: newCommitHintState()}
	stats := &RunStats{ToolCalls: map[string]int{"read_file": 1}}
	if msg := a.checkCommitHintGate(stats); msg != "" {
		t.Errorf("expected empty message when no code changed, got: %s", msg)
	}
}

func TestCommitHint_AlreadyCommitted(t *testing.T) {
	a := &Agent{commitHint: newCommitHintState()}
	stats := &RunStats{ToolCalls: map[string]int{
		"edit_file":  1,
		"git_add":    1,
		"git_commit": 1,
	}}
	if msg := a.checkCommitHintGate(stats); msg != "" {
		t.Errorf("expected empty message when agent already committed, got: %s", msg)
	}
}

func TestCommitHint_AlreadyFired(t *testing.T) {
	a := &Agent{commitHint: newCommitHintState()}
	a.commitHint.fired = true
	stats := &RunStats{ToolCalls: map[string]int{"edit_file": 1}}
	if msg := a.checkCommitHintGate(stats); msg != "" {
		t.Errorf("expected empty message when gate already fired, got: %s", msg)
	}
}

func TestCommitHint_WithUncommittedChanges(t *testing.T) {
	dir := t.TempDir()
	runGitCommitTest(t, dir, "init")
	runGitCommitTest(t, dir, "config", "user.email", "test@test.com")
	runGitCommitTest(t, dir, "config", "user.name", "test")

	writeFileCommitTest(t, dir, "main.go", "package main\n")
	runGitCommitTest(t, dir, "add", "main.go")
	runGitCommitTest(t, dir, "commit", "-m", "initial")

	writeFileCommitTest(t, dir, "main.go", "package main\n\nfunc main() {}\n")

	a := &Agent{commitHint: newCommitHintState()}
	a.SetWorkingDir(dir)

	// #698: the hint scopes to the agent's own edit list (absolute paths)
	// intersected with the dirty tree.
	stats := &RunStats{
		ToolCalls:   map[string]int{"edit_file": 1},
		FilesEdited: []string{filepath.Join(dir, "main.go")},
	}
	msg := a.checkCommitHintGate(stats)
	if msg == "" {
		t.Fatal("expected non-empty commit hint message")
	}
	if !strings.Contains(msg, "1 file") {
		t.Errorf("expected message to mention '1 file', got: %s", msg)
	}
	if !strings.Contains(msg, "git_add") || !strings.Contains(msg, "git_commit") {
		t.Errorf("expected message to mention git_add and git_commit, got: %s", msg)
	}
}

func TestCommitHint_NoUncommittedChanges(t *testing.T) {
	dir := t.TempDir()
	runGitCommitTest(t, dir, "init")
	runGitCommitTest(t, dir, "config", "user.email", "test@test.com")
	runGitCommitTest(t, dir, "config", "user.name", "test")
	writeFileCommitTest(t, dir, "main.go", "package main\n")
	runGitCommitTest(t, dir, "add", "main.go")
	runGitCommitTest(t, dir, "commit", "-m", "initial")

	a := &Agent{commitHint: newCommitHintState()}
	a.SetWorkingDir(dir)

	stats := &RunStats{ToolCalls: map[string]int{"edit_file": 1}}
	if msg := a.checkCommitHintGate(stats); msg != "" {
		t.Errorf("expected empty message when no uncommitted changes, got: %s", msg)
	}
}

func TestCommitHint_Reset(t *testing.T) {
	c := newCommitHintState()
	c.fired = true
	c.reset()
	if c.fired {
		t.Error("expected fired=false after reset")
	}
}

func TestCountChangedFiles(t *testing.T) {
	porcelain := " M main.go\n?? new.go\nA  added.go\n"
	count := countChangedFiles(porcelain)
	if count != 3 {
		t.Errorf("expected 3 changed files, got %d", count)
	}
	if countChangedFiles("") != 0 {
		t.Error("expected 0 for empty input")
	}
	if countChangedFiles("\n\n") != 0 {
		t.Error("expected 0 for whitespace-only input")
	}
}

// TestCommitHint_StashDoesNotSuppressHint pins #698's contract change: a
// git_stash round trip (push + pop) leaves the agent's edits uncommitted in
// the tree — exactly what this gate exists to flag. Only git_add/git_commit
// suppress the hint.
func TestCommitHint_StashDoesNotSuppressHint(t *testing.T) {
	dir := t.TempDir()
	runGitCommitTest(t, dir, "init")
	runGitCommitTest(t, dir, "config", "user.email", "test@test.com")
	runGitCommitTest(t, dir, "config", "user.name", "test")
	writeFileCommitTest(t, dir, "main.go", "package main\n")
	runGitCommitTest(t, dir, "add", "main.go")
	runGitCommitTest(t, dir, "commit", "-m", "initial")
	writeFileCommitTest(t, dir, "main.go", "package main\n\nfunc main() {}\n")

	a := &Agent{commitHint: newCommitHintState()}
	a.SetWorkingDir(dir)

	stats := &RunStats{
		ToolCalls: map[string]int{
			"edit_file": 1,
			"git_stash": 2, // push + pop round trip
		},
		FilesEdited: []string{filepath.Join(dir, "main.go")},
	}
	if msg := a.checkCommitHintGate(stats); msg == "" {
		t.Error("expected commit hint despite git_stash round trip — stash is not version-control completion")
	}
}

func runGitCommitTest(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
}

func writeFileCommitTest(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
		t.Fatalf("writeFile %s: %v", name, err)
	}
}
