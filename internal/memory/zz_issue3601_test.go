package memory

// #3601 probe: the consume-rate clamp window is [budgetFloorRatio, 1.0].
// Consumed and Uses are recorded under independent debounce keys, so the
// ratio can exceed 1.0 (e.g. consumption counted per-injection while uses
// lag); the old code only clamped the floor, letting the inline budget
// silently inflate past maxTotalInlineBytes - contradicting the documented
// clamp. The ceiling must hold.

import "testing"

func TestIssue3601_RateAboveOneCapsAtCeiling(t *testing.T) {
	dir := t.TempDir()
	am := &AutoMemory{dir: dir}
	// Consumed=40 over Uses=8: rate = 5.0, far past the 1.0 ceiling.
	forceUses(t, am, "k-impl", 8)
	forceConsumption(t, am, "k-impl", 40)

	if got := am.EffectiveInlineBudget(); got != maxTotalInlineBytes {
		t.Fatalf("rate>1.0 must cap the budget at the ceiling %d, got %d", maxTotalInlineBytes, got)
	}
}

func TestIssue3601_MidWindowRateStillScales(t *testing.T) {
	dir := t.TempDir()
	am := &AutoMemory{dir: dir}
	// rate = 6 consumed / 8 uses = 0.75: scaling must still work between
	// floor and ceiling (the clamp must not collapse to a constant).
	forceUses(t, am, "m-impl", 8)
	forceConsumption(t, am, "m-impl", 6)

	want := int(float64(maxTotalInlineBytes) * 0.75)
	if got := am.EffectiveInlineBudget(); got != want {
		t.Fatalf("mid-window rate must scale: want %d, got %d", want, got)
	}
}
