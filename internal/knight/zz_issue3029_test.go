package knight

// Regression probe for #3029: Append was read-modify-write under a
// process-internal mutex only. Two processes on the same workspace
// (daemon/TUI/A2A) reading N entries and each writing N+1 dropped the first
// writer's entry - atomic rename prevents tearing, not loss. The fix guards
// the whole read-modify-write with util.FileLock. Two store instances on
// the same path (distinct in-process mutexes, shared flock file) simulate
// the cross-process case.

import (
	"path/filepath"
	"sync"
	"testing"
)

func TestIssue3029_ConcurrentAppendsAcrossStoreInstancesLoseNothing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "knight-memory.jsonl")

	// Two instances = two semanticMemoryPathMu entries: only the file lock
	// can serialize their read-modify-write cycles.
	a := &semanticMemoryStore{path: path}
	b := &semanticMemoryStore{path: path}

	const perWriter = 25
	var wg sync.WaitGroup
	for _, s := range []*semanticMemoryStore{a, b} {
		wg.Add(1)
		go func(s *semanticMemoryStore) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				if err := s.Append(SemanticMemoryEntry{Kind: "lesson", Summary: "concurrent lesson"}); err != nil {
					t.Errorf("append: %v", err)
					return
				}
			}
		}(s)
	}
	wg.Wait()

	entries, err := a.Recent(1000)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2*perWriter {
		t.Fatalf("#3029: cross-instance concurrent appends lost entries: got %d, want %d", len(entries), 2*perWriter)
	}
}

func TestIssue3029_CapStillEnforced(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "knight-memory.jsonl")
	s := &semanticMemoryStore{path: path}
	for i := 0; i < maxSemanticMemoryEntries+50; i++ {
		if err := s.Append(SemanticMemoryEntry{Kind: "lesson", Summary: "filler"}); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := s.Recent(maxSemanticMemoryEntries + 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) > maxSemanticMemoryEntries {
		t.Fatalf("cap must hold at %d, got %d", maxSemanticMemoryEntries, len(entries))
	}
}
