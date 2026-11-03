package agent

import (
	"testing"
	"time"
)

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

	hp := trajHoldoutPath(dir)
	ledger, _ := loadHoldoutLedger(hp)
	for i := range ledger {
		ledger[i].ReleasedUntil = time.Now().UTC().Add(trajHoldoutReleaseWindow)
	}
	if err := writeHoldoutLedger(hp, ledger); err != nil {
		t.Fatal(err)
	}

	trajHoldoutDay = func() int64 { return 101 }
	held101 := trajHoldoutSelect(dir, entries)
	if len(held101) != 1 || held101[k1] {
		t.Fatalf("day101 must hold a different key, got %v", held101)
	}
	row := find3284Row(t, dir, k1s)
	if row.ReleasedUntil.IsZero() || !row.ReleasedUntil.After(time.Now().UTC()) {
		t.Fatalf("released row must survive day101 rotation with window intact: %+v", row)
	}

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

	if held2 := trajHoldoutSelect(dir, entries); held2[k1] {
		t.Fatalf("released key must stay suppressed, got %v", held2)
	}
	if ledger3284Size(t, dir) == 0 {
		t.Fatal("pause path deleted the released row (defect 1)")
	}

	if held3 := trajHoldoutSelect(dir, entries); held3[k1] {
		t.Fatalf("third select re-claimed released key, got %v", held3)
	}
	find3284Row(t, dir, trajHoldoutKeyString(k1))
}

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

	row := find3284Row(t, dir, trajHoldoutKeyString(k1))
	if row.HoldoutRuns != 3 {
		t.Fatalf("rotation must not reset counters (defect 2): %+v", row)
	}

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
