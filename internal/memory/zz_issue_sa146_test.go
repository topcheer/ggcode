package memory

// sa-146 (REALM, arXiv:2609.33226 retrieval-driven reconsolidation) probes:
// recall arbitration verdicts must not be use-and-forget. A HIGH-confidence
// loss (trust gap >= recallConflictLossGap) persists a Conflicts counter on
// the loser's sidecar record; the counter feeds cap-eviction order (below
// never-used) and arbitration trust (bounded penalty). Thin margins stay
// ephemeral annotations.

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// E2E: a high-confidence conflict during LoadForPrompt persists exactly one
// loss on the loser and nothing on the winner.
func TestSA146_LoadForPromptPersistsHighConfidenceLoss(t *testing.T) {
	dir := t.TempDir()
	am := &AutoMemory{dir: dir}
	writeMem(t, dir, "build-old-impl", "build command: go build ./...")
	writeMem(t, dir, "build-new-impl", "build command: go build -tags goolm ./...")
	// Winner: persistent + fresh (1.0+0.2+0.1). Loser: persistent + ancient
	// (1.0+0.2-0.3). Gap 0.4 >= 0.2 -> the loss must persist.
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

// Thin margin (tie trust, newer-wins tie-break only) must NOT persist a loss.
func TestSA146_ThinMarginLossNotPersisted(t *testing.T) {
	dir := t.TempDir()
	am := &AutoMemory{dir: dir}
	writeMem(t, dir, "cfg-a-impl", "build command: go build ./...")
	writeMem(t, dir, "cfg-b-impl", "build command: go build -tags goolm ./...")
	// Same category, same age: trust ties at 1.3, the newer modtime wins the
	// tie-break with margin 0 < recallConflictLossGap.

	if _, _, err := am.LoadForPrompt(); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"cfg-a-impl", "cfg-b-impl"} {
		if info, ok := am.UsageOf(k); ok && info.Conflicts != 0 {
			t.Fatalf("%s must not persist a thin-margin loss: %+v", k, info)
		}
	}
}

// RecordConflictLoss: debounced per key in the same window as RecordUse,
// accumulates after the window expires, and reloads from the sidecar in a
// fresh instance.
func TestSA146_RecordConflictLossDebounceAndReload(t *testing.T) {
	am := newTestAutoMemory(t)
	am.RecordConflictLoss([]string{"k1"})
	am.RecordConflictLoss([]string{"k1"}) // same debounce window: counted once
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

	am2 := &AutoMemory{dir: am.dir} // fresh instance, same sidecar
	reloaded, ok := am2.UsageOf("k1")
	if !ok || reloaded.Conflicts != 2 {
		t.Fatalf("sidecar reload = ok:%v %+v, want 2 conflicts", ok, reloaded)
	}
}

// Trust penalty: strictly monotonic in Conflicts, capped at 3 units.
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

// Cap eviction: a repeated high-confidence loser (Conflicts>=2) is evicted
// before a never-used clean entry, which is itself evicted before a used one.
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
