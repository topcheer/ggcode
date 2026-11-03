package memory

import (
	"strings"
	"testing"
	"time"
)

func forceUses(t *testing.T, am *AutoMemory, key string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {

		am.useOnce.Store(key, time.Now().Add(-time.Duration(i+1)*usageDebounce*2))
		am.RecordUse([]string{key}, "inline")
	}
}

func forceConsumption(t *testing.T, am *AutoMemory, key string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		am.useOnce.Store("consum:"+key, time.Now().Add(-time.Duration(i+1)*usageDebounce*2))
		am.RecordConsumption([]string{key})
	}
}

func TestSA147_ScanConsumptionSelectiveAndDebounced(t *testing.T) {
	dir := t.TempDir()
	am := &AutoMemory{dir: dir}
	writeMem(t, dir, "alpha-notes", "alpha first line about release process")
	writeMem(t, dir, "beta-notes", "beta first line about build process")
	forceUses(t, am, "alpha-notes", 2)
	forceUses(t, am, "beta-notes", 2)

	corpus := "as beta-notes says, plus remember: alpha first line about release process"
	if got := am.ScanConsumption(corpus); got != 2 {
		t.Fatalf("expected 2 matched keys, got %d", got)
	}
	a, _ := am.UsageOf("alpha-notes")
	b, _ := am.UsageOf("beta-notes")
	if a.Consumed != 1 || b.Consumed != 1 {
		t.Fatalf("expected Consumed=1 on both, got alpha=%d beta=%d", a.Consumed, b.Consumed)
	}

	am.ScanConsumption(corpus)
	a2, _ := am.UsageOf("alpha-notes")
	if a2.Consumed != 1 {
		t.Fatalf("debounce violated: alpha Consumed=%d", a2.Consumed)
	}
}

func TestSA147_BudgetFloorTightensInjection(t *testing.T) {
	dir := t.TempDir()
	am := &AutoMemory{dir: dir}
	writeMem(t, dir, "big-aaa-impl", strings.Repeat("a", 800))
	writeMem(t, dir, "big-bbb-impl", strings.Repeat("b", 800))
	forceUses(t, am, "big-aaa-impl", 8)
	forceUses(t, am, "big-bbb-impl", 8)
	forceConsumption(t, am, "big-aaa-impl", 2)

	if got := am.EffectiveInlineBudget(); got != int(maxTotalInlineBytes*budgetFloorRatio) {
		t.Fatalf("expected floor budget %d, got %d", int(maxTotalInlineBytes*budgetFloorRatio), got)
	}
	inline, indexOnly, err := am.LoadForPrompt()
	if err != nil {
		t.Fatal(err)
	}

	if len(inline) != 1 || len(indexOnly) != 1 {
		t.Fatalf("floor budget must demote overflow: inline=%d indexOnly=%d", len(inline), len(indexOnly))
	}
}

func TestSA147_BudgetFailOpenAndCeiling(t *testing.T) {

	dir := t.TempDir()
	am := &AutoMemory{dir: dir}
	if got := am.EffectiveInlineBudget(); got != maxTotalInlineBytes {
		t.Fatalf("fresh store must keep full budget, got %d", got)
	}

	forceUses(t, am, "k", 4)
	writeMem(t, am.dir, "k", strings.Repeat("x", 40))
	forceConsumption(t, am, "k", 4)
	if got := am.EffectiveInlineBudget(); got != maxTotalInlineBytes {
		t.Fatalf("high-consume store must keep full budget, got %d", got)
	}

	dir2 := t.TempDir()
	am2 := &AutoMemory{dir: dir2}
	forceUses(t, am2, "k", 9)
	forceConsumption(t, am2, "k", 1)
	if got := am2.EffectiveInlineBudget(); got != maxTotalInlineBytes {
		t.Fatalf("below minConsumedSamples must stay full budget, got %d", got)
	}
}

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
