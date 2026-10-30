//go:build goolm

package tool

import (
	"path/filepath"
	"testing"
)

// #4476 Bug 2: FilePathFuzzy lacked Search's maxResults<=0 → 10 guard; a
// negative value made results[:maxResults] panic on the negative bound.
func TestIssue4476_FilePathFuzzyNegativeMaxResultsNoPanic(t *testing.T) {
	m := issue1317Manager(t, map[string]string{
		"a.go": "package a\n\nfunc alpha() {}\n",
	})
	defer m.Stop()

	// Prime the index (the fixture only marks ready; docs come from a
	// rebuild pass, same as the sibling 1317 tests).
	m.mu.Lock()
	m.dirtyFiles = map[string]int64{filepath.Join(m.workingDir, "a.go"): 1}
	m.mu.Unlock()
	m.rebuildDirty("test")

	// Pre-fix: results[:‑1] panics. Post-fix: guarded to default 10.
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
