package agent

import (
	"testing"
	"time"
)

// #3275 regression probes. The r462-era fixtures seeded candidates without
// injection-arm history and single-candidate categories - exactly the shapes
// the fix removes from rotation - so these tests assert the NEW contract:
// never-injected insights and lone candidates are never claimed, and verdict
// releases persist as suppression windows instead of row deletion.

func hold3275Learning(cat, typ string, conf float64, injRuns int) trajectoryLearning {
	l := holdoutLearning(cat, typ, conf)
	// Full success on the injection arm: keeps the entry clear of the r461
	// effectivenessGated retirement (0% success + >= min samples gates).
	l.InjectedRuns = injRuns
	l.AfterSuccess = injRuns
	return l
}

// (a) never-injected insights must not be claimed - claiming one froze its
// InjectedRuns at 0 and deadlocked the verdict guard (>= 3 needed) forever.
func TestHoldout3275_NeverInjectedNotClaimed(t *testing.T) {
	dir := t.TempDir()
	entries := []trajectoryLearning{
		hold3275Learning("build", "pattern", 0.8, 0),
		hold3275Learning("build", "failure", 0.8, 0),
		hold3275Learning("build", "hint", 0.8, 0),
	}
	held := trajHoldoutSelect(dir, entries)
	if len(held) != 0 {
		t.Fatalf("never-injected insights must not be held, got %v", held)
	}
	ledger, _ := loadHoldoutLedger(trajHoldoutPath(dir))
	if len(ledger) != 0 {
		t.Fatalf("ledger should stay empty, got %d entries", len(ledger))
	}
}

// (b) a single-candidate category (the production norm: 1:1 category/type
// consolidation leaves exactly one key per category) is skipped - slot =
// day%1 == 0 claimed the sole insight forever with no counterfactual value.
func TestHoldout3275_SingleCandidateCategorySkipped(t *testing.T) {
	dir := t.TempDir()
	entries := []trajectoryLearning{
		hold3275Learning("strategy", "efficient-completion", 0.8, 4),
		hold3275Learning("optimization", "context-pressure", 0.8, 2),
	}
	held := trajHoldoutSelect(dir, entries)
	if len(held) != 0 {
		t.Fatalf("single-candidate categories must be skipped, got %v", held)
	}
	ledger, _ := loadHoldoutLedger(trajHoldoutPath(dir))
	if len(ledger) != 0 {
		t.Fatalf("no category was eligible; ledger must stay empty, got %d", len(ledger))
	}
}

// (c) a released key (suppression window) must not be re-claimed when its
// slot rotates back - the pre-fix deletion-based release was a no-op.
func TestHoldout3275_ReleasedNotReclaimed(t *testing.T) {
	dir := t.TempDir()
	entries := []trajectoryLearning{
		hold3275Learning("build", "pattern", 0.8, 3),
		hold3275Learning("build", "failure", 0.8, 3),
		hold3275Learning("build", "hint", 0.8, 3),
	}
	trajHoldoutDay = func() int64 { return 100 }
	t.Cleanup(func() { trajHoldoutDay = func() int64 { return time.Now().UTC().Unix() / 86400 } })

	held1 := trajHoldoutSelect(dir, entries)
	if len(held1) != 1 {
		t.Fatalf("three eligible candidates: exactly one must be held, got %v", held1)
	}
	var claimed trajKey
	for k := range held1 {
		claimed = k
	}

	// Simulate the verdict release exactly as recordHoldoutOutcome now does:
	// persist the row with a suppression window.
	hp := trajHoldoutPath(dir)
	ledger, _ := loadHoldoutLedger(hp)
	for i := range ledger {
		ledger[i].ReleasedUntil = time.Now().UTC().Add(trajHoldoutReleaseWindow)
	}
	if err := writeHoldoutLedger(hp, ledger); err != nil {
		t.Fatal(err)
	}

	// Same slot again (day pinned): the released key must not be re-claimed.
	held2 := trajHoldoutSelect(dir, entries)
	if held2[claimed] {
		t.Fatalf("released key re-claimed: %v", held2)
	}
	// Rotation to a different slot (day 101) resumes normally.
	trajHoldoutDay = func() int64 { return 101 }
	held3 := trajHoldoutSelect(dir, entries)
	if len(held3) != 1 || held3[claimed] {
		t.Fatalf("rotation must resume on a non-released key, got %v", held3)
	}
}

