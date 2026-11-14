package tool

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// #5268 item 2: a read-only command must not emit the files-changed
// advisory when a tracked file's mtime moves without a content change
// (mtime touch, content-identical rewrite).

func TestChangedSinceDigests_NoAdvisoryOnIdenticalContentRewrite(t *testing.T) {
	tracker := NewFileIntegrityTracker()
	defer tracker.Reset()

	dir := t.TempDir()
	path := filepath.Join(dir, "stable.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tracker.RecordRead(path)

	snap := tracker.SnapshotTrackedWithDigests()
	if len(snap) != 1 {
		t.Fatalf("expected 1 tracked file, got %d", len(snap))
	}
	if snap[path].Digest == 0 {
		t.Fatal("expected digest to be computed for small file")
	}

	// Simulate an mtime-only touch: same bytes, newer mtime.
	time.Sleep(20 * time.Millisecond)
	if err := os.Chtimes(path, time.Now(), time.Now()); err != nil {
		t.Fatal(err)
	}

	if changed := tracker.ChangedSinceDigests(snap); len(changed) != 0 {
		t.Errorf("phantom advisory: identical-content mtime bump reported %v as changed", changed)
	}

	// The baseline advanced, so a second no-op comparison stays silent too.
	if changed := tracker.ChangedSinceDigests(snap); len(changed) != 0 {
		t.Errorf("second pass re-flagged advanced baseline: %v", changed)
	}
}

func TestChangedSinceDigests_RealChangeStillDetected(t *testing.T) {
	tracker := NewFileIntegrityTracker()
	defer tracker.Reset()

	dir := t.TempDir()
	path := filepath.Join(dir, "edited.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tracker.RecordRead(path)
	snap := tracker.SnapshotTrackedWithDigests()

	time.Sleep(20 * time.Millisecond)
	if err := os.WriteFile(path, []byte("package main // edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	changed := tracker.ChangedSinceDigests(snap)
	if len(changed) != 1 || changed[0] != path {
		t.Errorf("expected real content change to be detected, got %v", changed)
	}
}

func TestDetectChangedFilesFromCommand_PhantomFixEndToEnd(t *testing.T) {
	defaultFileTracker.Reset()
	defer defaultFileTracker.Reset()

	dir := t.TempDir()
	path := filepath.Join(dir, "target.md")
	if err := os.WriteFile(path, []byte("note\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	defaultFileTracker.RecordRead(path)
	snap := defaultFileTracker.SnapshotTrackedWithDigests()

	if notice := detectChangedFilesFromCommand(snap); notice != "" {
		t.Errorf("expected no advisory when nothing changed, got: %s", notice)
	}

	// Read-only command runs; a sync tool rewrites the file with identical
	// bytes (mtime bump only). The advisory must stay silent.
	time.Sleep(20 * time.Millisecond)
	if err := os.Chtimes(path, time.Now(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if notice := detectChangedFilesFromCommand(snap); notice != "" {
		t.Errorf("phantom advisory after identical-content mtime bump: %s", notice)
	}

	// A real write by the command still reports.
	time.Sleep(20 * time.Millisecond)
	if err := os.WriteFile(path, []byte("note // changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if notice := detectChangedFilesFromCommand(snap); notice == "" {
		t.Error("expected advisory after real content change")
	}
}
