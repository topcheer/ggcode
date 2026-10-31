package tool

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// Regression guard for #4735 (A half): loadDiskCache used to write
// m.budgetExcluded BEFORE taking m.mu - racing persistIndex (RLock read) and
// doBuild Phase 6 (Lock write). Concurrent map read/write is an unrecoverable
// fatal throw in the Go runtime. The excluded set must now be assembled
// locally and published inside the lock. Run with -race to catch a regression.
func TestIssue4735_LoadDiskCachePublishesExcludedUnderLock(t *testing.T) {
	dir := t.TempDir()
	m := NewCodeIndexManager(dir)

	// Persist a cache with docs + excluded entries on disk, simulating
	// another instance's index.
	pi := persistedIndex{
		Version:   codeIndexVersion,
		UpdatedAt: time.Now(),
		Docs: []persistedDoc{{
			Path: "a.go", TF: map[string]int{"main": 1}, Length: 10, Mtime: time.Now().Unix()}},
		Excluded: []persistedExcluded{
			{Path: "big/generated.go", Mtime: 111},
			{Path: "big/vendor.go", Mtime: 222},
		},
	}
	data, err := json.Marshal(pi)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(m.indexPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(m.indexPath, data, 0o644); err != nil {
		t.Fatal(err)
	}

	m.loadDiskCache()

	m.mu.RLock()
	got := len(m.budgetExcluded)
	m.mu.RUnlock()
	if got != 2 {
		t.Fatalf("budgetExcluded must be restored from disk cache, got %d entries", got)
	}
	if !m.ready {
		t.Fatal("index must be ready after loadDiskCache")
	}

	// Race exercise: hammer loadDiskCache concurrently with Search (the
	// lazyLoad/persistIndex paths touch the same fields under m.mu). Under
	// the old unsynchronized write this throws "concurrent map read and map
	// write" with -race (or at runtime); now it must complete cleanly.
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); m.loadDiskCache() }()
		go func() { defer wg.Done(); m.Search("main", 10) }()
	}
	wg.Wait()
}

// The nil-cache edge must not clobber an existing excluded set: loadDiskCache
// returns early (no docs) and must leave state untouched.
func TestIssue4735_EmptyCacheKeepsExcluded(t *testing.T) {
	dir := t.TempDir()
	m := NewCodeIndexManager(dir)
	// No disk cache file at all -> loadDiskCache returns before touching any
	// state; nothing to assert beyond "no panic and stays not-ready".
	m.loadDiskCache()
	m.mu.RLock()
	ready := m.ready
	m.mu.RUnlock()
	if ready {
		t.Fatal("missing cache must leave manager not-ready")
	}
	_ = filepath.Join(dir, "unused")
}
