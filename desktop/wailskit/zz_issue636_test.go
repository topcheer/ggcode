package wailskit

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// Issue #636: hitting the 10k entry cap used to return an error from the
// WalkDir callback, aborting the walk AND making walkDirectoryEntries discard
// every collected entry ("return nil, err"). Recursive browsing of a large
// repo returned NOTHING. The cap must truncate via filepath.SkipAll and
// return the ~10k already-collected entries.
func TestIssue636_CapTruncatesInsteadOfFailing(t *testing.T) {
	tmpDir := t.TempDir()

	// Create more entries than the cap (flat layout keeps setup fast).
	total := maxRecursiveEntries + 50
	for i := 0; i < total; i++ {
		p := filepath.Join(tmpDir, fmt.Sprintf("f%06d.txt", i))
		if err := os.WriteFile(p, nil, 0o600); err != nil {
			t.Fatalf("seed file %d: %v", i, err)
		}
	}

	entries, err := walkDirectoryEntries(tmpDir)
	if err != nil {
		t.Fatalf("recursive walk over the cap must not fail: %v", err)
	}
	if len(entries) != maxRecursiveEntries {
		t.Fatalf("expected truncated result of exactly %d entries, got %d", maxRecursiveEntries, len(entries))
	}

	// Below the cap: unchanged behavior — everything returned, no error.
	small := t.TempDir()
	for i := 0; i < 5; i++ {
		if err := os.WriteFile(filepath.Join(small, fmt.Sprintf("s%d.txt", i)), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	smallEntries, err := walkDirectoryEntries(small)
	if err != nil {
		t.Fatalf("small walk: %v", err)
	}
	if len(smallEntries) != 5 {
		t.Fatalf("small walk expected 5 entries, got %d", len(smallEntries))
	}
}
