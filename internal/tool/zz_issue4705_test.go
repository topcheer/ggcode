package tool

import (
	"os"
	"path/filepath"
	"testing"
)

// #4705: a prefix with its own wildcards (*/**/*.go) used to be Joined
// literally into a nonexistent startDir - silent zero-match.
func TestGlobWildcardPrefixMatches(t *testing.T) {
	base := t.TempDir()
	for _, p := range []string{"a/x.go", "a/deep/d.go", "b/y.go", "b/keep/note.txt"} {
		full := filepath.Join(base, p)
		os.MkdirAll(filepath.Dir(full), 0755)
		os.WriteFile(full, []byte("x"), 0644)
	}
	got := walkGlob(base, "*/**/*.go", nil)
	if len(got) != 3 {
		t.Fatalf("*/**/*.go must match a/x.go a/deep/d.go b/y.go, got %d: %v (#4705)", len(got), got)
	}
	// Plain-prefix ** pattern still works (no regression).
	got = walkGlob(base, "a/**/*.go", nil)
	if len(got) != 2 {
		t.Fatalf("a/**/*.go must match 2, got %d: %v", len(got), got)
	}
}
