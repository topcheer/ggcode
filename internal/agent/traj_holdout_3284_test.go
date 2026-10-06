package agent

import (
	"testing"
	"time"
)

// #3284 regression probes. Both defects share one root cause: the
// category-single-row ledger meant any row not appended by the current
// select was deleted by the full rewrite.
//
//  1. released-key suppression window died on the FIRST subsequent select
//     (pause path dropped the row; the next rotation re-claimed blind).
//  2. control-arm counters reset on every rotation day, so sparse
//     workspaces never accumulated HoldoutRuns >= min runs.

func find3284Row(t *testing.T, dir string, key string) trajHoldoutEntry {
	t.Helper()
	ledger, _ := loadHoldoutLedger(trajHoldoutPath(dir))
	for _, e := range ledger {
		if e.InsightKey == key {
			return e
		}
	}
	t.Fatalf("row %q missing from ledger: %+v", key, ledger)
	return trajHoldoutEntry{}
}

func ledger3284Size(t *testing.T, dir string) int {
	t.Helper()
	ledger, _ := loadHoldoutLedger(trajHoldoutPath(dir))
	return len(ledger)
}

// Probe 1 (issue timing): day100 verdict releases K1; day101 slot rotates
// to K2 (K1's released row must SURVIVE that rewrite); day103 slot rotates
// back to K1 - still inside the window, so no blind re-claim, and the row
// still carries its original ReleasedUntil.
func TestHoldout3284_ReleasedRowSurvivesRotation(t *testing.T) {
	dir := t.TempDir()
	entries := []trajectoryLearning{
		hold3275Learning("build", "pattern", 0.8, 3),
		hold3275Learning("build", "failure", 0.8, 3),
		hold3275Learning("build", "hint", 0.8, 3),
	}
	trajHoldoutDay = func() int64 { return 100 }
	t.Cleanup(func() { trajHoldoutDay = func() int64 { return time.Now().UTC().Unix() / 86400 } })

	held := trajHoldoutSelect(dir, entries)
	if len(held) != 1 {
		t.Fatalf("one of three candidates must be held, got %v", held)
	}
	var k1 trajKey
	for k := range held {
		k1 = k
	}
	k1s := trajHoldoutKeyString(k1)

	// Verdict release: row persists with suppression window.
	hp := trajHoldoutPath(dir)
	ledger, _ := loadHoldoutLedger(hp)
	for i := range ledger {
		ledger[i].ReleasedUntil = time.Now().UTC().Add(trajHoldoutReleaseWindow)
	}
	if err := writeHoldoutLedger(hp, ledger); err != nil {
		t.Fatal(err)
	}

	// day101: slot lands on a different key - K2 is claimed, K1's released
	// row must survive the rewrite (pre-fix: silently deleted).
	trajHoldoutDay = func() int64 { return 101 }
	held101 := trajHoldoutSelect(dir, entries)
	if len(held101) != 1 || held101[k1] {
		t.Fatalf("day101 must hold a different key, got %v", held101)
	}
	row := find3284Row(t, dir, k1s)
	if row.ReleasedUntil.IsZero() || !row.ReleasedUntil.After(time.Now().UTC()) {
		t.Fatalf("released row must survive day101 rotation with window intact: %+v", row)
	}

	// day103: slot rotates back to K1 - inside the window, so the category
	// pauses (nothing held from it) and K1 is not blindly re-claimed with
	// fresh counters.
	trajHoldoutDay = func() int64 { return 103 }
	held103 := trajHoldoutSelect(dir, entries)
	if held103[k1] {
		t.Fatal("released key must not be re-claimed inside its window")
	}
	row = find3284Row(t, dir, k1s)
	if row.HoldoutRuns != 0 || row.ReleasedUntil.IsZero() {
		t.Fatalf("blind re-claim detected (counters reset / window cleared): %+v", row)
	}
}

