package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIssue4905_RemoveDeletesCreationBranchNotCurrent(t *testing.T) {
	mainRepo := initRepo542(t)

	wtDir := filepath.Join(mainRepo, ".ggcode", "worktrees", "agent-x")
	if err := os.MkdirAll(filepath.Dir(wtDir), 0755); err != nil {
		t.Fatal(err)
	}
	runGit542(t, mainRepo, "worktree", "add", "-b", "agent-x", wtDir)

	runGit542(t, wtDir, "switch", "-c", "switched-mid")

	tool := ExitWorktree{WorkingDir: wtDir}

	input, _ := json.Marshal(map[string]any{
		"action":          "remove",
		"discard_changes": true,
		"description":     "probe",
	})
	res, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected failure: %s", res.Content)
	}

	if out := runGit542(t, mainRepo, "branch", "--list", "agent-x"); out != "" {
		t.Fatalf("creation-time branch agent-x was not deleted; branches: %s", out)
	}

	if out := runGit542(t, mainRepo, "branch", "--list", "switched-mid"); out == "" {
		t.Fatal("branch switched-mid (checked out at exit time) was deleted - wrong target")
	}
	if !strings.Contains(res.Content, "switched-mid") {
		t.Fatalf("result must surface the kept switched branch, got: %s", res.Content)
	}
	if !strings.Contains(res.Content, "reflog") {
		t.Fatalf("deletion message must carry the reflog recovery hint, got: %s", res.Content)
	}
}

func TestIssue4905_RemoveDetachedHeadStillCleansBranch(t *testing.T) {
	mainRepo := initRepo542(t)

	wtDir := filepath.Join(mainRepo, ".ggcode", "worktrees", "agent-y")
	if err := os.MkdirAll(filepath.Dir(wtDir), 0755); err != nil {
		t.Fatal(err)
	}
	runGit542(t, mainRepo, "worktree", "add", "-b", "agent-y", wtDir)
	runGit542(t, wtDir, "switch", "--detach")

	tool := ExitWorktree{WorkingDir: wtDir}

	input, _ := json.Marshal(map[string]any{
		"action":          "remove",
		"discard_changes": true,
		"description":     "probe",
	})
	res, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected failure: %s", res.Content)
	}
	if out := runGit542(t, mainRepo, "branch", "--list", "agent-y"); out != "" {
		t.Fatalf("creation-time branch agent-y was orphaned on detached HEAD; branches: %s", out)
	}
	if !strings.Contains(res.Content, "deleted branch agent-y") {
		t.Fatalf("message must report the deleted creation branch, got: %s", res.Content)
	}
}
