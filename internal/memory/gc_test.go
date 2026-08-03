package memory

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDeleteMemory(t *testing.T) {
	tmpDir := t.TempDir()
	am := &AutoMemory{dir: tmpDir}

	am.SaveMemory("alpha", "alpha content")
	am.SaveMemory("beta", "beta content")

	// Delete existing memory
	if err := am.DeleteMemory("alpha"); err != nil {
		t.Fatalf("DeleteMemory: %v", err)
	}

	// Verify file is gone
	if _, err := os.Stat(filepath.Join(tmpDir, "alpha.md")); !os.IsNotExist(err) {
		t.Error("file should not exist after delete")
	}

	// Beta should still exist
	keys, _ := am.List()
	if len(keys) != 1 || keys[0] != "beta" {
		t.Errorf("expected only [beta], got %v", keys)
	}
}

func TestDeleteMemory_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	am := &AutoMemory{dir: tmpDir}

	err := am.DeleteMemory("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent memory")
	}
}

func TestDeleteMemory_SanitizedKey(t *testing.T) {
	tmpDir := t.TempDir()
	am := &AutoMemory{dir: tmpDir}

	am.SaveMemory("hello world", "content")
	if err := am.DeleteMemory("hello world"); err != nil {
		t.Fatalf("DeleteMemory with special chars: %v", err)
	}
}

func TestGarbageCollect_RemovesExpired(t *testing.T) {
	tmpDir := t.TempDir()
	am := &AutoMemory{dir: tmpDir}

	// Create a transient memory file (matches impl-task- pattern)
	path := filepath.Join(tmpDir, "impl-task-old.md")
	oldTime := time.Now().Add(-31 * 24 * time.Hour)
	if err := os.WriteFile(path, []byte("old task"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	// Create a persistent memory that should survive
	am.SaveMemory("build-process-impl", "persists")

	stats := am.GarbageCollect()
	if stats.ExpiredRemoved != 1 {
		t.Errorf("expected 1 expired removed, got %d", stats.ExpiredRemoved)
	}

	// Verify expired file is gone
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("expired file should have been removed")
	}

	// Verify persistent file survives
	if _, err := os.Stat(filepath.Join(tmpDir, "build-process-impl.md")); err != nil {
		t.Error("persistent file should survive GC")
	}
}

func TestGarbageCollect_RemovesDeduped(t *testing.T) {
	tmpDir := t.TempDir()
	am := &AutoMemory{dir: tmpDir}

	// Create two evolving entries with same dedup key
	older := filepath.Join(tmpDir, "competitor-analysis-2026-07-01-r1.md")
	newer := filepath.Join(tmpDir, "competitor-analysis-2026-07-13-r3.md")

	os.WriteFile(older, []byte("old"), 0644)
	os.WriteFile(newer, []byte("new"), 0644)

	oldTime := time.Now().Add(-48 * time.Hour)
	os.Chtimes(older, oldTime, oldTime)

	stats := am.GarbageCollect()
	if stats.DedupRemoved != 1 {
		t.Errorf("expected 1 deduped removed, got %d", stats.DedupRemoved)
	}

	// Older should be removed, newer should survive
	if _, err := os.Stat(older); !os.IsNotExist(err) {
		t.Error("older evolving file should have been removed")
	}
	if _, err := os.Stat(newer); err != nil {
		t.Error("newer evolving file should survive")
	}
}

func TestGarbageCollect_KeepsPersistent(t *testing.T) {
	tmpDir := t.TempDir()
	am := &AutoMemory{dir: tmpDir}

	am.SaveMemory("build-process-impl", "build info")
	am.SaveMemory("api-design", "api docs")

	stats := am.GarbageCollect()
	if stats.Total < 2 {
		t.Errorf("expected at least 2 total, got %d", stats.Total)
	}
	if stats.ExpiredRemoved+stats.DedupRemoved != 0 {
		t.Errorf("expected 0 removed, got expired=%d deduped=%d", stats.ExpiredRemoved, stats.DedupRemoved)
	}

	// All files should still exist
	keys, _ := am.List()
	if len(keys) != 2 {
		t.Errorf("expected 2 files remaining, got %d", len(keys))
	}
}

func TestGCStats_String(t *testing.T) {
	s := GCStats{Total: 5, ExpiredRemoved: 0, DedupRemoved: 0}
	if got := s.String(); got != "memory GC: 5 files scanned, 0 removed" {
		t.Errorf("unexpected String(): %q", got)
	}

	s2 := GCStats{Total: 10, ExpiredRemoved: 2, DedupRemoved: 1}
	got := s2.String()
	if got != "memory GC: 10 files scanned, 3 removed (2 expired, 1 deduped)" {
		t.Errorf("unexpected String(): %q", got)
	}
}
