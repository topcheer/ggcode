package tool

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(cmd.Environ(), "GIT_TEMPLATE_DIR=")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %s: %v", args, string(out), err)
	}
}

func TestIssue4802_BranchCollisionGuidance(t *testing.T) {
	dir := initTestGitRepo(t)
	tool := EnterWorktree{WorkingDir: dir}

	gitIn(t, dir, "branch", "collide4802")

	res, err := tool.Execute(context.Background(), json.RawMessage(`{"name":"collide4802"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatalf("collision must be an error, got: %s", res.Content)
	}
	for _, want := range []string{"already exists", "head:", "Reuse the existing branch", "different worktree name", "worktree add -B"} {
		if !strings.Contains(res.Content, want) {
			t.Fatalf("guidance missing %q in: %s", want, res.Content)
		}
	}
	gitIn(t, dir, "worktree", "prune")
}

func TestIssue4802_HeldByWorktreePointsAtIt(t *testing.T) {
	dir := initTestGitRepo(t)
	tool := EnterWorktree{WorkingDir: dir}

	res, err := tool.Execute(context.Background(), json.RawMessage(`{"name":"held4802"}`))
	if err != nil || res.IsError {
		t.Fatalf("first creation failed: %v %s", err, res.Content)
	}

	gitIn(t, dir, "worktree", "remove", "--force", dir+"/.ggcode/worktrees/held4802")
	res2, err2 := tool.Execute(context.Background(), json.RawMessage(`{"name":"held4802"}`))
	if err2 != nil {
		t.Fatal(err2)
	}
	if res2.IsError && !strings.Contains(res2.Content, "already exists") {
		t.Fatalf("unexpected error: %s", res2.Content)
	}
	gitIn(t, dir, "worktree", "prune")
}
