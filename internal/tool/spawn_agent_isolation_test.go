package tool

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/subagent"
)

func TestSpawnAgentIsolationParamValidation(t *testing.T) {
	mgr := subagent.NewManager(config.SubAgentConfig{MaxConcurrent: 2, Timeout: 5 * time.Second})
	defer mgr.Shutdown()

	s := SpawnAgentTool{Manager: mgr}
	input, _ := json.Marshal(map[string]string{
		"task":        "test task",
		"description": "d",
		"isolation":   "container",
	})
	result, err := s.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("expected error for invalid isolation value")
	}
	if !strings.Contains(result.Content, "invalid isolation") {
		t.Errorf("unexpected error content: %s", result.Content)
	}
}

func TestSpawnAgentIsolationNoneAccepted(t *testing.T) {
	mgr := subagent.NewManager(config.SubAgentConfig{MaxConcurrent: 2, Timeout: 5 * time.Second})
	defer mgr.Shutdown()

	s := SpawnAgentTool{Manager: mgr}
	input, _ := json.Marshal(map[string]string{
		"task":        "test task",
		"description": "d",
		"isolation":   "none",
	})
	result, err := s.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	if strings.Contains(result.Content, "worktree") {
		t.Errorf("isolation=none must not mention a worktree: %s", result.Content)
	}
	// Give the goroutine a moment to start before Shutdown.
	time.Sleep(100 * time.Millisecond)
}

func TestCreateAgentWorktree(t *testing.T) {
	repo := initTestGitRepo(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	wtPath, branch, err := createAgentWorktree(ctx, repo, "sa-1")
	if err != nil {
		t.Fatalf("createAgentWorktree: %v", err)
	}

	// Worktree directory must exist under <repo>/.ggcode/worktrees/.
	// Compare resolved paths: on macOS t.TempDir() may report /var/... while
	// findGitRoot returns the resolved /private/var/... form.
	worktreesDir := filepath.Join(repo, ".ggcode", "worktrees")
	resolvedWt, err := filepath.EvalSymlinks(wtPath)
	if err != nil {
		t.Fatalf("worktree dir missing: %v", err)
	}
	resolvedDir, err := filepath.EvalSymlinks(worktreesDir)
	if err != nil {
		t.Fatalf("worktrees dir missing: %v", err)
	}
	if filepath.Dir(resolvedWt) != resolvedDir {
		t.Errorf("worktree %q not under %q", resolvedWt, resolvedDir)
	}

	// Branch must exist.
	if out, err := exec.Command("git", "-C", repo, "rev-parse", "--verify", branch).CombinedOutput(); err != nil {
		t.Errorf("branch %q not found: %v: %s", branch, err, string(out))
	}

	// Worktree must be registered in git.
	out, err := exec.Command("git", "-C", repo, "worktree", "list", "--porcelain").CombinedOutput()
	if err != nil {
		t.Fatalf("git worktree list: %v: %s", err, string(out))
	}
	if !strings.Contains(string(out), wtPath) {
		t.Errorf("worktree not registered:\n%s", string(out))
	}

	// A second creation must get a distinct name (IDs restart per process,
	// so name collisions across sessions would fail git worktree add).
	wtPath2, _, err := createAgentWorktree(ctx, repo, "sa-1")
	if err != nil {
		t.Fatalf("second createAgentWorktree: %v", err)
	}
	if wtPath2 == wtPath {
		t.Fatal("expected distinct worktree names for repeated spawns")
	}
}

func TestCreateAgentWorktreeNonGitDir(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, _, err := createAgentWorktree(ctx, dir, "sa-1"); err == nil {
		t.Fatal("expected error for non-git working dir")
	}
}

func TestSpawnAgentWorktreeIsolationEndToEnd(t *testing.T) {
	repo := initTestGitRepo(t)
	mgr := subagent.NewManager(config.SubAgentConfig{MaxConcurrent: 2, Timeout: 5 * time.Second})
	defer mgr.Shutdown()

	s := SpawnAgentTool{Manager: mgr, WorkingDir: repo, Provider: nil}
	input, _ := json.Marshal(map[string]string{
		"task":        "test task",
		"description": "d",
		"isolation":   "worktree",
	})
	result, err := s.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "Isolated in git worktree") {
		t.Fatalf("spawn result missing worktree info: %s", result.Content)
	}

	// Extract the worktree path from the result and verify it exists.
	idx := strings.Index(result.Content, "Isolated in git worktree: ")
	rest := result.Content[idx+len("Isolated in git worktree: "):]
	end := strings.IndexByte(rest, ' ')
	if end < 0 {
		t.Fatalf("cannot parse worktree path from: %s", result.Content)
	}
	wtPath := rest[:end]
	if _, err := os.Stat(wtPath); err != nil {
		t.Errorf("worktree dir missing: %v", err)
	}

	// Snapshot must surface the worktree path for wait_agent.
	id := strings.TrimSpace(strings.TrimPrefix(strings.SplitN(result.Content, "\n", 2)[0], "Sub-agent spawned with ID: "))
	snap, ok := mgr.SnapshotByID(id)
	if !ok {
		t.Fatalf("snapshot not found for %s", id)
	}
	if snap.Worktree != wtPath {
		t.Errorf("snapshot worktree = %q, want %q", snap.Worktree, wtPath)
	}

	// Give the goroutine a moment before Shutdown.
	time.Sleep(100 * time.Millisecond)
}

func TestAnnotateWorktree(t *testing.T) {
	if got := annotateWorktree("result", subagent.Snapshot{}); got != "result" {
		t.Errorf("annotateWorktree without worktree = %q", got)
	}
	snap := subagent.Snapshot{Worktree: filepath.Join("repo", ".ggcode", "worktrees", "agent-sa-1")}
	got := annotateWorktree("result", snap)
	if !strings.Contains(got, "Isolated worktree:") || !strings.Contains(got, "agent-sa-1") {
		t.Errorf("annotateWorktree = %q, want worktree annotation", got)
	}
}
