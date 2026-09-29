package tool

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/subagent"
)

// initProofRepo creates a minimal git repo with one commit so the proof
// collector has a real worktree to inspect. Uses env overrides so the test
// never depends on global git identity config.
func initProofRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "a.txt")
	git("commit", "-q", "-m", "initial commit")
	return dir
}

func TestBuildWorktreeProofCommitted(t *testing.T) {
	wt := initProofRepo(t)
	proof := buildWorktreeProof(context.Background(), subagent.Snapshot{Worktree: wt, Status: subagent.StatusCompleted})

	if !strings.Contains(proof, "Proof-of-work") {
		t.Fatalf("missing proof header:\n%s", proof)
	}
	if !strings.Contains(proof, "- branch: ") {
		t.Fatalf("missing branch line:\n%s", proof)
	}
	if !strings.Contains(proof, "(last commit: initial commit)") {
		t.Fatalf("missing last commit subject:\n%s", proof)
	}
	if !strings.Contains(proof, "working tree: clean") {
		t.Fatalf("expected clean working tree:\n%s", proof)
	}
	if !strings.Contains(proof, "Adopt explicitly after review") {
		t.Fatalf("missing adoption gate hint:\n%s", proof)
	}
}

func TestBuildWorktreeProofUncommitted(t *testing.T) {
	wt := initProofRepo(t)
	if err := os.WriteFile(filepath.Join(wt, "a.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, "b_new.txt"), []byte("untracked\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	proof := buildWorktreeProof(context.Background(), subagent.Snapshot{Worktree: wt, Status: subagent.StatusCompleted})

	if !strings.Contains(proof, "uncommitted change(s)") {
		t.Fatalf("missing uncommitted count:\n%s", proof)
	}
	if !strings.Contains(proof, "b_new.txt") {
		t.Fatalf("missing untracked file in status:\n%s", proof)
	}
	if !strings.Contains(proof, "uncommitted diff:") {
		t.Fatalf("missing diffstat summary:\n%s", proof)
	}
}

func TestBuildWorktreeProofUnavailable(t *testing.T) {
	proof := buildWorktreeProof(context.Background(), subagent.Snapshot{
		Worktree: filepath.Join(t.TempDir(), "does-not-exist"),
		Status:   subagent.StatusFailed,
	})
	if !strings.Contains(proof, "proof unavailable") {
		t.Fatalf("expected graceful degradation, got:\n%s", proof)
	}
}

func TestAnnotateWorktreeProofGating(t *testing.T) {
	wt := initProofRepo(t)

	running := subagent.Snapshot{Worktree: wt, Status: subagent.StatusRunning}
	gotRunning := annotateWorktree(context.Background(), "body", running)
	if strings.Contains(gotRunning, "Proof-of-work") {
		t.Fatalf("proof must not run for in-progress snapshots:\n%s", gotRunning)
	}
	if !strings.Contains(gotRunning, "Isolated worktree:") {
		t.Fatalf("worktree path annotation missing:\n%s", gotRunning)
	}

	done := subagent.Snapshot{Worktree: wt, Status: subagent.StatusCompleted}
	if got := annotateWorktree(context.Background(), "body", done); !strings.Contains(got, "Proof-of-work") {
		t.Fatalf("proof missing for terminal snapshot:\n%s", got)
	}

	noWt := subagent.Snapshot{Status: subagent.StatusCompleted}
	if got := annotateWorktree(context.Background(), "body", noWt); got != "body" {
		t.Fatalf("shared-checkout snapshot must be untouched, got:\n%s", got)
	}
}

func TestTerminalSnapshot(t *testing.T) {
	terminal := map[subagent.Status]bool{
		subagent.StatusCompleted: true,
		subagent.StatusFailed:    true,
		subagent.StatusCancelled: true,
		subagent.StatusRunning:   false,
		subagent.StatusPending:   false,
	}
	for st, want := range terminal {
		if got := terminalSnapshot(st); got != want {
			t.Errorf("terminalSnapshot(%s) = %v, want %v", st, got, want)
		}
	}
}

func TestTruncateProofLine(t *testing.T) {
	if got := truncateProofLine("short", 10); got != "short" {
		t.Errorf("short line modified: %q", got)
	}
	long := strings.Repeat("x", 120)
	got := truncateProofLine(long, 80)
	if !strings.HasSuffix(got, "...") || len(got) != 80+3 {
		t.Errorf("unexpected truncation: len=%d suffix=%v", len(got), strings.HasSuffix(got, "..."))
	}
}
