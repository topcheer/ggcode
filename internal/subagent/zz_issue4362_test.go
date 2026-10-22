package subagent

// #4362: SweepStaleWorkspaceDirs treats the workspace dir mtime as the sole
// liveness signal, but Load/List never refreshed it. A named template that is
// read regularly but not rewritten for 90d gets RemoveAll'd by a sweep started
// from any OTHER workspace. The fix refreshes dir mtime on the first
// successful Load hit per store instance.

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestIssue4362LoadRefreshesDirMtime(t *testing.T) {
	withTestHome(t)
	ws := t.TempDir()
	store := NewTemplateStore(ws)
	if err := store.Save(NamedAgentTemplate{Name: "reviewer", SystemPrompt: "p"}); err != nil {
		t.Fatal(err)
	}

	old := time.Now().Add(-91 * 24 * time.Hour)
	if err := os.Chtimes(store.dir, old, old); err != nil {
		t.Fatal(err)
	}

	// A missing template must NOT count as liveness.
	if _, err := store.Load("nonexistent"); err == nil {
		t.Fatal("expected error for missing template")
	}
	if st, err := os.Stat(store.dir); err != nil {
		t.Fatal(err)
	} else if !st.ModTime().Before(time.Now().Add(-24 * time.Hour)) {
		t.Fatalf("dir mtime refreshed by failed Load: %v", st.ModTime())
	}

	if _, err := store.Load("reviewer"); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(store.dir); err != nil {
		t.Fatal(err)
	} else if st.ModTime().Before(time.Now().Add(-time.Hour)) {
		t.Fatalf("dir mtime not refreshed by successful Load: %v", st.ModTime())
	}
}

func TestIssue4362SweepSparesReadActiveWorkspace(t *testing.T) {
	withTestHome(t)
	wsA, wsB := t.TempDir(), t.TempDir()
	storeA := NewTemplateStore(wsA)
	if err := storeA.Save(NamedAgentTemplate{Name: "reviewer", SystemPrompt: "p"}); err != nil {
		t.Fatal(err)
	}

	old := time.Now().Add(-91 * 24 * time.Hour)
	if err := os.Chtimes(storeA.dir, old, old); err != nil {
		t.Fatal(err)
	}

	// Read activity in workspace A keeps its templates alive...
	if _, err := storeA.Load("reviewer"); err != nil {
		t.Fatal(err)
	}
	// ...so a sweep started from workspace B must not delete A's dir.
	if removed := SweepStaleWorkspaceDirs(wsB, 90*24*time.Hour); removed != 0 {
		t.Fatalf("removed = %d, want 0 (read-active workspace must survive)", removed)
	}
	if _, err := os.Stat(filepath.Join(storeA.dir, "reviewer.json")); err != nil {
		t.Fatalf("read-active workspace template deleted: %v", err)
	}

	// Control: an aged dir with NO read activity is still swept (#3341 intent).
	dead := filepath.Join(SubagentsRoot(), "dddd4444dddd4444")
	if err := os.MkdirAll(dead, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dead, "tpl.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(dead, old, old); err != nil {
		t.Fatal(err)
	}
	if removed := SweepStaleWorkspaceDirs(wsB, 90*24*time.Hour); removed != 1 {
		t.Fatalf("removed = %d, want 1 (dead dir must still be swept)", removed)
	}
}
