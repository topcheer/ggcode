package agent

// Probes for #3266:
//   - J: the ingest watermark must survive consolidation (which keeps the
//     OLDEST row's Timestamp and moves recency into LastReinforced). A
//     merged teammate row must not re-ingest the ledger entries it already
//     absorbed - two rounds of ingest after consolidation = zero fresh.
//   - H: TrajListLearnings.Injects must predict the renderer's FULL
//     selection (gates + dedupe + per-Type cap + overall cap) and the
//     panel must surface the global tier.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeTeammateLedger3266(t *testing.T, dir string, ts []time.Time) {
	t.Helper()
	var body string
	for i, tt := range ts {
		body += `{"ts":"` + tt.UTC().Format(time.RFC3339Nano) + `","team_id":"T","teammate":"tm-1","digest":"did thing ` + string(rune('A'+i)) + `"}` + "\n"
	}
	if err := os.MkdirAll(filepath.Join(dir, ".ggcode"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".ggcode", "teammate-experience.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Round 1 ingests everything; after consolidation merges the rows (oldest
// Timestamp kept, recency in LastReinforced), round 2 must ingest NOTHING.
func TestIssue3266_IngestIdempotentAcrossConsolidation(t *testing.T) {
	dir := t.TempDir()
	base := time.Now().Add(-time.Hour)
	writeTeammateLedger3266(t, dir, []time.Time{base, base.Add(time.Minute), base.Add(2 * time.Minute)})

	s := newTrajIntelState()
	s.ingestTeammateExperience(dir)
	s.mu.Lock()
	first := len(s.learnings)
	s.mu.Unlock()
	if first != 3 {
		t.Fatalf("round 1: expected 3 fresh, got %d", first)
	}

	// Simulate what persistLocked would have written: the three rows
	// consolidated into ONE (Timestamp = oldest, LastReinforced = newest).
	merged := trajectoryLearning{
		Timestamp: base, Type: "teammate", Category: "teammate_experience",
		Insight: "teammate experience: did thing A", Confidence: 0.7,
		Reinforced: 2, LastReinforced: base.Add(2 * time.Minute),
	}
	writeLearnings(t, dir, []trajectoryLearning{merged})

	// Round 2 on the SAME ledger: with the old Timestamp-only watermark the
	// merged row read as base and entries 2/3 re-ingested every run.
	s2 := newTrajIntelState()
	s2.ingestTeammateExperience(dir)
	s2.mu.Lock()
	second := len(s2.learnings)
	s2.mu.Unlock()
	if second != 0 {
		t.Fatalf("round 2 after consolidation: %d fresh entries re-ingested (watermark regression, want 0)", second)
	}
}

func TestIssue3266_InjectsPredictsFullSelection(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home) // isolate the global tier
	dir := t.TempDir()
	base := time.Now()

	// 10 distinct strategy categories + 2 recovery categories: per-Type
	// cap 3 and overall cap 8 must bite; the 9th/10th strategy entries and
	// the 4th+ of any type must show Injects=false even above the gates.
	var ls []trajectoryLearning
	for i := 0; i < 10; i++ {
		ls = append(ls, mkLearning(base.Add(-time.Duration(i)*time.Minute), "strategy", string(rune('a'+i)), "s"+string(rune('a'+i))))
	}
	ls = append(ls, mkLearning(base.Add(-time.Minute), "recovery", "r1", "rec1"))
	ls = append(ls, mkLearning(base.Add(-2*time.Minute), "recovery", "r2", "rec2"))
	writeLearnings(t, dir, ls)

	views := TrajListLearnings(dir)
	if len(views) != 12 {
		t.Fatalf("panel must list all local entries, got %d", len(views))
	}
	injectCount := 0
	for _, v := range views {
		if v.Injects {
			injectCount++
		}
	}
	// per-Type cap binds first: 3 strategy + 2 recovery = 5 injects (the
	// overall cap of 8 never bites in this shape - per-type is tighter).
	if injectCount != 5 {
		t.Fatalf("Injects must mirror per-Type caps (3 strategy + 2 recovery = 5), got %d", injectCount)
	}
	strategyInjects := 0
	for _, v := range views {
		if v.Injects && v.Type == "strategy" {
			strategyInjects++
		}
	}
	if strategyInjects != 3 { // per-Type cap
		t.Fatalf("Injects must mirror the per-Type cap of 3 for strategy, got %d", strategyInjects)
	}
}

func TestIssue3266_PanelSurfacesGlobalTier(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// Global store holds a category the workspace lacks.
	gp := filepath.Join(home, ".ggcode", "trajectory-learnings.jsonl")
	if err := os.MkdirAll(filepath.Dir(gp), 0o755); err != nil {
		t.Fatal(err)
	}
	g := mkLearning(time.Now().Add(-time.Minute), "strategy", "global_only", "from other projects")
	gb, err := json.Marshal(g)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(gp, append(gb, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	writeLearnings(t, dir, []trajectoryLearning{
		mkLearning(time.Now(), "recovery", "local_rec", "local insight"),
	})

	views := TrajListLearnings(dir)
	var sawGlobal bool
	for _, v := range views {
		if v.Category == "global_only" {
			sawGlobal = true
			if v.Injects {
				// global fills a missing category: it should be selected
			} else {
				t.Fatal("global tier entry that fills a gap must predict Injects=true")
			}
		}
	}
	if !sawGlobal {
		t.Fatal("panel must surface the global tier entries (was invisible pre-#3266)")
	}
}
