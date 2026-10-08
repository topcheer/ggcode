package memory

// sa-147 (LIMBO, arXiv:2609.14138 inference-time memory allocation) probes:
// memory injection must be a controllable inference-time resource, not a
// fixed policy. The consumption loop: ScanConsumption matches injected
// entries' keys / first-line fingerprints in the assistant corpus ->
// RecordConsumption persists Consumed -> EffectiveInlineBudget scales the
// total inline budget by the store-wide Consumed/Uses ratio, clamped to
// [budgetFloorRatio, 1.0], fail-open when unmeasured.

import (
	"strings"
	"testing"
	"time"
)

// forceUses records n uses of key bypassing the debounce via useOnce
// backdating (same technique as the sa-146 probes).
func forceUses(t *testing.T, am *AutoMemory, key string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		// RecordUse debounces on the BARE key (usage.go:332), unlike the
		// "loss:"/"consum:" namespaced writers.
		am.useOnce.Store(key, time.Now().Add(-time.Duration(i+1)*usageDebounce*2))
		am.RecordUse([]string{key}, "inline")
	}
}

// forceConsumption records n consumptions of key bypassing the debounce.
func forceConsumption(t *testing.T, am *AutoMemory, key string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		am.useOnce.Store("consum:"+key, time.Now().Add(-time.Duration(i+1)*usageDebounce*2))
		am.RecordConsumption([]string{key})
	}
}

// P1: ScanConsumption is selective (only cited key/fingerprint consumed)
// and debounced (a second scan inside the window does not double-count).
func TestSA147_ScanConsumptionSelectiveAndDebounced(t *testing.T) {
	dir := t.TempDir()
	am := &AutoMemory{dir: dir}
	writeMem(t, dir, "alpha-notes", "alpha first line about release process")
	writeMem(t, dir, "beta-notes", "beta first line about build process")
	forceUses(t, am, "alpha-notes", 2)
	forceUses(t, am, "beta-notes", 2)

	// Corpus quotes beta's key verbatim and alpha's fingerprint, but not
	// any third entry (none exists).
	corpus := "as beta-notes says, plus remember: alpha first line about release process"
	if got := am.ScanConsumption(corpus); got != 2 {
		t.Fatalf("expected 2 matched keys, got %d", got)
	}
	a, _ := am.UsageOf("alpha-notes")
	b, _ := am.UsageOf("beta-notes")
	if a.Consumed != 1 || b.Consumed != 1 {
		t.Fatalf("expected Consumed=1 on both, got alpha=%d beta=%d", a.Consumed, b.Consumed)
	}
	// Second scan inside the debounce window: no double counting.
	am.ScanConsumption(corpus)
	a2, _ := am.UsageOf("alpha-notes")
	if a2.Consumed != 1 {
		t.Fatalf("debounce violated: alpha Consumed=%d", a2.Consumed)
	}
}

// P2: a low-consume store (2 consumed / 8+ uses) converges to the floor
// budget, and the floor actually demotes overflow entries to index-only.
func TestSA147_BudgetFloorTightensInjection(t *testing.T) {
	dir := t.TempDir()
	am := &AutoMemory{dir: dir}
	writeMem(t, dir, "big-aaa-impl", strings.Repeat("a", 800))
	writeMem(t, dir, "big-bbb-impl", strings.Repeat("b", 800))
	forceUses(t, am, "big-aaa-impl", 8)
	forceUses(t, am, "big-bbb-impl", 8)
	forceConsumption(t, am, "big-aaa-impl", 2) // rate = 2/16 = 0.125 -> floor 0.25

	if got := am.EffectiveInlineBudget(); got != int(maxTotalInlineBytes*budgetFloorRatio) {
		t.Fatalf("expected floor budget %d, got %d", int(maxTotalInlineBytes*budgetFloorRatio), got)
	}
	inline, indexOnly, err := am.LoadForPrompt()
	if err != nil {
		t.Fatal(err)
	}
	// 800+800 = 1600 > 1500: the second entry must be demoted to index-only.
	if len(inline) != 1 || len(indexOnly) != 1 {
		t.Fatalf("floor budget must demote overflow: inline=%d indexOnly=%d", len(inline), len(indexOnly))
	}
}

// P3: fail-open arms - fresh unmeasured store and a high-consume store
// both keep the full budget (legacy behavior byte-identical until
// evidence accumulates; high yield never exceeds the ceiling).
func TestSA147_BudgetFailOpenAndCeiling(t *testing.T) {
	// Fresh store: no sidecar at all.
	dir := t.TempDir()
	am := &AutoMemory{dir: dir}
	if got := am.EffectiveInlineBudget(); got != maxTotalInlineBytes {
		t.Fatalf("fresh store must keep full budget, got %d", got)
	}

	// High-consume: 4/4 -> rate 1.0 -> ceiling.
	forceUses(t, am, "k", 4)
	writeMem(t, am.dir, "k", strings.Repeat("x", 40))
	forceConsumption(t, am, "k", 4)
	if got := am.EffectiveInlineBudget(); got != maxTotalInlineBytes {
		t.Fatalf("high-consume store must keep full budget, got %d", got)
	}

	// Measured but tiny sample (1 consumption): still fail-open.
	dir2 := t.TempDir()
	am2 := &AutoMemory{dir: dir2}
	forceUses(t, am2, "k", 9)
	forceConsumption(t, am2, "k", 1)
	if got := am2.EffectiveInlineBudget(); got != maxTotalInlineBytes {
		t.Fatalf("below minConsumedSamples must stay full budget, got %d", got)
	}
}

// P4: consumption signal persists across instances (a second AutoMemory
// over the same dir sees the same Consumed and the same adapted budget).
func TestSA147_ConsumptionPersistsAcrossInstances(t *testing.T) {
	dir := t.TempDir()
	am := &AutoMemory{dir: dir}
	writeMem(t, dir, "persist-key", "persist first line fingerprint here")
	forceUses(t, am, "persist-key", 8)
	am.ScanConsumption("quoted: persist-key indeed")

	am2 := &AutoMemory{dir: dir}
	rec, ok := am2.UsageOf("persist-key")
	if !ok || rec.Consumed != 1 {
		t.Fatalf("reloaded instance must see Consumed=1, got ok=%v %+v", ok, rec)
	}
	if am2.EffectiveInlineBudget() != am.EffectiveInlineBudget() {
		t.Fatalf("budget must be a store property, got %d vs %d", am2.EffectiveInlineBudget(), am.EffectiveInlineBudget())
	}
}
