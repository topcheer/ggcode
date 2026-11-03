//go:build goolm

package tool

import (
	"path/filepath"
	"testing"
)

func TestIssue4476_FilePathFuzzyNegativeMaxResultsNoPanic(t *testing.T) {
	m := issue1317Manager(t, map[string]string{
		"a.go": "package a\n\nfunc alpha() {}\n",
	})
	defer m.Stop()

	m.mu.Lock()
	m.dirtyFiles = map[string]int64{filepath.Join(m.workingDir, "a.go"): 1}
	m.mu.Unlock()
	m.rebuildDirty("test")

	out := m.FilePathFuzzy("a.go", -1)
	if len(out) == 0 {
		t.Fatalf("guarded fuzzy query should still match a.go, got %v", out)
	}
	if len(out) > 10 {
		t.Fatalf("guarded cap should be 10, got %d", len(out))
	}
	if out := m.FilePathFuzzy("a.go", 0); len(out) == 0 {
		t.Fatalf("zero maxResults should also fall back to the default, got %v", out)
	}
}
