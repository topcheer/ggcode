package agent

import (
	"testing"
	"time"
)

// #3300 regression probes. Two residual defects in the #3284/#3275 fix
// surface, confirmed by independent deep review:
//
//	G: trajHoldoutSelect drops sibling rows on three paths (category body
//	   skipped, pause path, category fallen out of byCat) because `out` is
//	   a full ledger rewrite - sibling counters reset and release windows
//	   are voided (blind re-claim on the next rotation).
//	B: recordHoldoutOutcome re-verdicts rows still inside their release
//	   window: one decay step per run (not per verdict) and the window is
//	   pushed to now+7d on every pass, force-retiring the key to the
//	   confidence floor within a week.

// G probe 1: category with exactly one injected candidate takes the
// len(eligible) <= 1 skip path - a sibling row that is inside its release
// window must survive the rewrite (#3284 regression on this path).
func TestHoldout3300_LoneEligibleKeepsReleasedSibling(t *testing.T) {
	dir := t.TempDir()
	// Category "build" has two keys above the confidence gate but only one
	// with injection-arm samples -> eligible == 1 -> whole category body
	// skipped.
	entries := []trajectoryLearning{
		hold3275Learning("build", "pattern", 0.8, 3),
		holdoutLearning("build", "failure", 0.8), // InjectedRuns == 0
	}
	trajHoldoutDay = func() int64 { return 100 }
	t.Cleanup(func() { trajHoldoutDay = func() int64 { return time.Now().UTC().Unix() / 86400 } })

	// Pre-seed the ledger with a RELEASED sibling row in the same category.
	seedLedgerWithRuns(t, dir, "build", "failure", 5, 1)
	led, _ := loadHoldoutLedger(trajHoldoutPath(dir))
	led[0].ReleasedUntil = time.Now().UTC().Add(trajHoldoutReleaseWindow)
	if err := writeHoldoutLedger(trajHoldoutPath(dir), led); err != nil {
		t.Fatal(err)
	}

	held := trajHoldoutSelect(dir, entries)
	if len(held) != 0 {
		t.Fatalf("lone-eligible category must hold nothing today, got %v", held)
	}
	row := find3284Row(t, dir, led[0].InsightKey)
	if !row.ReleasedUntil.After(time.Now()) {
		t.Fatalf("released sibling row lost its window via the lone-eligible skip path: %+v", row)
	}
	if row.HoldoutRuns != 5 || row.HoldoutSuccesses != 1 {
		t.Fatalf("released sibling counters reset via the skip path: %+v", row)
	}
}

// G probe 2: category whose keys all fell below the confidence gate never
// enters byCat - its ledger rows (including an in-window released row)
// must still survive the full rewrite.
func TestHoldout3300_GatedCategoryRowsSurvive(t *testing.T) {
	dir := t.TempDir()
	// Every entry below trajPromptMinConfidence: byCat stays empty.
	entries := []trajectoryLearning{
		holdoutLearning("build", "pattern", 0.1),
	}
	trajHoldoutDay = func() int64 { return 100 }
	t.Cleanup(func() { trajHoldoutDay = func() int64 { return time.Now().UTC().Unix() / 86400 } })

	seedLedgerWithRuns(t, dir, "build", "pattern", 5, 2)
	led, _ := loadHoldoutLedger(trajHoldoutPath(dir))
	led[0].ReleasedUntil = time.Now().UTC().Add(trajHoldoutReleaseWindow)
	if err := writeHoldoutLedger(trajHoldoutPath(dir), led); err != nil {
		t.Fatal(err)
	}

	held := trajHoldoutSelect(dir, entries)
	if len(held) != 0 {
		t.Fatalf("nothing can be held when the gate excludes all keys, got %v", held)
	}
	if got := ledger3284Size(t, dir); got != 1 {
		t.Fatalf("gated category row was deleted by the rewrite: ledger size %d", got)
	}
	row := find3284Row(t, dir, led[0].InsightKey)
	if !row.ReleasedUntil.After(time.Now()) || row.HoldoutRuns != 5 {
		t.Fatalf("gated category row must keep window+counters: %+v", row)
	}
}

