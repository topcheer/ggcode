package agent

import (
	"testing"
	"time"
)

func TestHoldout3300_LoneEligibleKeepsReleasedSibling(t *testing.T) {
	dir := t.TempDir()

	entries := []trajectoryLearning{
		hold3275Learning("build", "pattern", 0.8, 3),
		holdoutLearning("build", "failure", 0.8),
	}
	trajHoldoutDay = func() int64 { return 100 }
	t.Cleanup(func() { trajHoldoutDay = func() int64 { return time.Now().UTC().Unix() / 86400 } })

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

func TestHoldout3300_GatedCategoryRowsSurvive(t *testing.T) {
	dir := t.TempDir()

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

func TestHoldout3300_PausePathKeepsSiblings(t *testing.T) {
	dir := t.TempDir()
	entries := []trajectoryLearning{
		hold3275Learning("build", "pattern", 0.8, 3),
		hold3275Learning("build", "failure", 0.8, 3),
	}
	trajHoldoutDay = func() int64 { return 100 }
	t.Cleanup(func() { trajHoldoutDay = func() int64 { return time.Now().UTC().Unix() / 86400 } })

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

func TestHoldout3300_ReleasedRowNotReverdicted(t *testing.T) {
	dir := t.TempDir()

	entries := []trajectoryLearning{
		hold3275Learning("build", "pattern", 0.9, 3),
		hold3275Learning("build", "failure", 0.9, 3),
	}
	s := holdoutTestState(t, dir, entries...)

	seedLedgerWithRuns(t, dir, "build", "pattern", 6, 0)
	led, _ := loadHoldoutLedger(trajHoldoutPath(dir))
	rel := led[0].ReleasedUntil
	_ = rel
	window := time.Now().UTC().Add(trajHoldoutReleaseWindow)
	led[0].ReleasedUntil = window
	if err := writeHoldoutLedger(trajHoldoutPath(dir), led); err != nil {
		t.Fatal(err)
	}

	s.recordHoldoutLocked(holdoutLearning("build", "failure", 0.9))
	s.recordHoldoutOutcome(dir, false)

	row := find3284Row(t, dir, led[0].InsightKey)
	if row.ReleasedUntil.After(window.Add(time.Second)) {
		t.Fatalf("released row re-verdicted inside its window: ReleasedUntil pushed to %v (original %v)", row.ReleasedUntil, window)
	}
	if row.HoldoutRuns != 6 {
		t.Fatalf("released row counters must not grow while unclaimed: %+v", row)
	}
}
