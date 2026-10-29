package memory

// #4334 probes: a delete->recreate->delete sequence must keep BOTH deleted
// gaps - the old single-tombstone overwrite lost the first deletion
// instant, so an as_of inside the first gap time-traveled back to the
// already-deleted v1.

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestIssue4334_DoubleDeleteKeepsFirstGap(t *testing.T) {
	dir := t.TempDir()
	am := NewProjectAutoMemory(dir)
	if am == nil {
		t.Fatal("auto memory nil")
	}
	if err := am.SaveMemory("dd", "v1"); err != nil {
		t.Fatal(err)
	}
	preDelete := time.Now()
	time.Sleep(10 * time.Millisecond)
	if err := am.DeleteMemory("dd"); err != nil { // T1
		t.Fatal(err)
	}
	firstGapAt := time.Now()
	time.Sleep(10 * time.Millisecond)
	if err := am.SaveMemory("dd", "v2"); err != nil { // recreate
		t.Fatal(err)
	}
	midV2 := time.Now()
	time.Sleep(10 * time.Millisecond)
	if err := am.DeleteMemory("dd"); err != nil { // T2
		t.Fatal(err)
	}
	afterSecond := time.Now()
	time.Sleep(10 * time.Millisecond)

	// The core regression: as_of inside the FIRST deleted gap [T1, recreate)
	// must find nothing - the single-tombstone bug returned v1 here.
	if _, _, found, _ := am.ReadMemoryAsOf("dd", firstGapAt); found {
		t.Fatal("as_of in the first deleted gap must find nothing (tombstone overwrite regression)")
	}
	// After the second deletion: also nothing.
	if _, _, found, _ := am.ReadMemoryAsOf("dd", afterSecond); found {
		t.Fatal("as_of after the second delete must find nothing")
	}
	// Before the first deletion: v1 is exact.
	if content, _, found, _ := am.ReadMemoryAsOf("dd", preDelete); !found || content != "v1" {
		t.Fatalf("pre-delete as_of = (%q, %v), want (v1, true)", content, found)
	}
	// Between the recreate and the second deletion: v2 is exact.
	if content, _, found, _ := am.ReadMemoryAsOf("dd", midV2); !found || content != "v2" {
		t.Fatalf("recreated-interval as_of = (%q, %v), want (v2, true)", content, found)
	}
}

// Legacy compatibility: a pre-#4334 single tombstone (no timestamp suffix)
// must still cut the interval after its deletion instant.
func TestIssue4334_LegacySingleTombstoneStillHonored(t *testing.T) {
	dir := t.TempDir()
	am := NewProjectAutoMemory(dir)
	if am == nil {
		t.Fatal("auto memory nil")
	}
	if err := am.SaveMemory("legacy-tomb", "v1"); err != nil {
		t.Fatal(err)
	}
	midAt := time.Now()
	time.Sleep(10 * time.Millisecond)
	if err := am.DeleteMemory("legacy-tomb"); err != nil {
		t.Fatal(err)
	}
	delAt := time.Now()
	time.Sleep(10 * time.Millisecond)

	// Reshape to pre-#4334 on-disk state: replace every timestamp-suffixed
	// tombstone with one legacy single-path file carrying the same mtime.
	hdir := filepath.Join(am.dir, historyDirName)
	safe := disambiguateKey("legacy-tomb", sanitizeKey("legacy-tomb"))
	matches, _ := filepath.Glob(filepath.Join(hdir, safe+".tombstone.*"))
	var legacyMTime time.Time
	for _, m := range matches {
		if info, err := os.Stat(m); err == nil {
			legacyMTime = info.ModTime()
		}
		if err := os.Remove(m); err != nil {
			t.Fatal(err)
		}
	}
	if legacyMTime.IsZero() {
		t.Fatal("expected a timestamp-suffixed tombstone to reshape")
	}
	legacy := filepath.Join(hdir, safe+".tombstone")
	if err := os.WriteFile(legacy, []byte("legacy-tomb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(legacy, legacyMTime, legacyMTime); err != nil {
		t.Fatal(err)
	}

	// After the deletion instant (legacy tombstone only): found=false.
	if _, _, found, _ := am.ReadMemoryAsOf("legacy-tomb", delAt); found {
		t.Fatal("legacy single tombstone must still cut the interval after deletion")
	}
	// Before the deletion instant: v1 resolves exactly.
	if content, _, found, _ := am.ReadMemoryAsOf("legacy-tomb", midAt); !found || content != "v1" {
		t.Fatalf("pre-delete as_of = (%q, %v), want (v1, true)", content, found)
	}
}
