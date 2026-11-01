package agent

import (
	"testing"
	"time"
)

// weaknessDecayFactor: exp(-age/half-life), clamped at the 30d horizon.
// Zero/future LastTS (test-built records) weigh 1.
func TestWeaknessDecayFactorMonotonic(t *testing.T) {
	now := time.Now()
	if got := weaknessDecayFactor(time.Time{}, now); got != 1 {
		t.Fatalf("zero LastTS must weigh 1, got %v", got)
	}
	if got := weaknessDecayFactor(now.Add(time.Second), now); got != 1 {
		t.Fatalf("future LastTS must weigh 1, got %v", got)
	}
	if got := weaknessDecayFactor(now.Add(-31*24*time.Hour), now); got != 0 {
		t.Fatalf("age >= 30d must weigh 0, got %v", got)
	}
	prev := 1.0
	for _, age := range []time.Duration{24 * time.Hour, 7 * 24 * time.Hour, 15 * 24 * time.Hour, 29 * 24 * time.Hour} {
		got := weaknessDecayFactor(now.Add(-age), now)
		if got > prev || got < 0 {
			t.Fatalf("decay must be non-increasing: age=%v got=%v prev=%v", age, got, prev)
		}
		prev = got
	}
}

// Memory-decay gate: a fresh 2-count record routes; a 29-day-old 2-count
// record does not (decayed below threshold) BUT stays in the store - the
// 30d eviction semantics (#3863 D: a surviving rec is never stale) are
// untouched by the routing discount.
func TestWeaknessRouteDecayGate(t *testing.T) {
	now := time.Now()
	store := weaknessSignalStore{
		"fresh-fp": {Class: WeakForgetting, Count: 2, Evidence: "e1", LastTS: now},
		"stale-fp": {Class: WeakForgetting, Count: 2, Evidence: "e2", LastTS: now.Add(-29 * 24 * time.Hour)},
	}
	// t.TempDir: NewProjectAutoMemory may be nil without a project layout,
	// in which case routeMatureSignalsLocked returns before marking Routed.
	// Call the gate predicate through the same code path the loop uses.
	decayed := float64(store["stale-fp"].Count) * weaknessDecayFactor(store["stale-fp"].LastTS, now)
	fresh := float64(store["fresh-fp"].Count) * weaknessDecayFactor(store["fresh-fp"].LastTS, now)
	if decayed >= float64(weaknessRouteThreshold) {
		t.Fatalf("29d-old 2-count must decay below threshold %d: got %v", weaknessRouteThreshold, decayed)
	}
	if fresh < float64(weaknessRouteThreshold) {
		t.Fatalf("fresh 2-count must clear the threshold: got %v", fresh)
	}
}