// Probe 2: repeated same-day selects after a release - the second select
// hits the pause path; the third must still see the row (pre-fix: second
// select deleted the row, third re-claimed blind).
func TestHoldout3284_PauseThenThirdSelect(t *testing.T) {
	dir := t.TempDir()
	entries := []trajectoryLearning{
		hold3275Learning("build", "pattern", 0.8, 3),
		hold3275Learning("build", "failure", 0.8, 3),
		hold3275Learning("build", "hint", 0.8, 3),
	}
	trajHoldoutDay = func() int64 { return 100 }
	t.Cleanup(func() { trajHoldoutDay = func() int64 { return time.Now().UTC().Unix() / 86400 } })

	held := trajHoldoutSelect(dir, entries)
	var k1 trajKey
	for k := range held {
		k1 = k
	}

	hp := trajHoldoutPath(dir)
	ledger, _ := loadHoldoutLedger(hp)
	for i := range ledger {
		ledger[i].ReleasedUntil = time.Now().UTC().Add(trajHoldoutReleaseWindow)
	}
	if err := writeHoldoutLedger(hp, ledger); err != nil {
		t.Fatal(err)
	}

	// Second select, same day: pause path - row must survive.
	if held2 := trajHoldoutSelect(dir, entries); held2[k1] {
		t.Fatalf("released key must stay suppressed, got %v", held2)
	}
	if ledger3284Size(t, dir) == 0 {
		t.Fatal("pause path deleted the released row (defect 1)")
	}
	// Third select, same day: still suppressed, still present.
	if held3 := trajHoldoutSelect(dir, entries); held3[k1] {
		t.Fatalf("third select re-claimed released key, got %v", held3)
	}
	find3284Row(t, dir, trajHoldoutKeyString(k1))
}

// Probe 3: cross-day counter accumulation through the SELECT path. K2 is
// held on day1 with existing counters; day2 rotates to K1 - K2's row (and
// its counters) must survive; day3 rotates back to K2 and the streak
// continues with preserved HoldoutRuns (pre-fix: every rotation day reset
// the counters, so sparse workspaces never reached min runs).
func TestHoldout3284_CrossDayCounterAccumulation(t *testing.T) {
	dir := t.TempDir()
	entries := []trajectoryLearning{
		hold3275Learning("build", "pattern", 0.8, 3),
		hold3275Learning("build", "failure", 0.8, 3),
	}
	trajHoldoutDay = func() int64 { return 200 }
	t.Cleanup(func() { trajHoldoutDay = func() int64 { return time.Now().UTC().Unix() / 86400 } })

	held1 := trajHoldoutSelect(dir, entries)
	if len(held1) != 1 {
		t.Fatalf("one of two candidates must be held, got %v", held1)
	}
	var k1, k2 trajKey
	for k := range held1 {
		k1 = k
	}
	for _, l := range entries {
		k := trajKeyOf(l)
		if k != k1 {
			k2 = k
		}
	}

	// Give the held key some accumulated runs, then rotate away (day201).
	hp := trajHoldoutPath(dir)
	ledger, _ := loadHoldoutLedger(hp)
	for i := range ledger {
		if ledger[i].InsightKey == trajHoldoutKeyString(k1) {
			ledger[i].HoldoutRuns = 3
			ledger[i].HoldoutSuccesses = 3
		}
	}
	if err := writeHoldoutLedger(hp, ledger); err != nil {
		t.Fatal(err)
	}
	trajHoldoutDay = func() int64 { return 201 }
	held2 := trajHoldoutSelect(dir, entries)
	if len(held2) != 1 || !held2[k2] {
		t.Fatalf("day201 must rotate to the other key, got %v", held2)
	}
	// K1's row with its counters must survive the rotation.
	row := find3284Row(t, dir, trajHoldoutKeyString(k1))
	if row.HoldoutRuns != 3 {
		t.Fatalf("rotation must not reset counters (defect 2): %+v", row)
	}
	// day202 rotates back to K1: streak continues with preserved counters.
	trajHoldoutDay = func() int64 { return 202 }
	held3 := trajHoldoutSelect(dir, entries)
	if len(held3) != 1 || !held3[k1] {
		t.Fatalf("day202 must rotate back to K1, got %v", held3)
	}
	row = find3284Row(t, dir, trajHoldoutKeyString(k1))
	if row.HoldoutRuns != 3 {
		t.Fatalf("re-claim must preserve accumulated runs, got %+v", row)
	}
}
