package agent

import (
	"os/exec"
	"strings"
	"testing"
)

// sa-223 probes: the draft-PR hint must fire exactly when the agent
// committed on an unpushed feature branch, and stay silent in every other
// end state (no commit, default branch, everything pushed, already fired).

func testRunStatsWithCommit(committed bool) *RunStats {
	rs := &RunStats{ToolCalls: map[string]int{}}
	if committed {
		rs.ToolCalls["git_commit"] = 1
	}
	return rs
}

func newDraftPRTestAgent(t *testing.T, workingDir string) *Agent {
	t.Helper()
	a := &Agent{draftPRHint: newDraftPRHintState()}
	a.workingDir = workingDir
	return a
}

// Real-git fixture: feature branch with no upstream -> ahead of main.
func TestDraftPRHintFiresOnUnpushedFeatureBranch(t *testing.T) {
	dir := t.TempDir()
	if err := exec.Command("git", "-C", dir, "init", "-b", "main").Run(); err != nil {
		t.Skip("git unavailable")
	}
	for _, args := range [][]string{
		{"-C", dir, "config", "user.email", "t@t"},
		{"-C", dir, "config", "user.name", "t"},
		{"-C", dir, "commit", "--allow-empty", "-m", "base"},
		{"-C", dir, "checkout", "-b", "feat/x"},
		{"-C", dir, "commit", "--allow-empty", "-m", "work"},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	a := newDraftPRTestAgent(t, dir)
	msg := a.checkDraftPRHintGate(testRunStatsWithCommit(true))
	if !strings.Contains(msg, "feat/x") || !strings.Contains(msg, "draft PR") {
		t.Fatalf("expected draft-PR hint for unpushed feature branch, got %q", msg)
	}
	// One shot per run.
	if again := a.checkDraftPRHintGate(testRunStatsWithCommit(true)); again != "" {
		t.Fatalf("gate must fire once per run, got %q", again)
	}
}

func TestDraftPRHintSilentWithoutCommit(t *testing.T) {
	dir := t.TempDir()
	if err := exec.Command("git", "-C", dir, "init", "-b", "main").Run(); err != nil {
		t.Skip("git unavailable")
	}
	a := newDraftPRTestAgent(t, dir)
	if msg := a.checkDraftPRHintGate(testRunStatsWithCommit(false)); msg != "" {
		t.Fatalf("no git_commit in run -> no hint, got %q", msg)
	}
}

func TestDraftPRHintSilentOnDefaultBranch(t *testing.T) {
	dir := t.TempDir()
	if err := exec.Command("git", "-C", dir, "init", "-b", "main").Run(); err != nil {
		t.Skip("git unavailable")
	}
	for _, args := range [][]string{
		{"-C", dir, "config", "user.email", "t@t"},
		{"-C", dir, "config", "user.name", "t"},
		{"-C", dir, "commit", "--allow-empty", "-m", "base"},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	a := newDraftPRTestAgent(t, dir)
	if msg := a.checkDraftPRHintGate(testRunStatsWithCommit(true)); msg != "" {
		t.Fatalf("commit on main -> no PR hint, got %q", msg)
	}
}

// Upstream set and fully pushed -> ahead == 0 -> silent.
func TestDraftPRHintSilentWhenPushed(t *testing.T) {
	dir := t.TempDir()
	clone := t.TempDir()
	if err := exec.Command("git", "-C", dir, "init", "-b", "main", "--bare").Run(); err != nil {
		t.Skip("git unavailable")
	}
	for _, args := range [][]string{
		{"-C", clone, "clone", dir, "."},
		{"-C", clone, "config", "user.email", "t@t"},
		{"-C", clone, "config", "user.name", "t"},
		{"-C", clone, "commit", "--allow-empty", "-m", "base"},
		{"-C", clone, "push", "origin", "main"},
		{"-C", clone, "checkout", "-b", "feat/y"},
		{"-C", clone, "commit", "--allow-empty", "-m", "work"},
		{"-C", clone, "push", "-u", "origin", "feat/y"},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	a := newDraftPRTestAgent(t, clone)
	if msg := a.checkDraftPRHintGate(testRunStatsWithCommit(true)); msg != "" {
		t.Fatalf("fully pushed branch -> no hint, got %q", msg)
	}
}
