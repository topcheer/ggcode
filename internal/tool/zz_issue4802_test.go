package tool

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// gitIn runs git in dir (tests must never touch the real repo checkout -
// a missing cmd.Dir once created a stray branch in the working repo).
func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(cmd.Environ(), "GIT_TEMPLATE_DIR=")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %s: %v", args, string(out), err)
	}
}

// #4802 (S-34): when `git worktree add -b <name>` collides with a branch
// that already exists in the main tree, the bare git fatal used to dead-end
// agents (inducing main-tree checkout fallback). The error must now carry
// the existing branch's head and actionable next moves.
func TestIssue4802_BranchCollisionGuidance(t *testing.T) {
	dir := initTestGitRepo(t)
	tool := EnterWorktree{WorkingDir: dir}

	// Create the colliding branch in the main tree.
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

// #4802: when the colliding branch is held by a live worktree, the guidance
// must point at that worktree (reuse it) instead of suggesting add (which
// git refuses for a checked-out branch).
func TestIssue4802_HeldByWorktreePointsAtIt(t *testing.T) {
	dir := initTestGitRepo(t)
	tool := EnterWorktree{WorkingDir: dir}

	// First worktree on branch held4802 succeeds.
	res, err := tool.Execute(context.Background(), json.RawMessage(`{"name":"held4802"}`))
	if err != nil || res.IsError {
		t.Fatalf("first creation failed: %v %s", err, res.Content)
	}
	// Free the branch name at the path level (dir entry) but the branch
	// stays held in git's eyes only while the worktree exists; after
	// removal the branch is free, so a retry must succeed (no regression).
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
