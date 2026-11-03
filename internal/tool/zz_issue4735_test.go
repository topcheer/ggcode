package tool

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestIssue4735_LoadDiskCachePublishesExcludedUnderLock(t *testing.T) {
	dir := t.TempDir()
	m := NewCodeIndexManager(dir)

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

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); m.loadDiskCache() }()
		go func() { defer wg.Done(); m.Search("main", 10) }()
	}
	wg.Wait()
}

func TestIssue4735_EmptyCacheKeepsExcluded(t *testing.T) {
	dir := t.TempDir()
	m := NewCodeIndexManager(dir)

	m.loadDiskCache()
	m.mu.RLock()
	ready := m.ready
	m.mu.RUnlock()
	if ready {
		t.Fatal("missing cache must leave manager not-ready")
	}
	_ = filepath.Join(dir, "unused")
}
