package memory

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
	if err := am.DeleteMemory("dd"); err != nil {
		t.Fatal(err)
	}
	firstGapAt := time.Now()
	time.Sleep(10 * time.Millisecond)
	if err := am.SaveMemory("dd", "v2"); err != nil {
		t.Fatal(err)
	}
	midV2 := time.Now()
	time.Sleep(10 * time.Millisecond)
	if err := am.DeleteMemory("dd"); err != nil {
		t.Fatal(err)
	}
	afterSecond := time.Now()
	time.Sleep(10 * time.Millisecond)

	if _, _, found, _ := am.ReadMemoryAsOf("dd", firstGapAt); found {
		t.Fatal("as_of in the first deleted gap must find nothing (tombstone overwrite regression)")
	}

	if _, _, found, _ := am.ReadMemoryAsOf("dd", afterSecond); found {
		t.Fatal("as_of after the second delete must find nothing")
	}

	if content, _, found, _ := am.ReadMemoryAsOf("dd", preDelete); !found || content != "v1" {
		t.Fatalf("pre-delete as_of = (%q, %v), want (v1, true)", content, found)
	}

	if content, _, found, _ := am.ReadMemoryAsOf("dd", midV2); !found || content != "v2" {
		t.Fatalf("recreated-interval as_of = (%q, %v), want (v2, true)", content, found)
	}
}

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

	if _, _, found, _ := am.ReadMemoryAsOf("legacy-tomb", delAt); found {
		t.Fatal("legacy single tombstone must still cut the interval after deletion")
	}

	if content, _, found, _ := am.ReadMemoryAsOf("legacy-tomb", midAt); !found || content != "v1" {
		t.Fatalf("pre-delete as_of = (%q, %v), want (v1, true)", content, found)
	}
}
