package subagent

import (
	"os"
	"testing"
	"time"
)

// #4362 regression: successful Load must refresh the store dir's mtime (at
// least once per process). SweepStaleWorkspaceDirs treats dir mtime as the
// liveness signal; with no touch on read, a template created once and then
// only READ for >90d was silently RemoveAll'd by the next startup sweep
// from any other workspace sharing ~/.ggcode/subagents.
func TestIssue4362LoadRefreshesDirMtime(t *testing.T) {
	dir := t.TempDir()
	store := &TemplateStore{dir: dir}

	tmpl := NamedAgentTemplate{Name: "reviewer", Description: "d", SystemPrompt: "p"}
	if err := store.Save(tmpl); err != nil {
		t.Fatal(err)
	}

	// Backdate the dir to simulate a >90d idle-but-in-use store.
	old := time.Now().Add(-120 * 24 * time.Hour)
	if err := os.Chtimes(dir, old, old); err != nil {
		t.Fatal(err)
	}

	if _, err := store.Load("reviewer"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if info.ModTime().Before(time.Now().Add(-time.Hour)) {
		t.Fatal("#4362 regression: Load did not refresh the store dir mtime - read-only templates remain sweep-vulnerable")
	}

	// Once-per-process guard: a second Load still succeeds (the touch
	// simply skips).
	if _, err := store.Load("reviewer"); err != nil {
		t.Fatalf("second Load after touch failed: %v", err)
	}
}
