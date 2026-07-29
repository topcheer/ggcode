package vcs

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

func TestSummary_NotARepo(t *testing.T) {
	got := Summary(context.Background(), t.TempDir())
	if !strings.Contains(got, "not a version-controlled repository") {
		t.Fatalf("expected not-a-repo message, got %q", got)
	}
}

func TestSummary_GitCleanRepo(t *testing.T) {
	if !hasBinary("git") {
		t.Skip("git not installed")
	}
	dir := setupGitRepo(t)
	got := Summary(context.Background(), dir)
	if !strings.Contains(got, "Git repository") {
		t.Errorf("expected 'Git repository' in %q", got)
	}
	if !strings.Contains(got, "clean working tree") {
		t.Errorf("expected 'clean working tree' in %q", got)
	}
}

func TestSummary_GitDirtyRepo(t *testing.T) {
	if !hasBinary("git") {
		t.Skip("git not installed")
	}
	dir := setupGitRepo(t)
	writeFile(t, dir, "new.txt", "new\n")
	writeFile(t, dir, "other.txt", "other\n")
	got := Summary(context.Background(), dir)
	if !strings.Contains(got, "uncommitted file") {
		t.Errorf("expected 'uncommitted file' in %q", got)
	}
	if !strings.Contains(got, "2") {
		t.Errorf("expected file count 2 in %q", got)
	}
}

func TestSummary_GitBranch(t *testing.T) {
	if !hasBinary("git") {
		t.Skip("git not installed")
	}
	dir := setupGitRepo(t)
	got := Summary(context.Background(), dir)
	if !strings.Contains(got, "on main") && !strings.Contains(got, "on master") {
		t.Errorf("expected branch name in %q", got)
	}
}

func TestSummary_GitDetachedHead(t *testing.T) {
	if !hasBinary("git") {
		t.Skip("git not installed")
	}
	dir := setupGitRepo(t)
	// Add a second commit then detach to first
	c := exec.Command("git", "add", "f2.txt")
	c.Dir = dir
	setGitEnv(c)
	writeFile(t, dir, "f2.txt", "x\n")
	c = exec.Command("git", "add", "f2.txt")
	c.Dir = dir
	setGitEnv(c)
	c.Run()

	c = exec.Command("git", "commit", "-m", "second")
	c.Dir = dir
	setGitEnv(c)
	c.Run()

	// Detach to first commit
	c = exec.Command("git", "rev-parse", "HEAD~1")
	c.Dir = dir
	out, _ := c.Output()
	sha := strings.TrimSpace(string(out))

	c = exec.Command("git", "checkout", sha)
	c.Dir = dir
	setGitEnv(c)
	c.Run()

	got := Summary(context.Background(), dir)
	// Should not crash and should still mention Git repository
	if !strings.Contains(got, "Git repository") {
		t.Errorf("expected Git repository in %q", got)
	}
}

func TestCountStatusLines(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"", 0},
		{" M file.go\n", 1},
		{" M a.go\n M b.go\n", 2},
		{"\n\n\n", 0},
	}
	for _, tt := range tests {
		got := countStatusLines(tt.input)
		if got != tt.want {
			t.Errorf("countStatusLines(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestGitAheadBehind_NoUpstream(t *testing.T) {
	if !hasBinary("git") {
		t.Skip("git not installed")
	}
	dir := setupGitRepo(t)
	g := Git{}
	ahead, behind, ok := g.AheadBehind(context.Background(), dir)
	if ok {
		t.Errorf("expected ok=false for repo with no upstream, got ahead=%d behind=%d", ahead, behind)
	}
}
