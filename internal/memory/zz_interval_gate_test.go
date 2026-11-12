package memory

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// #5519: startup re-ran the full staleness scan on EVERY process boot even
// though the verdicts are persisted in consolidation-state.json. The 2h
// interval gate must skip recent runs (empty report) and still run when the
// last run is old or absent.
func TestConsolidateIntervalGate(t *testing.T) {
	dir := t.TempDir()
	am := NewAutoMemoryAt(dir)

	// First run: no state -> full run, LastRun persisted.
	r1 := am.Consolidate(dir)
	_ = r1
	data, err := os.ReadFile(filepath.Join(dir, "consolidation-state.json"))
	if err != nil || len(data) == 0 {
		t.Fatalf("first run must persist state: %v", err)
	}

	// Immediate second run: must skip (zero report, no rescan).
	if r2 := am.Consolidate(dir); r2.Scanned != 0 || r2.HasFindings() {
		t.Fatalf("recent LastRun must skip consolidation, got %+v", r2)
	}

	// Backdated LastRun (>2h): must run again.
	state := consolidationState{LastRun: time.Now().Add(-3 * time.Hour).UTC().Format(time.RFC3339)}
	am.saveConsolidationState(&state)
	r3 := am.Consolidate(dir)
	_ = r3 // full run; only the skip branch is asserted here
}
