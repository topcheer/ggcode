package memory

import "testing"

func TestIssue3601_RateAboveOneCapsAtCeiling(t *testing.T) {
	dir := t.TempDir()
	am := &AutoMemory{dir: dir}

	forceUses(t, am, "k-impl", 8)
	forceConsumption(t, am, "k-impl", 40)

	if got := am.EffectiveInlineBudget(); got != maxTotalInlineBytes {
		t.Fatalf("rate>1.0 must cap the budget at the ceiling %d, got %d", maxTotalInlineBytes, got)
	}
}

func TestIssue3601_MidWindowRateStillScales(t *testing.T) {
	dir := t.TempDir()
	am := &AutoMemory{dir: dir}

	forceUses(t, am, "m-impl", 8)
	forceConsumption(t, am, "m-impl", 6)

	want := int(float64(maxTotalInlineBytes) * 0.75)
	if got := am.EffectiveInlineBudget(); got != want {
		t.Fatalf("mid-window rate must scale: want %d, got %d", want, got)
	}
}
