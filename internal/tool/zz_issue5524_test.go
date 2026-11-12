package tool

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// #5524: the files-changed advisory must route paths through the worktree
// namespace before summarizing. Paths under .ggcode/worktrees/<name>/ are
// isolated by design; reporting them as main-tree changes misdirects
// fixes/stashes. Regression pin: dirty worktree + clean main tree -> the
// advisory must report the main tree as clean.
func TestChangedFilesAdvisoryWorktreeRouting(t *testing.T) {
	base := t.TempDir()
	mainFile := filepath.Join(base, "internal", "agent", "agent.go")
	wtDir := filepath.Join(base, ".ggcode", "worktrees", "demo", "internal", "tool")
	wtFile := filepath.Join(wtDir, "builtin.go")
	for _, d := range []string{filepath.Dir(mainFile), wtDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{mainFile, wtFile} {
		if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	t0 := time.Now().Add(-time.Hour)
	snapshot := map[string]time.Time{mainFile: t0, wtFile: t0}
	t1 := time.Now()
	for _, f := range []string{mainFile, wtFile} {
		if err := os.Chtimes(f, t1, t1); err != nil {
			t.Fatal(err)
		}
	}

	// Both changed: main path reported, worktree path summarized per-worktree.
	out := detectChangedFilesFromCommand(snapshot)
	if !strings.Contains(out, mainFile) {
		t.Fatalf("main-tree change must be reported:\n%s", out)
	}
	if strings.Contains(out, wtFile) {
		t.Fatalf("worktree path must not be listed as a main-tree change:\n%s", out)
	}
	if !strings.Contains(out, "isolated worktree") || !strings.Contains(out, "demo") {
		t.Fatalf("worktree change must be summarized per-worktree:\n%s", out)
	}

	// Worktree-only change: the main tree must be reported clean.
	out2 := detectChangedFilesFromCommand(map[string]time.Time{wtFile: t0})
	if strings.Contains(out2, "modified on disk") && !strings.Contains(out2, "main tree clean") {
		t.Fatalf("worktree-only changes must not read as main-tree changes:\n%s", out2)
	}
	if !strings.Contains(out2, "main tree clean") {
		t.Fatalf("worktree-only case must state the main tree is clean:\n%s", out2)
	}
}
