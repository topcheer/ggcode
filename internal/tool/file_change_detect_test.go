package tool

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestSnapshotTracked_ChangedSince verifies that SnapshotTracked captures
// mtimes before a command and ChangedSince correctly identifies files whose
// mtime advanced after a command like gofmt or sed -i modified them on disk.
func TestSnapshotTracked_ChangedSince(t *testing.T) {
	tracker := NewFileIntegrityTracker()
	defer tracker.Reset()

	tmpDir := t.TempDir()
	path1 := filepath.Join(tmpDir, "a.go")
	path2 := filepath.Join(tmpDir, "b.go")
	path3 := filepath.Join(tmpDir, "c.go")

	// Create files and record reads.
	for _, p := range []string{path1, path2, path3} {
		if err := os.WriteFile(p, []byte("package main\n"), 0644); err != nil {
			t.Fatal(err)
		}
		tracker.RecordRead(p)
	}

	// Snapshot before command.
	snap := tracker.SnapshotTracked()
	if len(snap) != 3 {
		t.Fatalf("expected 3 tracked files, got %d", len(snap))
	}

	// Simulate a command modifying path1 and path2 on disk, but not path3.
	// Sleep briefly so mtime resolution advances.
	time.Sleep(20 * time.Millisecond)
	_ = os.WriteFile(path1, []byte("package main // changed\n"), 0644)
	_ = os.WriteFile(path2, []byte("package main // also changed\n"), 0644)

	changed := tracker.ChangedSince(snap)
	if len(changed) != 2 {
		t.Fatalf("expected 2 changed files, got %d: %v", len(changed), changed)
	}

	// Verify the unchanged file is not in the list.
	for _, c := range changed {
		if c == path3 {
			t.Errorf("path3 should not be in changed list")
		}
	}
}

// TestDetectChangedFilesFromCommand verifies the notice string is produced
// when tracked files are modified, and empty when nothing changed.
func TestDetectChangedFilesFromCommand(t *testing.T) {
	defaultFileTracker.Reset()
	defer defaultFileTracker.Reset()

	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "target.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}
	defaultFileTracker.RecordRead(path)

	// Snapshot - nothing changed yet.
	snap := defaultFileTracker.SnapshotTracked()
	if detectChangedFilesFromCommand(snap) != "" {
		t.Errorf("expected empty notice when nothing changed")
	}

	// Modify file after snapshot.
	time.Sleep(20 * time.Millisecond)
	_ = os.WriteFile(path, []byte("package main // formatted\n"), 0644)

	notice := detectChangedFilesFromCommand(snap)
	if notice == "" {
		t.Errorf("expected non-empty notice when file changed")
	}
}
