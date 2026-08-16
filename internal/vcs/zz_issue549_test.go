package vcs

// Issue #549 bug E: detached HEAD must display the short commit SHA instead
// of the literal "HEAD" that `git rev-parse --abbrev-ref HEAD` returns with
// exit code 0.

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

func gitCmd(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func initTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitCmd(t, dir, "init", "-b", "main")
	gitCmd(t, dir, "config", "user.email", "test@example.com")
	gitCmd(t, dir, "config", "user.name", "Test")
	gitCmd(t, dir, "commit", "--allow-empty", "-m", "init")
	return dir
}

func TestIssue549BugE_DetachedHeadShowsShortSHA(t *testing.T) {
	dir := initTestRepo(t)
	sha := gitCmd(t, dir, "rev-parse", "HEAD")
	gitCmd(t, dir, "checkout", "--detach", sha)

	g := Git{}
	branch, err := g.CurrentBranch(context.Background(), dir)
	if err != nil {
		t.Fatalf("CurrentBranch: %v", err)
	}
	if branch == "HEAD" {
		t.Fatal("detached HEAD must not surface the literal \"HEAD\" as branch label")
	}
	shortSHA := gitCmd(t, dir, "rev-parse", "--short", "HEAD")
	if branch != shortSHA {
		t.Fatalf("expected short SHA %q, got %q", shortSHA, branch)
	}
}

func TestIssue549BugE_NormalBranchUnchanged(t *testing.T) {
	dir := initTestRepo(t)
	g := Git{}
	branch, err := g.CurrentBranch(context.Background(), dir)
	if err != nil {
		t.Fatalf("CurrentBranch: %v", err)
	}
	if branch != "main" {
		t.Fatalf("expected \"main\" on a normal branch, got %q", branch)
	}
}