// Verdict release keeps the ledger row with a future suppression window.
func TestHoldout3275_VerdictReleasePersistsRow(t *testing.T) {
	dir := t.TempDir()
	// Injection arm 3/3 vs holdout arm 1/5 => delta 0.6 >= 0.10 => release.
	s := holdoutTestState(t, dir, hold3275Learning("build", "pattern", 0.9, 3))
	if err := s.rewriteAllLocked(func(existing []trajectoryLearning, _ error) ([]trajectoryLearning, error) {
		existing[0].InjectedRuns = 3
		existing[0].AfterSuccess = 3
		return existing, nil
	}); err != nil {
		t.Fatal(err)
	}
	seedLedgerWithRuns(t, dir, "build", "pattern", 5, 1)
	s.recordHoldoutLocked(holdoutLearning("build", "pattern", 0.9))
	s.recordHoldoutOutcome(dir, true)

	led, _ := loadHoldoutLedger(trajHoldoutPath(dir))
	if len(led) != 1 {
		t.Fatalf("released row must persist in ledger, got %d rows", len(led))
	}
	if !led[0].ReleasedUntil.After(time.Now()) {
		t.Fatalf("released row must carry a future suppression window: %+v", led[0])
	}
}

// End-to-end: two eligible candidates, one held; held entry's r461 counters
// stay at zero while the injected sibling accumulates (arm isolation holds
// under the new eligibility rules).
func TestHoldout3275_ArmIsolationUnderNewRules(t *testing.T) {
	dir := t.TempDir()
	entries := []trajectoryLearning{
		hold3275Learning("build", "pattern", 0.8, 3),
		hold3275Learning("build", "failure", 0.8, 3),
	}
	s := holdoutTestState(t, dir, entries...)
	trajHoldoutDay = func() int64 { return 100 }
	t.Cleanup(func() { trajHoldoutDay = func() int64 { return time.Now().UTC().Unix() / 86400 } })

	s.RenderPromptSection(dir)
	if len(s.holdoutThisRun) != 1 {
		t.Fatalf("exactly one candidate must be held, got %v", s.holdoutThisRun)
	}
	s.recordHoldoutOutcome(dir, true)
	ls, err := s.loadFromFile()
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range ls {
		if l.InjectedRuns != 3 { // unchanged from the seed value
			t.Fatalf("control arm must not touch injection counters: %+v", l)
		}
	}
}

// #3277: a cancelled run must clear BOTH per-run sets - a stale
// holdoutThisRun would inflate the next completed run's HoldoutRuns for
// keys that run actually injected (double-arm counting).
func TestHoldout3277_CancelClearsHoldoutSet(t *testing.T) {
	dir := t.TempDir()
	s := holdoutTestState(t, dir)
	s.recordHoldoutLocked(holdoutLearning("build", "pattern", 0.8))
	s.recordInjectedLocked(holdoutLearning("build", "failure", 0.8))
	if len(s.holdoutThisRun) != 1 || len(s.injectedThisRun) != 1 {
		t.Fatalf("seed failed: hold=%v inj=%v", s.holdoutThisRun, s.injectedThisRun)
	}
	s.clearInjectedRun()
	s.mu.Lock()
	hl, il := len(s.holdoutThisRun), len(s.injectedThisRun)
	s.mu.Unlock()
	if hl != 0 || il != 0 {
		t.Fatalf("cancel must clear both sets, got holdout=%d injected=%d", hl, il)
	}
}
