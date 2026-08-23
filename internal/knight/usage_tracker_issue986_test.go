package knight

import (
	"os"
	"path/filepath"
	"testing"
)

// --- Issue #986, Problem 3: transient read failure must not cause history loss ---

// A missing file is a legal fresh start: tracker loads (empty) and saves fine.
func TestIssue986UsageTrackerMissingFileInitializes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.json")
	ut := NewUsageTracker(path)
	ut.RecordUse("deploy-skill")
	ut.Flush()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("usage file not written: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("usage file is empty")
	}
}

// Corrupt JSON on disk: saves must be refused so the on-disk history survives.
func TestIssue986UsageTrackerCorruptFileRefusesOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.json")
	// Truncated JSON: parse must fail so the tracker refuses to overwrite.
	history := []byte(`{"legacy-skill":{"usage_count":42,`)
	if err := os.WriteFile(path, history, 0600); err != nil {
		t.Fatalf("seed history: %v", err)
	}

	ut := NewUsageTracker(path)
	ut.RecordUse("new-skill") // triggers ensureLoaded + markDirty + attempted save
	ut.Flush()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(data) != string(history) {
		t.Errorf("history was overwritten after corrupt read:\n got: %s\nwant: %s", data, history)
	}
}

// Unreadable file (permission denied): saves must be refused as well.
func TestIssue986UsageTrackerUnreadableFileRefusesOverwrite(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission-based test unreliable as root")
	}
	path := filepath.Join(t.TempDir(), "usage.json")
	history := []byte(`{"legacy-skill":{"usage_count":7,`)
	if err := os.WriteFile(path, history, 0600); err != nil {
		t.Fatalf("seed history: %v", err)
	}
	if err := os.Chmod(path, 0000); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	ut := NewUsageTracker(path)
	ut.RecordUse("new-skill")
	ut.Flush()

	// Restore readability before verifying content survived.
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatalf("restore chmod: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(data) != string(history) {
		t.Errorf("history was overwritten after unreadable file:\n got: %s\nwant: %s", data, history)
	}
}

// A directory in place of the file yields a non-not-exist read error and must
// also refuse to overwrite; once the obstruction clears the tracker recovers.
func TestIssue986UsageTrackerLoadRetriesAfterError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "usage.json")
	if err := os.Mkdir(path, 0755); err != nil { // read error: is a directory
		t.Fatalf("mkdir: %v", err)
	}

	ut := NewUsageTracker(path)
	ut.RecordUse("new-skill") // must not panic and must not write

	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("tracker wrote inside unreadable path: %v", entries)
	}

	// Remove the obstruction and verify the tracker recovers on retry.
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove obstruction: %v", err)
	}
	ut.RecordUse("new-skill")
	ut.Flush()
	if data, err := os.ReadFile(path); err != nil || len(data) == 0 {
		t.Errorf("tracker did not recover after obstruction cleared: data=%q err=%v", data, err)
	}
}
