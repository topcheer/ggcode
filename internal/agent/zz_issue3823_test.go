package agent

// #3823 companion: git add/commit are tree-preserving (index/ref only),
// while `git worktree remove` deletes a whole directory and must NOT be
// exempted from the invalidation warning.

import "testing"

func TestIssue3823_CommitAddTreePreserving(t *testing.T) {
	for _, line := range []string{
		"git add -A",
		"git commit -m \"release: v1.2.3\"",
		"git -C /other/repo commit -am x",
		"git init",
		"git clone https://example.com/r.git",
	} {
		ro, found := classifyGitCommandLine(line)
		if !found || !ro {
			t.Errorf("%q must classify read-only/tree-preserving, got found=%v ro=%v", line, found, ro)
		}
	}
}

func TestIssue3823_WorktreeSecondLevel(t *testing.T) {
	for _, line := range []string{
		"git worktree list",
		"git worktree add .ggcode/worktrees/x -b x origin/main",
	} {
		if ro, found := classifyGitCommandLine(line); !found || !ro {
			t.Errorf("%q must stay tree-preserving, got found=%v ro=%v", line, found, ro)
		}
	}
	for _, line := range []string{
		"git worktree remove .ggcode/worktrees/x --force",
		"git worktree prune",
		"git worktree lock .ggcode/worktrees/x",
	} {
		if ro, found := classifyGitCommandLine(line); found && ro {
			t.Errorf("%q must NOT be tree-preserving (deletes/locks a directory)", line)
		}
	}
}
