package tool

// r31 (Background Agents pattern): wait_agent's annotateWorktree must, for
// COMPLETED worktree-isolated runs whose branch carries commits no remote
// has, append a draft-PR delivery hint so delegated work returns as a
// review-ready diff instead of a text report that silently rots in the
// worktree. Advisory only: any git failure maps to "no hint".

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/subagent"
)

func TestParseUnpushedCount(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"3\n", 3},
		{"0\n", 0},
		{"  7  \n", 7},
		{"", 0},
		{"abc", 0},
		{"-2", 0},
	}
	for _, c := range cases {
		if got := parseUnpushedCount(c.in); got != c.want {
			t.Errorf("parseUnpushedCount(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestUnpushedCommitCountNonRepo(t *testing.T) {
	// A directory that is not a git repo must map to 0 (advisory, no panic).
	if got := unpushedCommitCount(t.TempDir()); got != 0 {
		t.Errorf("non-repo dir must yield 0, got %d", got)
	}
}

func TestAnnotateWorktreeDeliveryHintGating(t *testing.T) {
	// No worktree: nothing appended.
	if got := annotateWorktree("res", subagent.Snapshot{Status: subagent.StatusCompleted}); strings.Contains(got, "Delivery hint") {
		t.Error("no-worktree snapshot must not carry a delivery hint")
	}
	// Non-completed run: worktree path annotated, but no delivery hint even
	// if the branch would have unpushed commits.
	running := subagent.Snapshot{Status: subagent.StatusRunning, Worktree: t.TempDir()}
	if got := annotateWorktree("res", running); strings.Contains(got, "Delivery hint") {
		t.Error("running snapshot must not carry a delivery hint")
	} else if !strings.Contains(got, "Isolated worktree:") {
		t.Error("worktree path must still be annotated")
	}
	// Completed run in a non-repo worktree: hint suppressed (0 unpushed).
	done := subagent.Snapshot{Status: subagent.StatusCompleted, Worktree: t.TempDir()}
	if got := annotateWorktree("res", done); strings.Contains(got, "Delivery hint") {
		t.Error("completed non-repo worktree must not carry a delivery hint")
	}
}

func TestAnnotateWorktreeDeliveryHintRealRepo(t *testing.T) {
	// Build a scratch repo with one commit and NO remote: every commit is
	// unpushed, so the hint must appear on a completed snapshot.
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		if err := exec.Command("git", append([]string{"-C", dir}, args...)...).Run(); err != nil {
			t.Skipf("git step %v failed: %v", args, err)
		}
	}
	run("init", "-q", "-b", "wt")
	run("config", "user.email", "t@t")
	run("config", "user.name", "t")
	run("commit", "--allow-empty", "-m", "x")
	if got := unpushedCommitCount(dir); got != 1 {
		t.Skipf("unexpected unpushed count %d (env git quirk), skipping assertion", got)
	}
	got := annotateWorktree("res", subagent.Snapshot{Status: subagent.StatusCompleted, Worktree: dir})
	if !strings.Contains(got, "Delivery hint") || !strings.Contains(got, "gh pr create --draft") {
		t.Errorf("completed unpushed worktree must carry the draft-PR delivery hint, got: %s", got)
	}
}
