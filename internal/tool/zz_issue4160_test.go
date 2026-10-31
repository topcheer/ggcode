package tool

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// #4160: a budget-exhausted Spawn returns a synthetic sa-budget-* entry.
// The isolation-worktree setup must skip it - each pass-through used to
// deterministically create and leak one worktree + branch on the terminal
// (failed) entry, unbounded across retry attempts.
func TestSetupIsolationWorktreeSkipsSyntheticIDs(t *testing.T) {
	dir := t.TempDir()
	tt := SpawnAgentTool{WorkingDir: dir}
	for _, id := range []string{"sa-budget-1", "sa-limit-2", "sa-shutdown-3"} {
		got, err := tt.setupIsolationWorktree(context.Background(), "worktree", id)
		if err != nil {
			t.Fatalf("synthetic id %s: unexpected error %v", id, err)
		}
		if got != "" {
			t.Fatalf("synthetic id %s: got worktree %q, want empty", id, got)
		}
	}
	// No worktree directory may exist for synthetic entries.
	wtRoot := filepath.Join(dir, ".ggcode", "worktrees")
	if entries, err := os.ReadDir(wtRoot); err == nil && len(entries) > 0 {
		t.Fatalf("synthetic ids created worktrees: %v", entries)
	}
}
