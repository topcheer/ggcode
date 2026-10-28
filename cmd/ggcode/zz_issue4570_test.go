package main

// #4570: repl.SetSystemPromptBuilder's sub-agent prompt still closed over the
// STARTUP git status snapshot (root.go L607) after #4502 moved the other two
// builders to lazy per-build detection - repl-created sub-agents carried a
// stale branch name / dirty count after any mid-session commit, checkout, or
// worktree. The fix: the builder closure calls detectGitStatus directly
// (identical to the skillTool builder) and the startup snapshot variable is
// gone entirely. Pin: no builder may return a captured gitStatus variable.

import (
	"os"
	"strings"
	"testing"
)

func TestIssue4570_ReplBuilderDetectsGitLazily(t *testing.T) {
	src, err := os.ReadFile("root.go")
	if err != nil {
		t.Skip("source not readable in this environment")
	}
	s := string(src)

	// The stale-snapshot variable must not exist at all: any
	// "return gitStatus" closure would be the #4570 regression.
	if strings.Contains(s, "func() string { return gitStatus }") {
		t.Fatal("#4570: a builder still returns the startup gitStatus snapshot - repl sub-agents would carry stale branch/dirty state")
	}
	// The repl builder must call detectGitStatus live, same as #4502's sites.
	n := strings.Count(s, "func() string { return detectGitStatus(")
	if n < 2 {
		t.Fatalf("#4570: expected lazy detectGitStatus in both the skillTool and repl builders, found %d", n)
	}
	// And the startup snapshot declaration is gone (it had no other consumer).
	if strings.Contains(s, "gitStatus := detectGitStatus(") {
		t.Fatal("#4570: unused startup snapshot declaration remains")
	}
}
