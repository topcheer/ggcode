package agent

import (
	"path/filepath"
	"testing"
	"time"
)

// #3266 regression probes (J watermark / I decay math / H panel fidelity).

// J: re-ingesting the same teammate ledger twice must be a no-op on the
// merged row (zero Reinforced/Confidence delta) — the watermark reads
// max(Timestamp, LastReinforced) because consolidateLearnings keeps the
// OLDEST Timestamp on the merged row.
func TestIssue3266_TeammateReingestIdempotent(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	// Ledger with three entries, distinct monotonic ts (all land in the
	// same Category=teammate_experience, so persist consolidates them
	// into ONE merged row that keeps the OLDEST Timestamp).
	t0 := time.Now().Add(-3 * time.Hour)
	writeLedger(t, dir, []map[string]any{
		{"ts": t0, "team_id": "t1", "teammate": "tm-1 (coder)", "digest": "first digest"},
		{"ts": t0.Add(time.Hour), "team_id": "t1", "teammate": "tm-2 (tester)", "digest": "second digest"},
		{"ts": t0.Add(2 * time.Hour), "team_id": "t1", "teammate": "tm-3 (coder)", "digest": "third digest"},
	})

	flush := func() []trajectoryLearning {
		s := newTrajIntelState()
		s.ingestTeammateExperience(dir)
		s.mu.Lock()
		defer s.mu.Unlock()
		if err := s.persistLocked(); err != nil {
			t.Fatal(err)
		}
		return readStoreRows(t, filepath.Join(dir, ".ggcode", "trajectory-learnings.jsonl"))
	}

	after1 := flush()
	if len(after1) != 1 {
		t.Fatalf("expected one merged row, got %d", len(after1))
	}
	row := after1[0]
	if row.Reinforced != 2 {
		t.Fatalf("merge wrong: %+v", row)
	}
	if row.Insight != "teammate experience: third digest" {
		t.Fatalf("newest digest must win the merge, got %q", row.Insight)
	}

	// Second run over the SAME ledger: zero delta (the old watermark read
	// the merged row's oldest Timestamp, so t2/t3 were re-counted every
	// run - Reinforced inflated unbounded).
	after2 := flush()
	if len(after2) != 1 {
		t.Fatalf("expected still one row, got %d", len(after2))
	}
	if after2[0].Reinforced != row.Reinforced {
		t.Fatalf("re-ingest must be idempotent: Reinforced %d -> %d", row.Reinforced, after2[0].Reinforced)
	}
	if after2[0].Confidence != row.Confidence {
		t.Fatalf("re-ingest must be idempotent: Confidence %v -> %v", row.Confidence, after2[0].Confidence)
	}
	if after2[0].Insight != row.Insight {
		t.Fatal("re-ingest must be idempotent: Insight drifted")
	}
}

// I: decay can now cross the injection floor (old 0.5-midpoint math made
// that impossible), zero-value entries age out too, and failed-retired
// entries do not resurrect above the gate.
func TestIssue3266_DecayCrossesFloorAndNoResurrect(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	old := time.Now().UTC().Add(-10 * trajConfidenceHalfLife)
	fresh := trajectoryLearning{Category: "a", Type: "strategy", Insight: "fresh", Confidence: 0.95, LastReinforced: time.Now().UTC()}
	if ef := fresh.EffectiveConfidence(); ef < trajPromptMinConfidence {
		t.Fatalf("fresh high-confidence must inject, got %v", ef)
	}
	// Same entry, many half-lives old: must fall BELOW the 0.3 floor.
	aged := fresh
	aged.LastReinforced = old
	if ef := aged.EffectiveConfidence(); ef >= trajPromptMinConfidence {
		t.Fatalf("decay must cross the floor, got %v", ef)
	}
	// Zero-value (never reinforced): starts at the 0.5 baseline but must
	// also age out instead of pinning at 0.5 forever.
	zero := trajectoryLearning{Category: "b", Type: "pattern", Insight: "zero", Timestamp: old}
	if ef := zero.EffectiveConfidence(); ef >= 0.5 {
		t.Fatalf("zero-value entry must decay below baseline, got %v", ef)
	}
	// Failed-retired (0.2 < floor): converging upward must stay under the
	// gate — no resurrection after 30 days.
	failedOld := trajectoryLearning{Category: "c", Type: "failure", Insight: "failed", Confidence: 0.2, LastReinforced: old}
	if ef := failedOld.EffectiveConfidence(); ef >= trajPromptMinConfidence {
		t.Fatalf("failed-retired entry resurrected: %v", ef)
	}
}

// H: Injects must simulate ALL five renderer layers — budget, confidence
// gate, category dedupe, per-Type quota, effectiveness gate — including
// global-tier entries.
func TestIssue3266_PanelInjectsSimulatesAllLayers(t *testing.T) {
	ws := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	trajHoldoutEnabled = false // r462 holdout claims single-candidate fixtures
	t.Cleanup(func() { trajHoldoutEnabled = true })
	now := time.Now().UTC()
	// Same Type exceeding the per-Type quota + low-confidence entry +
	// a global-only category that must be visible and flagged General.
	writeR460Store(t, filepath.Join(ws, ".ggcode", "trajectory-learnings.jsonl"), []trajectoryLearning{
		{Category: "c1", Type: "strategy", Insight: "quota-1", Confidence: 0.9, LastReinforced: now},
		{Category: "c2", Type: "strategy", Insight: "quota-2", Confidence: 0.88, LastReinforced: now},
		{Category: "c3", Type: "strategy", Insight: "quota-3", Confidence: 0.86, LastReinforced: now}, // exceeds quota if trajPromptPerType<3
		{Category: "c4", Type: "hint", Insight: "low", Confidence: 0.2, LastReinforced: now},          // below floor
	})
	writeR460Store(t, filepath.Join(home, ".ggcode", "trajectory-learnings.jsonl"), []trajectoryLearning{
		{Category: "gcat", Type: "pattern", Insight: "global-only", Confidence: 0.9, LastReinforced: now},
	})
	views := TrajListLearnings(ws)
	var q1, q2, q3, low, gen, genInjects bool
	for _, v := range views {
		switch v.Insight {
		case "quota-1":
			q1 = v.Injects
		case "quota-2":
			q2 = v.Injects
		case "quota-3":
			q3 = v.Injects
		case "low":
			low = v.Injects
		case "global-only (general, other projects)":
			gen = v.General
			genInjects = v.Injects
		}
	}
	if !q1 || !q2 {
		t.Fatalf("top entries must inject: q1=%v q2=%v", q1, q2)
	}
	if q3 && trajPromptPerType < 3 {
		t.Fatal("per-Type quota not simulated")
	}
	if low {
		t.Fatal("below-floor entry reported as injecting")
	}
	if !gen || !genInjects {
		t.Fatal("global-tier entry must be visible with General=true and Injects simulated")
	}
}

func readStoreRows(t *testing.T, path string) []trajectoryLearning {
	t.Helper()
	s := newTrajIntelState()
	s.filePath = path
	entries, err := s.loadFromFile()
	if err != nil {
		t.Fatal(err)
	}
	return entries
}
