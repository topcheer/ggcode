package util

// sa-242 runtime audit: AtomicWriteFile cleans its temp on every error
// return, but a process killed between CreateTemp and Rename (SIGKILL,
// OOM, power loss) leaves ".ggcode-tmp-*" behind forever. SweepStaleTempFiles
// is the startup backstop; these tests pin its age gate and tolerance.

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSweepStaleTempFiles_RemovesOnlyStale(t *testing.T) {
	dir := t.TempDir()
	stale := filepath.Join(dir, ".ggcode-tmp-stale")
	fresh := filepath.Join(dir, ".ggcode-tmp-fresh")
	keep := filepath.Join(dir, "unrelated.json")
	for _, p := range []string{stale, fresh, keep} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Age the stale one past the sweep cutoff.
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}

	removed, err := SweepStaleTempFiles(dir, time.Hour)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale temp must be removed, stat err=%v", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatalf("fresh temp must survive: %v", err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("non-temp file must survive: %v", err)
	}
}

func TestSweepStaleTempFiles_MissingDir(t *testing.T) {
	removed, err := SweepStaleTempFiles(filepath.Join(t.TempDir(), "nope"), time.Hour)
	if err != nil || removed != 0 {
		t.Fatalf("missing dir must be a no-op, got removed=%d err=%v", removed, err)
	}
}

func TestSweepStaleTempFiles_IgnoresDirs(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, ".ggcode-tmp-dirlike")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(sub, old, old); err != nil {
		t.Fatal(err)
	}
	removed, err := SweepStaleTempFiles(dir, time.Hour)
	if err != nil || removed != 0 {
		t.Fatalf("directories must be skipped, got removed=%d err=%v", removed, err)
	}
	if _, err := os.Stat(sub); err != nil {
		t.Fatalf("dir must survive: %v", err)
	}
}
