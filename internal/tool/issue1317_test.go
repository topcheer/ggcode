package tool

// Regression tests for GitHub issue #1317.
//
// A: rebuildDirty mutated the SHARED index (docs append/swap-truncate, df
// map ++/--) while Search/FilePathFuzzy readers held only the old pointer
// with no lock - concurrent map read/write is a fatal, unrecoverable
// process crash. The fix clones the index shell before mutating.
//
// B: removeDocFromIndex swaps the tail doc into the vacated slot but the
// local docIdx map kept the tail doc's old index. A same-batch
// "delete A + modify B" (B being the tail) then called
// replaceDocInIndex(idx, staleIdx, ...) - out-of-bounds panic in an
// unrecovered background goroutine, or wrong-slot replacement (silent
// index corruption).

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func issue1317Manager(t *testing.T, files map[string]string) *CodeIndexManager {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	m := NewCodeIndexManager(dir)
	m.mu.Lock()
	m.ready = true
	m.mu.Unlock()
	return m
}

// B: delete the head doc while modifying the tail doc in one batch. With
// docs=[a,b,c] removing a swaps c into slot 0; the stale docIdx[c]=2 made
// the subsequent replaceDocInIndex(idx, 2, newC) panic (index out of
// range after truncation to len 2) or silently corrupt.
func TestIssue1317_RemoveThenUpdateSameBatch(t *testing.T) {
	m := issue1317Manager(t, map[string]string{
		"a.go": "package a\n\nfunc alpha() {}\n",
		"b.go": "package b\n\nfunc beta() {}\n",
		"c.go": "package c\n\nfunc gamma() {}\n",
	})
	defer m.Stop()

	// Prime the index via incremental path itself: first rebuild indexes
	// the dirty set, giving us the exact in-memory state under test.
	m.mu.Lock()
	m.dirtyFiles = map[string]int64{
		filepath.Join(m.workingDir, "a.go"): 1,
		filepath.Join(m.workingDir, "b.go"): 1,
		filepath.Join(m.workingDir, "c.go"): 1,
	}
	m.mu.Unlock()
	m.rebuildDirty("test")
	if got := len(m.index.docs); got != 3 {
		t.Fatalf("prime: expected 3 docs, got %d", got)
	}

	// Delete a.go, modify c.go - batch order in the dirty map iteration
	// is random, so run the racy combination repeatedly.
	for i := 0; i < 20; i++ {
		if err := os.Remove(filepath.Join(m.workingDir, "a.go")); err != nil {
			t.Fatalf("remove a.go: %v", err)
		}
		if err := os.WriteFile(filepath.Join(m.workingDir, "c.go"), []byte("package c\n\nfunc gammaRenamed() {}\n"), 0o644); err != nil {
			t.Fatalf("rewrite c.go: %v", err)
		}
		m.mu.Lock()
		m.dirtyFiles = map[string]int64{
			filepath.Join(m.workingDir, "a.go"): 2,
			filepath.Join(m.workingDir, "c.go"): 2,
		}
		m.mu.Unlock()
		m.rebuildDirty("test")

		m.mu.RLock()
		docs := m.index.docs
		df := m.index.df
		m.mu.RUnlock()

		if len(docs) != 2 {
			t.Fatalf("iter %d: expected 2 docs after remove, got %d", i, len(docs))
		}
		paths := map[string]bool{}
		for _, d := range docs {
			paths[d.path] = true
		}
		if !paths["b.go"] || !paths["c.go"] || paths["a.go"] {
			t.Fatalf("iter %d: wrong doc set: %v", i, paths)
		}
		if df["renamed"] != 1 {
			t.Fatalf("iter %d: updated term (camelCase split) missing or wrong df: %v", i, df["renamed"])
		}
		if _, ok := df["alpha"]; ok {
			t.Fatalf("iter %d: deleted doc term still indexed", i)
		}

		// Restore for next iteration.
		if err := os.WriteFile(filepath.Join(m.workingDir, "a.go"), []byte("package a\n\nfunc alpha() {}\n"), 0o644); err != nil {
			t.Fatalf("restore a.go: %v", err)
		}
		m.mu.Lock()
		m.dirtyFiles = map[string]int64{filepath.Join(m.workingDir, "a.go"): 3}
		m.mu.Unlock()
		m.rebuildDirty("test")
	}
}

// A: hammer Search while rebuildDirty mutates. Under -race the old
// in-place mutation reports immediately; the clone keeps the published
// pointer immutable.
func TestIssue1317_ConcurrentSearchDuringRebuild(t *testing.T) {
	m := issue1317Manager(t, map[string]string{
		"a.go": "package a\n\nfunc alphaSearchTerm() {}\n",
		"b.go": "package b\n\nfunc betaSearchTerm() {}\n",
	})
	defer m.Stop()

	m.mu.Lock()
	m.dirtyFiles = map[string]int64{
		filepath.Join(m.workingDir, "a.go"): 1,
		filepath.Join(m.workingDir, "b.go"): 1,
	}
	m.mu.Unlock()
	m.rebuildDirty("test")

	var wg sync.WaitGroup
	stop := time.Now().Add(2 * time.Second)

	// Writer: flip file content and rebuild. ONE writer, matching the
	// real trigger path (single debounce background goroutine); the race
	// under test is rebuild-vs-Search, not rebuild-vs-rebuild.
	wg.Add(1)
	go func() {
		defer wg.Done()
		content := "package a\n\nfunc alphaSearchTerm() {}\n"
		for time.Now().Before(stop) {
			if content == "package a\n\nfunc alphaSearchTerm() {}\n" {
				content = "package a\n\nfunc alphaSearchTermTwo() {}\n"
			} else {
				content = "package a\n\nfunc alphaSearchTerm() {}\n"
			}
			_ = os.WriteFile(filepath.Join(m.workingDir, "a.go"), []byte(content), 0o644)
			m.mu.Lock()
			m.dirtyFiles = map[string]int64{filepath.Join(m.workingDir, "a.go"): time.Now().Unix()}
			m.mu.Unlock()
			m.rebuildDirty("test")
		}
	}()

	// Readers: search concurrently.
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for time.Now().Before(stop) {
				if _, err := m.Search("searchterm", 10); err != nil {
					t.Errorf("search: %v", err)
					return
				}
				_ = m.FilePathFuzzy("a.go", 5)
			}
		}()
	}

	wg.Wait()
}
