package wailskit

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestIssue636_CapTruncatesInsteadOfFailing(t *testing.T) {
	tmpDir := t.TempDir()

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