// G probe 3: the pause path (target row in its release window) must also
// keep the OTHER sibling rows of that category - #3284's preservation
// loop sits after the pause `continue`, so siblings were dropped there.
func TestHoldout3300_PausePathKeepsSiblings(t *testing.T) {
	dir := t.TempDir()
	entries := []trajectoryLearning{
		hold3275Learning("build", "pattern", 0.8, 3),
		hold3275Learning("build", "failure", 0.8, 3),
	}
	trajHoldoutDay = func() int64 { return 100 }
	t.Cleanup(func() { trajHoldoutDay = func() int64 { return time.Now().UTC().Unix() / 86400 } })

	// Sort order puts "failure" at slot 0 -> today's target (day 100 % 2 == 0).
	// Give the TARGET row a release window (pause path) and the "pattern"
	// sibling its own counters.
	targetKey := trajHoldoutKeyString(trajKey{cat: "build", typ: "failure"})
	siblingKey := trajHoldoutKeyString(trajKey{cat: "build", typ: "pattern"})
	led := []trajHoldoutEntry{
		{
			InsightKey: targetKey, Category: "build", Type: "failure",
			HoldoutRuns: 5, HoldoutSuccesses: 4, HeldSince: time.Now().UTC(),
			ReleasedUntil: time.Now().UTC().Add(trajHoldoutReleaseWindow),
		},
		{
			InsightKey: siblingKey, Category: "build", Type: "pattern",
			HoldoutRuns: 4, HoldoutSuccesses: 3, HeldSince: time.Now().UTC(),
		},
	}
	if err := writeHoldoutLedger(trajHoldoutPath(dir), led); err != nil {
		t.Fatal(err)
	}

	held := trajHoldoutSelect(dir, entries)
	if len(held) != 0 {
		t.Fatalf("category inside the release window must pause holdout, got %v", held)
	}
	row := find3284Row(t, dir, siblingKey)
	if row.HoldoutRuns != 4 || row.HoldoutSuccesses != 3 {
		t.Fatalf("sibling row lost via the pause path rewrite: %+v", row)
	}
	tRow := find3284Row(t, dir, targetKey)
	if !tRow.ReleasedUntil.After(time.Now()) || tRow.HoldoutRuns != 5 {
		t.Fatalf("paused target row must keep window+counters: %+v", tRow)
	}
}

// B probe: a released, mature row must NOT be re-verdicted while its
// window is open. Repeated recordHoldoutOutcome calls (one per run, each
// holding a DIFFERENT key) used to push ReleasedUntil to now+7d every
// time - the window never expired and the key was decayed to the floor.
func TestHoldout3300_ReleasedRowNotReverdicted(t *testing.T) {
	dir := t.TempDir()
	// Two keys in category "build" so selects can hold the OTHER one.
	entries := []trajectoryLearning{
		hold3275Learning("build", "pattern", 0.9, 3),
		hold3275Learning("build", "failure", 0.9, 3),
	}
	s := holdoutTestState(t, dir, entries...)

	// Seed a released, mature row for "pattern" whose verdict would decay
	// it again (holdout arm 0/6 vs injection arm 3/3 => delta <= 0).
	seedLedgerWithRuns(t, dir, "build", "pattern", 6, 0)
	led, _ := loadHoldoutLedger(trajHoldoutPath(dir))
	rel := led[0].ReleasedUntil // zero - set below
	_ = rel
	window := time.Now().UTC().Add(trajHoldoutReleaseWindow)
	led[0].ReleasedUntil = window
	if err := writeHoldoutLedger(trajHoldoutPath(dir), led); err != nil {
		t.Fatal(err)
	}

	// A run holds the sibling key ("failure") - the released "pattern" row
	// is not claimed, but recordHoldoutOutcome still walks the whole ledger.
	s.recordHoldoutLocked(holdoutLearning("build", "failure", 0.9))
	s.recordHoldoutOutcome(dir, false)

	// The released row must keep its ORIGINAL window, not a pushed-out one.
	row := find3284Row(t, dir, led[0].InsightKey)
	if row.ReleasedUntil.After(window.Add(time.Second)) {
		t.Fatalf("released row re-verdicted inside its window: ReleasedUntil pushed to %v (original %v)", row.ReleasedUntil, window)
	}
	if row.HoldoutRuns != 6 {
		t.Fatalf("released row counters must not grow while unclaimed: %+v", row)
	}
}
