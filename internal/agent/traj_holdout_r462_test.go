package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// r462 shadow-holdout control arm probes.

func holdoutTestState(t *testing.T, dir string, learnings ...trajectoryLearning) *trajIntelState {
	t.Helper()
	s := newTrajIntelState()
	s.filePath = filepath.Join(dir, ".ggcode", "trajectory-learnings.jsonl")
	if err := os.MkdirAll(filepath.Dir(s.filePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if len(learnings) > 0 {
		if err := s.rewriteAllLocked(func(_ []trajectoryLearning, loadErr error) ([]trajectoryLearning, error) {
			if loadErr != nil && !os.IsNotExist(loadErr) {
				return nil, loadErr
			}
			return learnings, nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func holdoutLearning(cat, typ string, conf float64) trajectoryLearning {
	return trajectoryLearning{
		Category: cat, Type: typ, Insight: cat + "/" + typ + " insight",
		Confidence: conf, Timestamp: time.Now().UTC(),
	}
}

// 1. Rotation determinism: same day + same candidates -> same held key,
// across independent calls; a changed day rotates within the category.
func TestHoldoutSelect_DeterministicRotation(t *testing.T) {
	dir := t.TempDir()
	entries := []trajectoryLearning{
		holdoutLearning("build", "pattern", 0.8),
		holdoutLearning("build", "failure", 0.7),
		holdoutLearning("build", "hint", 0.9),
		holdoutLearning("test", "pattern", 0.6),
	}
	trajHoldoutDay = func() int64 { return 100 }
	t.Cleanup(func() { trajHoldoutDay = func() int64 { return time.Now().UTC().Unix() / 86400 } })

	a := trajHoldoutSelect(dir, entries)
	b := trajHoldoutSelect(dir, entries)
	if len(a) != 2 || len(b) != 2 {
		t.Fatalf("expected one held key per category, got %d / %d", len(a), len(b))
	}
	for k := range a {
		if !b[k] {
			t.Fatalf("selection not deterministic: %v vs %v", a, b)
		}
	}
	// Streak preservation: same day re-select keeps counters (written below).
	s := holdoutTestState(t, dir)
	s.recordHoldoutLocked(holdoutLearning("test", "pattern", 0.6))
	s.recordHoldoutOutcome(dir, true)
	led, err := loadHoldoutLedger(trajHoldoutPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range led {
		if e.Category == "test" && e.HoldoutRuns != 1 {
			t.Fatalf("holdout run not recorded: %+v", e)
		}
	}
	// Day change rotates the slot within the category (3 candidates).
	trajHoldoutDay = func() int64 { return 101 }
	c := trajHoldoutSelect(dir, entries)
	if len(c) != 2 {
		t.Fatalf("rotation lost a category: %v", c)
	}
}

// 2. Render skips held entries and the outcome write-back feeds only the
// holdout ledger — the r461 injection counters stay untouched (decoupling).
func TestHoldout_RenderSkipAndCounterDecoupling(t *testing.T) {
	dir := t.TempDir()
	entries := []trajectoryLearning{holdoutLearning("build", "pattern", 0.9)}
	s := holdoutTestState(t, dir, entries...)
	trajHoldoutDay = func() int64 { return 100 }
	t.Cleanup(func() { trajHoldoutDay = func() int64 { return time.Now().UTC().Unix() / 86400 } })

	s.RenderPromptSection(dir)
	target := trajKey{cat: "build", typ: "pattern"}
	if !s.holdoutThisRun[target] {
		t.Fatalf("expected the seeded candidate held out, got %v", s.holdoutThisRun)
	}
	if s.injectedThisRun[target] {
		t.Fatal("held entry must NOT be counted as injected")
	}
	s.recordHoldoutOutcome(dir, true)
	if len(s.holdoutThisRun) != 0 {
		t.Fatal("outcome write-back must consume the per-run set")
	}
	// r461 counters untouched on disk.
	ls, err := s.loadFromFile()
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range ls {
		if l.InjectedRuns != 0 || l.AfterSuccess != 0 {
			t.Fatalf("control arm leaked into injection counters: %+v", l)
		}
	}
}

// 3. Verdict boundaries: delta <= 0 decays Confidence (via the r459
// channel, clamped at the floor) and releases; delta >= 10pp releases
// without decay; immature arms stay held and unjudged.
func TestHoldout_VerdictBoundaries(t *testing.T) {
	t.Run("negative-delta decays and releases", func(t *testing.T) {
		dir := t.TempDir()
		// Injection arm 1/4 vs holdout arm 2/4 -> delta = -0.25.
		s := holdoutTestState(t, dir, holdoutLearning("build", "pattern", 0.9))
		if err := s.rewriteAllLocked(func(existing []trajectoryLearning, _ error) ([]trajectoryLearning, error) {
			existing[0].InjectedRuns = 4
			existing[0].AfterSuccess = 1
			return existing, nil
		}); err != nil {
			t.Fatal(err)
		}
		seedLedgerWithRuns(t, dir, "build", "pattern", 4, 2)
		s.recordHoldoutLocked(holdoutLearning("build", "pattern", 0.9))
		s.recordHoldoutOutcome(dir, true) // any outcome: verdict is what matters
		ls, err := s.loadFromFile()
		if err != nil {
			t.Fatal(err)
		}
		if got := ls[0].Confidence; got >= 0.9 {
			t.Fatalf("confidence not decayed: %v", got)
		}
		if led, _ := loadHoldoutLedger(trajHoldoutPath(dir)); len(led) != 0 {
			t.Fatalf("verdict must release the holdout entry, got %+v", led)
		}
	})
	t.Run("positive-delta releases without decay", func(t *testing.T) {
		dir := t.TempDir()
		s := holdoutTestState(t, dir, holdoutLearning("build", "pattern", 0.9))
		if err := s.rewriteAllLocked(func(existing []trajectoryLearning, _ error) ([]trajectoryLearning, error) {
			existing[0].InjectedRuns = 5
			existing[0].AfterSuccess = 5
			return existing, nil
		}); err != nil {
			t.Fatal(err)
		}
		seedLedgerWithRuns(t, dir, "build", "pattern", 5, 2) // 1.0 - 0.4 = 0.6
		s.recordHoldoutLocked(holdoutLearning("build", "pattern", 0.9))
		s.recordHoldoutOutcome(dir, false)
		ls, _ := s.loadFromFile()
		if ls[0].Confidence != 0.9 {
			t.Fatalf("helpful insight must not decay: %v", ls[0].Confidence)
		}
		if led, _ := loadHoldoutLedger(trajHoldoutPath(dir)); len(led) != 0 {
			t.Fatalf("helpful verdict must release, got %+v", led)
		}
	})
	t.Run("immature injection arm stays held unjudged", func(t *testing.T) {
		dir := t.TempDir()
		s := holdoutTestState(t, dir, holdoutLearning("build", "pattern", 0.9))
		// Injection counters absent (InjectedRuns=0 < min 3).
		seedLedgerWithRuns(t, dir, "build", "pattern", 5, 0)
		s.recordHoldoutLocked(holdoutLearning("build", "pattern", 0.9))
		s.recordHoldoutOutcome(dir, true)
		led, _ := loadHoldoutLedger(trajHoldoutPath(dir))
		if len(led) != 1 {
			t.Fatal("unmeasured injection arm must keep the entry held")
		}
	})
}

// 4. Ledger IO round-trip via the atomic writer leaves no stray temp files.
func TestHoldout_LedgerNoStrayTemp(t *testing.T) {
	dir := t.TempDir()
	if err := writeHoldoutLedger(trajHoldoutPath(dir), []trajHoldoutEntry{{
		InsightKey: "build\x00pattern", Category: "build", Type: "pattern",
		HeldSince: time.Now().UTC(), HoldoutRuns: 1,
	}}); err != nil {
		t.Fatal(err)
	}
	ents, err := loadHoldoutLedger(trajHoldoutPath(dir))
	if err != nil || len(ents) != 1 || ents[0].HoldoutRuns != 1 {
		t.Fatalf("round-trip failed: %v %+v", err, ents)
	}
	files, _ := os.ReadDir(filepath.Join(dir, ".ggcode"))
	for _, f := range files {
		if strings.HasSuffix(f.Name(), ".tmp") {
			t.Fatalf("stray temp: %s", f.Name())
		}
	}
}

func seedLedgerWithRuns(t *testing.T, dir, cat, typ string, runs, succ int) {
	t.Helper()
	if err := writeHoldoutLedger(trajHoldoutPath(dir), []trajHoldoutEntry{{
		InsightKey: trajHoldoutKeyString(trajKey{cat: cat, typ: typ}),
		Category:   cat, Type: typ,
		HeldSince:   time.Now().UTC(),
		HoldoutRuns: runs, HoldoutSuccesses: succ,
	}}); err != nil {
		t.Fatal(err)
	}
}
