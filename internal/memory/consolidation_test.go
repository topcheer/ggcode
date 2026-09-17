package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newTestMemory builds an AutoMemory backed by a temp dir.
func newTestMemory(t *testing.T) *AutoMemory {
	t.Helper()
	return &AutoMemory{dir: t.TempDir()}
}

func writeTestEntry(t *testing.T, am *AutoMemory, key, content string) {
	t.Helper()
	if err := am.SaveMemory(key, content); err != nil {
		t.Fatalf("SaveMemory(%q): %v", key, err)
	}
}

func TestConsolidateCleanStore(t *testing.T) {
	am := newTestMemory(t)
	writeTestEntry(t, am, "build-process", "build command: go build ./...")

	report := am.Consolidate("")
	if report.Scanned != 1 {
		t.Fatalf("Scanned = %d, want 1", report.Scanned)
	}
	if report.HasFindings() {
		t.Fatalf("clean store produced warnings: %v", report.Warnings)
	}
}

func TestConsolidateDetectsConflicts(t *testing.T) {
	am := newTestMemory(t)
	writeTestEntry(t, am, "build-impl", "build command: go build ./...")
	writeTestEntry(t, am, "build-design", "build command: go build -tags goolm ./...")

	report := am.Consolidate("")
	if report.ConflictPairs != 1 {
		t.Fatalf("ConflictPairs = %d, want 1; warnings=%v", report.ConflictPairs, report.Warnings)
	}
	if len(report.Warnings) == 0 || !strings.Contains(report.Warnings[0], "conflict") {
		t.Fatalf("expected a conflict warning, got %v", report.Warnings)
	}
}

func TestConsolidateConflictFirstSeenPersists(t *testing.T) {
	am := newTestMemory(t)
	writeTestEntry(t, am, "build-impl", "test framework: gotest")
	writeTestEntry(t, am, "build-design", "test framework: gotest-race")

	first := am.Consolidate("")
	if first.ConflictPairs != 1 {
		t.Fatalf("first run: ConflictPairs = %d, want 1", first.ConflictPairs)
	}

	state := am.loadConsolidationState()
	if len(state.Conflicts) != 1 || state.Conflicts[0].FirstSeen == "" {
		t.Fatalf("state conflicts = %+v, want one with FirstSeen", state.Conflicts)
	}
	firstSeen := state.Conflicts[0].FirstSeen

	time.Sleep(10 * time.Millisecond) // ensure a later run timestamp differs
	second := am.Consolidate("")
	if second.ConflictPairs != 1 {
		t.Fatalf("second run: ConflictPairs = %d, want 1", second.ConflictPairs)
	}

	state2 := am.loadConsolidationState()
	if len(state2.Conflicts) != 1 || state2.Conflicts[0].FirstSeen != firstSeen {
		t.Fatalf("FirstSeen not preserved: got %+v, want %s", state2.Conflicts, firstSeen)
	}
}

func TestConsolidateDetectsSupersededNearDuplicates(t *testing.T) {
	am := newTestMemory(t)
	writeTestEntry(t, am, "cache-strategy", "cache: memory only")
	time.Sleep(10 * time.Millisecond)
	writeTestEntry(t, am, "cache-strategy-v2", "cache: memory + disk")

	report := am.Consolidate("")
	if report.SupersededPairs != 1 {
		t.Fatalf("SupersededPairs = %d, want 1; warnings=%v", report.SupersededPairs, report.Warnings)
	}
	state := am.loadConsolidationState()
	newer, ok := state.Superseded["cache-strategy"]
	if !ok || newer != "cache-strategy-v2" {
		t.Fatalf("Superseded map = %+v, want cache-strategy -> cache-strategy-v2", state.Superseded)
	}
}

func TestConsolidateEvolvingEntriesExcludedFromSupersede(t *testing.T) {
	// Evolving entries (research-*) already have curation-level keep-newest
	// semantics; consolidation must not duplicate that as a supersede pair.
	am := newTestMemory(t)
	writeTestEntry(t, am, "research-cache", "cache: level1")
	time.Sleep(10 * time.Millisecond)
	writeTestEntry(t, am, "research-cache-r2", "cache: level2")

	report := am.Consolidate("")
	if report.SupersededPairs != 0 {
		t.Fatalf("SupersededPairs = %d, want 0 for evolving pairs", report.SupersededPairs)
	}
}

func TestConsolidateDetectsStaleOversized(t *testing.T) {
	am := newTestMemory(t)
	writeTestEntry(t, am, "big-note", strings.Repeat("x", maxInlineBytes+100))

	report := am.Consolidate("")
	if report.StaleFindings == 0 {
		t.Fatalf("StaleFindings = 0, want >=1 for oversized entry")
	}
	found := false
	for _, w := range report.Warnings {
		if strings.Contains(w, "oversized") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected oversized warning, got %v", report.Warnings)
	}
}

func TestConsolidateSkipsCrossCategorySupersede(t *testing.T) {
	am := newTestMemory(t)
	writeTestEntry(t, am, "deploy-impl", "deploy: manual")
	time.Sleep(10 * time.Millisecond)
	writeTestEntry(t, am, "deploy-design", "deploy: automated")

	report := am.Consolidate("")
	if report.SupersededPairs != 0 {
		t.Fatalf("SupersededPairs = %d, want 0 for cross-category pair", report.SupersededPairs)
	}
}

func TestConsolidateStateFileIsNotAMemoryEntry(t *testing.T) {
	am := newTestMemory(t)
	writeTestEntry(t, am, "note-a", "alpha: one")
	am.Consolidate("")
	if _, err := loadTestState(am); err != nil {
		t.Fatalf("state file missing: %v", err)
	}
	keys, err := am.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range keys {
		if k == "consolidation-state" {
			t.Fatalf("state file leaked into memory listing: %v", keys)
		}
	}
}

func loadTestState(am *AutoMemory) ([]byte, error) {
	return os.ReadFile(filepath.Join(am.dir, consolidationStateFile))
}
