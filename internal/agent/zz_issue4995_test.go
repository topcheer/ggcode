package agent

import (
	"strings"
	"testing"
)

// #4995: raw `git worktree add` without -b must fail fast with a hint at
// creation time; explicit -b/-B must stay silent; late push failures with the
// detached-HEAD signature get the rescue hint.
func TestIssue4995RawWorktreeAddWithoutBranchFlags(t *testing.T) {
	cases := []struct {
		name string
		cmd  string
		want bool
	}{
		{"bare add", "git worktree add .ggcode/worktrees/x origin/main", true},
		{"cd chained add", "cd /repo && git worktree add /tmp/wt 892dd4c0d", true},
		{"add with -b", "git worktree add -b fix-x .ggcode/worktrees/x origin/main", false},
		{"add with -B", "git worktree add -B fix-x .ggcode/worktrees/x origin/main", false},
		{"add --detach explicit", "git worktree add --detach /tmp/wt main", false},
	}
	for _, tc := range cases {
		got := worktreeDetachedHint("run_command", tc.cmd, "HEAD is now at 892dd4c0d", false)
		if (got != "") != tc.want {
			t.Fatalf("%s: hint=%v want=%v (got: %s)", tc.name, got != "", tc.want, got)
		}
	}
}

func TestIssue4995DetachedPushFailureRescued(t *testing.T) {
	out := "error: src refspec fix-x does not match any\nerror: failed to push some refs"
	if got := worktreeDetachedHint("run_command", "git push origin HEAD:fix-x", out, true); got == "" {
		t.Fatal("push failure with detached-HEAD signature must yield the rescue hint")
	}
	// Unrelated push failure text stays silent.
	if got := worktreeDetachedHint("run_command", "git push origin main", "remote rejected: protected branch", true); got != "" {
		t.Fatalf("unrelated push failure must stay silent, got: %s", got)
	}
}

func TestIssue4995NonShellToolsAndFailuresSilent(t *testing.T) {
	if got := worktreeDetachedHint("read_file", "git worktree add /tmp/wt main", "", false); got != "" {
		t.Fatalf("non-shell tool must stay silent, got: %s", got)
	}
	if got := worktreeDetachedHint("run_command", "git worktree add /tmp/wt main", "fatal: invalid reference", true); got != "" {
		t.Fatalf("failed worktree add must stay silent (the error already speaks), got: %s", got)
	}
	if !strings.Contains(worktreeDetachedGuidance, "git switch -c") {
		t.Fatal("guidance must contain the switch -c recovery path")
	}
}
