package sessionidx

import (
	"os"
	"path/filepath"
	"testing"
)

// #5184: scanExternal full rebuild discarded decoded entries without
// releasing their totalDecoded slots (and left them in evictionOrder), so
// ghost occupancy double-counted on re-decode and eventually rejected all
// admissions until daemon restart.

// TestIssue5184_RebuildCarriesUnchangedDecodedEntry pins the carry path:
// a new sibling idx triggers a rebuild; the already-decoded session with an
// unchanged file (id+mtime+size) must keep its decoded copy - totalDecoded
// stays stable after a second query instead of doubling.
func TestIssue5184_RebuildCarriesUnchangedDecodedEntry(t *testing.T) {
	dir := t.TempDir()
	seedIdx(t, dir, "sessA", 40)
	m := NewManager(dir, "")
	m.SetTotalBudget(1 << 20)
	m.scanExternal()
	_ = m.Query([]string{"token-alpha"}, "sessA", 2)
	afterFirst := m.TotalDecodedBytes()
	if afterFirst == 0 {
		t.Fatal("sessA must be decoded after first query")
	}

	// New sibling session appears -> refreshExternal sees count mismatch and
	// runs scanExternal (full rebuild).
	seedIdx(t, dir, "sibling", 40)
	m.refreshExternal()

	// The unchanged decoded file must survive the rebuild: either carried
	// (decoded stays true, slot intact) or released - never ghosted.
	afterRebuild := m.TotalDecodedBytes()
	if afterRebuild != afterFirst {
		t.Fatalf("rebuild must carry or release decoded slots: got %d, want %d", afterRebuild, afterFirst)
	}
	_ = m.Query([]string{"token-alpha"}, "sessA", 2)
	afterSecond := m.TotalDecodedBytes()
	if afterSecond != afterFirst {
		t.Fatalf("ghost double-count: after second query totalDecoded=%d, first=%d (issue repro was 2x)", afterSecond, afterFirst)
	}
}

// TestIssue5184_RebuildReleasesDroppedDecodedEntry pins the release path:
// when the rebuild drops a decoded entry (idx file removed), its budget
// slot must be released so future decodes are not blocked by ghosts.
func TestIssue5184_RebuildReleasesDroppedDecodedEntry(t *testing.T) {
	dir := t.TempDir()
	seedIdx(t, dir, "gone", 40)
	seedIdx(t, dir, "stay", 40)
	m := NewManager(dir, "")
	m.SetTotalBudget(1 << 20)
	m.scanExternal()
	_ = m.Query([]string{"token-alpha"}, "gone", 2)
	if m.TotalDecodedBytes() == 0 {
		t.Fatal("gone must be decoded after first query")
	}

	// Remove the decoded sibling's idx; a later scan (refreshExternal
	// triggers it whenever the table count drifts - here we call
	// scanExternal directly to pin the rebuild path itself) drops the
	// entry, and the drop must release its slot.
	_ = os.Remove(filepath.Join(IdxDir(dir), "gone.idx"))
	seedIdx(t, dir, "fresh", 40)
	m.scanExternal()

	if got := m.TotalDecodedBytes(); got != 0 {
		t.Fatalf("dropped decoded entry must release its slot: totalDecoded=%d", got)
	}
	// Budget must still admit fresh decodes (no ghost blockage).
	_ = m.Query([]string{"token-alpha"}, "stay", 2)
	if m.TotalDecodedBytes() == 0 {
		t.Fatal("new decode must be admissible after ghost release")
	}
}

// TestIssue5184_SelfOverLimitReleasesSlots pins the early-return path:
// when the self idx exceeds the limit and the whole external table is
// dropped, decoded slots must be released (previously: silent ghosts).
func TestIssue5184_SelfOverLimitReleasesSlots(t *testing.T) {
	dir := t.TempDir()
	seedIdx(t, dir, "ext1", 40)
	m := NewManager(dir, "")
	m.SetTotalBudget(1 << 20)
	m.scanExternal()
	_ = m.Query([]string{"token-alpha"}, "ext1", 2)
	if m.TotalDecodedBytes() == 0 {
		t.Fatal("ext1 must be decoded after first query")
	}

	// Same-package test: register a self index and shrink the self limit so
	// scanExternal takes the drop-everything early return.
	selfIx, err := Load(dir, "ext1")
	if err != nil {
		t.Fatalf("load self: %v", err)
	}
	m.mu.Lock()
	m.self = selfIx
	m.selfLimitBytes = 1
	m.mu.Unlock()

	m.scanExternal()
	if got := m.TotalDecodedBytes(); got != 0 {
		t.Fatalf("dropping the external table must release decoded slots: totalDecoded=%d", got)
	}
	if len(m.evictionOrder) != 0 {
		t.Fatalf("evictionOrder must be cleared with the table, got %d entries", len(m.evictionOrder))
	}
}
