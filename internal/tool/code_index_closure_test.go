package tool

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newTestIndexManager builds a manager whose cache lives in the test's temp
// dir (never the user's real cache directory).
func newTestIndexManager(t *testing.T, dir string) *CodeIndexManager {
	t.Helper()
	m := NewCodeIndexManager(dir)
	m.indexPath = filepath.Join(t.TempDir(), "index.json")
	return m
}

func buildSync(t *testing.T, m *CodeIndexManager) {
	t.Helper()
	if !m.tryLock() {
		t.Fatal("could not acquire index lock")
	}
	defer m.unlock()
	m.doBuild(context.Background())
}

// The periodic scan must compare against the in-memory index, not the disk
// cache. When the cache is missing or was discarded (too large), the old code
// marked every file dirty on every 5-minute tick and re-tokenized the whole
// tree forever.
func TestCodeIndexScanUsesInMemoryTruthWithoutDiskCache(t *testing.T) {
	root := t.TempDir()
	for _, f := range []string{"a.go", "b.go", "c.go"} {
		if err := os.WriteFile(filepath.Join(root, f), []byte("package alpha\n\nfunc Login() {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	m := newTestIndexManager(t, root)
	buildSync(t, m)
	if !m.IsReady() || m.Stats().IndexedFiles != 3 {
		t.Fatalf("expected 3 indexed docs, got ready=%v stats=%+v", m.IsReady(), m.Stats())
	}

	// Cache gone (discarded for size, cleaned, or never persisted).
	if err := os.Remove(m.indexPath); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	m.scanForExternalChanges()
	m.mu.RLock()
	dirty := len(m.dirtyFiles)
	m.mu.RUnlock()
	if dirty != 0 {
		t.Fatalf("unchanged tree must produce no dirty files without a disk cache, got %d", dirty)
	}

	// An external edit is still detected (mtime changes).
	time.Sleep(1100 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(root, "b.go"), []byte("package alpha\n\nfunc Logout() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m.scanForExternalChanges()
	m.mu.RLock()
	_, bDirty := m.dirtyFiles["b.go"]
	dirty = len(m.dirtyFiles)
	m.mu.RUnlock()
	if dirty != 1 || !bDirty {
		t.Fatalf("expected only b.go dirty after external edit, got %d entries, b.go=%v", dirty, bDirty)
	}
}

// A tree with nothing to index is a terminal state, not an endless "being
// built, try again" that re-walks the tree on every call.
func TestCodeIndexEmptyTreeIsTerminal(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "notes.pdf"), []byte("not code"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := newTestIndexManager(t, root)
	buildSync(t, m)

	if !m.IsReady() {
		t.Fatal("empty tree must report ready (terminal state)")
	}
	if _, err := m.Search("anything", 5); !errors.Is(err, errIndexEmpty) {
		t.Fatalf("expected errIndexEmpty, got %v", err)
	}
	if m.Stats().IndexedFiles != 0 {
		t.Fatalf("expected 0 indexed files, got %d", m.Stats().IndexedFiles)
	}

	// A file appearing later leaves the empty state through the normal
	// scan + incremental rebuild path.
	if err := os.WriteFile(filepath.Join(root, "new.go"), []byte("package late\n\nfunc Zebracorn() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m.scanForExternalChanges()
	m.rebuildDirty("test")
	if _, err := m.Search("zebracorn", 5); err != nil || m.Stats().IndexedFiles != 1 {
		t.Fatalf("expected 1 doc after late file appeared, err=%v stats=%+v", err, m.Stats())
	}
}

// A file that grows past the size cap after being indexed must drop out of
// the index (doBuild would skip it) instead of serving stale terms forever.
func TestCodeIndexOversizeFileDroppedIncrementally(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "big.go")
	if err := os.WriteFile(target, []byte("package big\n\nfunc Hummingbird() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "other.go"), []byte("package other\n\nfunc Pangolin() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := newTestIndexManager(t, root)
	buildSync(t, m)
	if m.Stats().IndexedFiles != 2 {
		t.Fatalf("expected 2 docs, got %+v", m.Stats())
	}

	big := "package big\nfunc Hummingbird() {}\n" + strings.Repeat("// filler commentary line\n", (codeIndexMaxFileSize/len("// filler commentary line\n"))+10)
	if err := os.WriteFile(target, []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}

	m.scanForExternalChanges()
	m.mu.RLock()
	_, marked := m.dirtyFiles["big.go"]
	m.mu.RUnlock()
	if !marked {
		t.Fatal("oversize transition must mark the file dirty")
	}
	m.rebuildDirty("test")

	res, err := m.Search("hummingbird", 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range res {
		if r.path == "big.go" {
			t.Fatal("oversize file must have been dropped from the index")
		}
	}
	if m.Stats().IndexedFiles != 1 {
		t.Fatalf("expected 1 doc after dropping oversize file, got %+v", m.Stats())
	}
	// And the on-disk cache agrees, so a restart does not resurrect it.
	m2 := newTestIndexManager(t, root)
	m2.indexPath = m.indexPath
	m2.loadDiskCache()
	if res, _ := m2.Search("hummingbird", 10); len(res) != 0 {
		t.Fatalf("oversize file resurrected from disk cache: %v", res)
	}
}

// WaitReady lets a warm-cache cold start answer the first search instead of
// returning "being built" and burning a model round-trip.
func TestCodeIndexWaitReadyWarmStart(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\nfunc Alpha() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := newTestIndexManager(t, root)
	buildSync(t, m) // persist the cache

	// Fresh manager over the same tree = warm cache, nothing in memory.
	m2 := newTestIndexManager(t, root)
	m2.indexPath = m.indexPath
	m2.StartBackgroundIndex()
	if !m2.WaitReady(5 * time.Second) {
		t.Fatal("WaitReady must return true once the warm build publishes")
	}
	res, err := m2.Search("alpha", 5)
	if err != nil || len(res) != 1 {
		t.Fatalf("expected 1 result after WaitReady, err=%v res=%v", err, res)
	}
	m2.Stop()
}
