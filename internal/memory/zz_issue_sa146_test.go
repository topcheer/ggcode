package memory

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSA146_LoadForPromptPersistsHighConfidenceLoss(t *testing.T) {
	dir := t.TempDir()
	am := &AutoMemory{dir: dir}
	writeMem(t, dir, "build-old-impl", "build command: go build ./...")
	writeMem(t, dir, "build-new-impl", "build command: go build -tags goolm ./...")

	oldPath := filepath.Join(dir, "build-old-impl.md")
	past := time.Now().Add(-400 * 24 * time.Hour)
	if err := os.Chtimes(oldPath, past, past); err != nil {
		t.Fatal(err)
	}

	if _, _, err := am.LoadForPrompt(); err != nil {
		t.Fatal(err)
	}
	loser, ok := am.UsageOf("build-old-impl")
	if !ok || loser.Conflicts != 1 {
		t.Fatalf("loser must carry exactly 1 persisted conflict, got ok=%v %+v", ok, loser)
	}
	winner, ok := am.UsageOf("build-new-impl")
	if !ok || winner.Conflicts != 0 {
		t.Fatalf("winner must stay clean, got ok=%v %+v", ok, winner)
	}
}

func TestSA146_ThinMarginLossNotPersisted(t *testing.T) {
	dir := t.TempDir()
	am := &AutoMemory{dir: dir}
	writeMem(t, dir, "cfg-a-impl", "build command: go build ./...")
	writeMem(t, dir, "cfg-b-impl", "build command: go build -tags goolm ./...")

	if _, _, err := am.LoadForPrompt(); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"cfg-a-impl", "cfg-b-impl"} {
		if info, ok := am.UsageOf(k); ok && info.Conflicts != 0 {
			t.Fatalf("%s must not persist a thin-margin loss: %+v", k, info)
		}
	}
}

func TestSA146_RecordConflictLossDebounceAndReload(t *testing.T) {
	am := newTestAutoMemory(t)
	am.RecordConflictLoss([]string{"k1"})
	am.RecordConflictLoss([]string{"k1"})
	info, _ := am.UsageOf("k1")
	if info.Conflicts != 1 {
		t.Fatalf("debounced count = %d, want 1", info.Conflicts)
	}

	am.useOnce.Store("loss:k1", time.Now().Add(-usageDebounce-time.Minute))
	am.RecordConflictLoss([]string{"k1"})
	info, _ = am.UsageOf("k1")
	if info.Conflicts != 2 {
		t.Fatalf("post-window count = %d, want 2", info.Conflicts)
	}

	am2 := &AutoMemory{dir: am.dir}
	reloaded, ok := am2.UsageOf("k1")
	if !ok || reloaded.Conflicts != 2 {
		t.Fatalf("sidecar reload = ok:%v %+v, want 2 conflicts", ok, reloaded)
	}
}

func TestSA146_TrustPenaltyMonotonicCapped(t *testing.T) {
	now := time.Now()
	mk := func(c int) MemoryMeta {
		return MemoryMeta{Category: CategoryPersistent, CreatedAt: now, Conflicts: c}
	}
	s0 := memoryTrustScore(mk(0), now)
	s1 := memoryTrustScore(mk(1), now)
	s3 := memoryTrustScore(mk(3), now)
	s5 := memoryTrustScore(mk(5), now)
	if !(s1 < s0) {
		t.Fatalf("1 conflict must lower trust: %v -> %v", s0, s1)
	}
	if !(s3 < s1) {
		t.Fatalf("3 conflicts must lower trust below 1: %v -> %v", s1, s3)
	}
	if s5 != s3 {
		t.Fatalf("penalty must cap at %d losses: %v vs %v", trustConflictPenaltyCap, s3, s5)
	}
}

func TestSA146_HighConflictEvictedBeforeNeverUsed(t *testing.T) {
	now := time.Now()
	active := []MemoryMeta{
		{Key: "never-used", Category: CategoryDefault, CreatedAt: now.Add(-2 * 24 * time.Hour)},
		{Key: "loser", Category: CategoryDefault, CreatedAt: now.Add(-3 * 24 * time.Hour), Uses: 4, Conflicts: 2},
		{Key: "used-clean", Category: CategoryDefault, CreatedAt: now.Add(-4 * 24 * time.Hour), Uses: 9},
	}
	kept, evicted := capByCountSplit(active, 2)
	if len(evicted) != 1 || evicted[0].Key != "loser" {
		t.Fatalf("high-conflict loser must be evicted first, evicted=%+v", evicted)
	}
	if len(kept) != 2 {
		t.Fatalf("kept = %d, want 2", len(kept))
	}
}
