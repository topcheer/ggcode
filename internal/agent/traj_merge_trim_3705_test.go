package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTrajMergeInto_TrimsLowestConfidenceNotTailFIFO(t *testing.T) {
	main := t.TempDir()
	storeDir := filepath.Join(main, ".ggcode")
	if err := os.MkdirAll(storeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	dstPath := filepath.Join(storeDir, "trajectory-learnings.jsonl")

	base := time.Now()
	var lines []byte
	writeLine := func(l trajectoryLearning) {
		b, err := json.Marshal(l)
		if err != nil {
			t.Fatal(err)
		}
		lines = append(append(lines, b...), '\n')
	}

	for i := 0; i < 10; i++ {
		writeLine(trajectoryLearning{
			Category:       "cat-reinforced",
			Type:           fmt.Sprintf("r%d", i),
			Insight:        fmt.Sprintf("reinforced-insight-%d", i),
			Confidence:     0.9,
			Reinforced:     18,
			Success:        true,
			Timestamp:      base.Add(-1 * time.Hour),
			LastReinforced: base.Add(-1 * time.Hour),
		})
	}

	for i := 0; i < 40; i++ {
		writeLine(trajectoryLearning{
			Category:   "cat-noise",
			Type:       fmt.Sprintf("n%d", i),
			Insight:    fmt.Sprintf("noise-insight-%d", i),
			Confidence: 0.5,
			Timestamp:  base.Add(-60 * 24 * time.Hour),
		})
	}
	if err := os.WriteFile(dstPath, lines, 0o644); err != nil {
		t.Fatal(err)
	}

	var srcLines []byte
	for i := 0; i < 5; i++ {
		b, err := json.Marshal(trajectoryLearning{
			Category:   "cat-fresh",
			Type:       fmt.Sprintf("f%d", i),
			Insight:    fmt.Sprintf("fresh-insight-%d", i),
			Confidence: 0.8,
			Timestamp:  base,
		})
		if err != nil {
			t.Fatal(err)
		}
		srcLines = append(append(srcLines, b...), '\n')
	}
	srcPath := filepath.Join(t.TempDir(), "src.jsonl")
	if err := os.WriteFile(srcPath, srcLines, 0o644); err != nil {
		t.Fatal(err)
	}

	added, err := TrajMergeInto(main, srcPath)
	if err != nil {
		t.Fatalf("TrajMergeInto: %v", err)
	}
	if added != 5 {
		t.Fatalf("added = %d, want 5", added)
	}

	got, err := loadTrajFile(dstPath)
	if err != nil {
		t.Fatalf("loadTrajFile: %v", err)
	}
	if len(got) != trajIntelMaxEntries {
		t.Fatalf("store size = %d, want %d", len(got), trajIntelMaxEntries)
	}
	survive := map[string]bool{}
	for _, l := range got {
		survive[l.Insight] = true
	}

	for i := 0; i < 10; i++ {
		key := fmt.Sprintf("reinforced-insight-%d", i)
		if !survive[key] {
			t.Errorf("reinforced row %q was evicted; confidence-aware trim must keep it", key)
		}
	}
	for i := 0; i < 5; i++ {
		key := fmt.Sprintf("fresh-insight-%d", i)
		if !survive[key] {
			t.Errorf("fresh row %q was evicted; decayed noise must go first", key)
		}
	}
	droppedNoise := 0
	for i := 0; i < 40; i++ {
		if !survive[fmt.Sprintf("noise-insight-%d", i)] {
			droppedNoise++
		}
	}
	if droppedNoise != 5 {
		t.Errorf("dropped noise rows = %d, want exactly 5 (the lowest-confidence evictions)", droppedNoise)
	}

	for i := 1; i < len(got); i++ {
		if got[i].Timestamp.Before(got[i-1].Timestamp) {
			t.Fatalf("store rows out of chronological order at %d: %v before %v", i, got[i].Timestamp, got[i-1].Timestamp)
		}
	}
}

func TestTrimLearnings_EvictsLowestConfidence(t *testing.T) {
	base := time.Now()
	var all []trajectoryLearning
	for i := 0; i < 10; i++ {
		all = append(all, trajectoryLearning{
			Category: "hi", Type: fmt.Sprintf("h%d", i), Insight: fmt.Sprintf("hi-%d", i),
			Confidence: 0.95, Reinforced: 20, Timestamp: base.Add(-2 * time.Hour),
		})
	}
	for i := 0; i < 50; i++ {
		all = append(all, trajectoryLearning{
			Category: "lo", Type: fmt.Sprintf("l%d", i), Insight: fmt.Sprintf("lo-%d", i),
			Confidence: 0.1, Timestamp: base.Add(-10 * 24 * time.Hour),
		})
	}
	got := trimLearnings(all)
	if len(got) != trajIntelMaxEntries {
		t.Fatalf("len = %d, want %d", len(got), trajIntelMaxEntries)
	}
	hi := 0
	for _, l := range got {
		if l.Category == "hi" {
			hi++
		}
	}
	if hi != 10 {
		t.Errorf("high-confidence survivors = %d, want 10", hi)
	}
}
