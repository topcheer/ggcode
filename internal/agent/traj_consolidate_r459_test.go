package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// r459: learning consolidation probes.

// mkConfLearning builds a learning carrying explicit consolidation fields.
func mkConfLearning(ts time.Time, typ, cat, insight string, conf float64, reinforced int, last time.Time) trajectoryLearning {
	return trajectoryLearning{
		Timestamp: ts, Type: typ, Category: cat, Insight: insight,
		Success: true, Confidence: conf, Reinforced: reinforced, LastReinforced: last,
	}
}

func TestConsolidateLearnings_MergesAndReinforces(t *testing.T) {
	base := time.Now().Add(-time.Hour)
	all := []trajectoryLearning{
		mkConfLearning(base, "strategy", "over_exploration", "old insight", 0.5, 0, base),
		mkConfLearning(base.Add(time.Minute), "strategy", "over_exploration", "repeat", 0.5, 0, base), // Success=true -> up
	}
	out := consolidateLearnings(all)
	if len(out) != 1 {
		t.Fatalf("same category+type must merge, got %d", len(out))
	}
	if out[0].Reinforced != 1 || out[0].Confidence <= 0.5 {
		t.Fatalf("reinforcement must bump confidence: %+v", out[0])
	}
	// #3266(J): newest text wins arbitration - keeping the oldest froze
	// the merged row at the stalest digest while confidence only rose.
	if out[0].Insight != "repeat" {
		t.Fatalf("newest text wins arbitration: %q", out[0].Insight)
	}
}

func TestConsolidateLearnings_FailureLowers(t *testing.T) {
	base := time.Now().Add(-time.Hour)
	fail := mkConfLearning(base, "strategy", "x", "ins", 0.5, 0, base)
	fail.Success = false
	out := consolidateLearnings([]trajectoryLearning{
		mkConfLearning(base.Add(-time.Minute), "strategy", "x", "ins", 0.5, 0, base),
		fail,
	})
	if out[0].Confidence >= 0.5 {
		t.Fatalf("failure reinforcement must lower confidence: %f", out[0].Confidence)
	}
}

func TestConsolidateLearnings_DistinctCategoriesKept(t *testing.T) {
	base := time.Now()
	out := consolidateLearnings([]trajectoryLearning{
		mkConfLearning(base, "strategy", "a", "1", 0.5, 0, base),
		mkConfLearning(base, "recovery", "b", "2", 0.5, 0, base),
		mkConfLearning(base, "teammate", "c", "3", 0.5, 0, base),
	})
	if len(out) != 3 {
		t.Fatalf("distinct categories must not merge, got %d", len(out))
	}
}

func TestEffectiveConfidence_LegacyBaselineAndDecay(t *testing.T) {
	legacy := trajectoryLearning{Timestamp: time.Now()}
	if got := legacy.EffectiveConfidence(); got != 0.5 {
		t.Fatalf("legacy zero-value entries must read 0.5, got %f", got)
	}
	fresh := mkConfLearning(time.Now(), "s", "c", "i", 0.9, 3, time.Now())
	if got := fresh.EffectiveConfidence(); got < 0.89 || got > 0.91 {
		t.Fatalf("fresh high-confidence entry should stay ~0.9, got %f", got)
	}
	old := mkConfLearning(time.Now().Add(-120*24*time.Hour), "s", "c", "i", 0.9, 3, time.Now().Add(-120*24*time.Hour))
	if got := old.EffectiveConfidence(); got >= 0.7 {
		t.Fatalf("120-day-old entry must decay toward 0.5, got %f", got)
	}
}

func TestRenderPromptSection_LowConfidenceGated(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // r460: isolate from any real global store
	dir := t.TempDir()
	now := time.Now()
	writeLearnings(t, dir, []trajectoryLearning{
		mkConfLearning(now, "strategy", "weak", "eroded insight", 0.1, 5, now),
		mkConfLearning(now, "strategy", "strong", "solid insight", 0.9, 5, now),
	})
	s := newTrajIntelState()
	got := s.RenderPromptSection(dir)
	if strings.Contains(got, "eroded insight") {
		t.Fatalf("below-threshold insight must not inject: %q", got)
	}
	if !strings.Contains(got, "solid insight") {
		t.Fatalf("high-confidence insight must inject: %q", got)
	}
}

func TestRenderPromptSection_ReinforcedOlderBeatsFreshNoise(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // r460: isolate from any real global store
	dir := t.TempDir()
	now := time.Now()
	writeLearnings(t, dir, []trajectoryLearning{
		mkConfLearning(now.Add(-2*time.Hour), "strategy", "proven", "proven pattern", 0.9, 5, now.Add(-time.Minute)),
		mkConfLearning(now, "strategy", "newer", "one-off noise", 0.5, 0, now),
	})
	s := newTrajIntelState()
	got := s.RenderPromptSection(dir)
	if !strings.Contains(got, "proven pattern") {
		t.Fatalf("reinforced insight must not be silenced by newer noise: %q", got)
	}
}

func TestPersistConsolidatesDuplicateRows(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // isolate TrajGlobalPath (#3266(H): list now reads the global tier)
	dir := t.TempDir()
	now := time.Now()
	// Simulate a store with duplicates: persist twice with same category.
	s := newTrajIntelState()
	s.filePath = filepath.Join(dir, ".ggcode", "trajectory-learnings.jsonl")
	s.mu.Lock()
	s.learnings = []trajectoryLearning{
		mkConfLearning(now, "strategy", "dup", "insight v1", 0.5, 0, now),
		mkConfLearning(now.Add(time.Second), "strategy", "dup", "insight v2", 0.5, 0, now),
	}
	s.mu.Unlock()
	if err := s.persistLocked(); err != nil {
		t.Fatalf("persist: %v", err)
	}
	views := TrajListLearnings(dir)
	if len(views) != 1 {
		t.Fatalf("persist must consolidate duplicates on write, got %d views", len(views))
	}
	if views[0].Reinforced != 1 {
		t.Fatalf("expected reinforced=1 after merge, got %d", views[0].Reinforced)
	}
}

func TestTrajListAndClear_UserSurface(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // isolate TrajGlobalPath (#3266(H): list now reads the global tier)
	dir := t.TempDir()
	now := time.Now()
	writeLearnings(t, dir, []trajectoryLearning{
		mkConfLearning(now, "strategy", "a", "visible", 0.8, 2, now),
	})
	views := TrajListLearnings(dir)
	if len(views) != 1 || !views[0].Injects {
		t.Fatalf("list projection broken: %+v", views)
	}
	if err := TrajClearLearnings(dir); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".ggcode", "trajectory-learnings.jsonl")); !os.IsNotExist(err) {
		t.Fatal("clear must remove the store file")
	}
	if TrajClearLearnings(dir) != nil {
		t.Fatal("clear must be idempotent on absent file")
	}
	if got := TrajListLearnings(dir); got != nil {
		t.Fatalf("list after clear must be empty, got %d", len(got))
	}
}
