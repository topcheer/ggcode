package tool

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// #5212: discard_changes=true used to silently authorize `git branch -D` on
// branches carrying unpushed commits (a completed local fix awaiting PR was
// lost that way). The flag consents to UNCOMMITTED FILE changes only; branch
// deletion now needs the dedicated force_delete_branch override.
func TestIssue5212DiscardChangesKeepsUnpushedBranch(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	origin := root + "/origin.git"
	mainRepo := root + "/main"

	run := func(args ...string) {
		t.Helper()
		out, err := exec.Command("git", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	run("init", "--bare", "-b", "main", origin)
	run("clone", origin, mainRepo)
	run("-C", mainRepo, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "--allow-empty", "-m", "base")
	run("-C", mainRepo, "push", "origin", "main")

	// Worktree branch with one COMMIT (clean status) that was never pushed.
	// Must live under <main>/.ggcode/worktrees (the enter_worktree domain)
	// and mirror the real convention: worktree dir name == branch name -
	// that is the shape where the branch probes engage at all.
	wtPath := root + "/main/.ggcode/worktrees/wt-5201"
	run("-C", mainRepo, "worktree", "add", "-b", "wt-5201", wtPath, "main")
	run("-C", wtPath, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "--allow-empty", "-m", "local fix awaiting PR")

	tw := ExitWorktree{WorkingDir: wtPath}
	input := func(discard, force bool) json.RawMessage {
		return json.RawMessage(`{"action":"remove","description":"t","discard_changes":` +
			boolStr(discard) + `,"force_delete_branch":` + boolStr(force) + `}`)
	}

	// Case 1: discard_changes=true alone must NOT delete the unpushed branch.
	res, err := tw.Execute(ctx, input(true, false))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", res.Content)
	}
	if branchGone(t, mainRepo, "wt-5201") {
		t.Fatalf("BUG #5212: discard_changes=true silently deleted branch with unpushed commits")
	}
	if !allContained(res.Content, "branch kept", "tip SHAs:") {
		t.Fatalf("keep result missing evidence: %s", res.Content)
	}

	// Case 2: the dedicated override deletes it, naming the SHAs.
	// Case 1 removed the worktree dir; re-attach it to the (kept) branch.
	run("-C", mainRepo, "worktree", "add", wtPath, "wt-5201")
	res, err = tw.Execute(ctx, input(true, true))
	if err != nil {
		t.Fatalf("execute(force): %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result (force): %s", res.Content)
	}
	if !branchGone(t, mainRepo, "wt-5201") {
		t.Fatalf("force_delete_branch=true did not delete the branch")
	}
	if !allContained(res.Content, "WARNING", "force-deleted branch", "tip SHAs:") {
		t.Fatalf("force result missing SHA evidence: %s", res.Content)
	}
}

func branchGone(t *testing.T, repo, branch string) bool {
	t.Helper()
	out, err := exec.Command("git", "-C", repo, "rev-parse", "--verify", "--quiet", branch).Output()
	if err != nil {
		return true
	}
	return len(out) == 0
}

func allContained(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
