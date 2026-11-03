package tool

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

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

	wtRoot := filepath.Join(dir, ".ggcode", "worktrees")
	if entries, err := os.ReadDir(wtRoot); err == nil && len(entries) > 0 {
		t.Fatalf("synthetic ids created worktrees: %v", entries)
	}
}
