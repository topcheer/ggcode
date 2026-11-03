package agent

import (
	"testing"
	"time"
)

func hold3275Learning(cat, typ string, conf float64, injRuns int) trajectoryLearning {
	l := holdoutLearning(cat, typ, conf)

	l.InjectedRuns = injRuns
	l.AfterSuccess = injRuns
	return l
}

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

	hp := trajHoldoutPath(dir)
	ledger, _ := loadHoldoutLedger(hp)
	for i := range ledger {
		ledger[i].ReleasedUntil = time.Now().UTC().Add(trajHoldoutReleaseWindow)
	}
	if err := writeHoldoutLedger(hp, ledger); err != nil {
		t.Fatal(err)
	}

	held2 := trajHoldoutSelect(dir, entries)
	if held2[claimed] {
		t.Fatalf("released key re-claimed: %v", held2)
	}

	trajHoldoutDay = func() int64 { return 101 }
	held3 := trajHoldoutSelect(dir, entries)
	if len(held3) != 1 || held3[claimed] {
		t.Fatalf("rotation must resume on a non-released key, got %v", held3)
	}
}

func TestHoldout3275_VerdictReleasePersistsRow(t *testing.T) {
	dir := t.TempDir()

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
		if l.InjectedRuns != 3 {
			t.Fatalf("control arm must not touch injection counters: %+v", l)
		}
	}
}

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
