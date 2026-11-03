package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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

func TestHoldoutSelect_DeterministicRotation(t *testing.T) {
	dir := t.TempDir()
	entries := []trajectoryLearning{
		hold3275Learning("build", "pattern", 0.8, 3),
		hold3275Learning("build", "failure", 0.7, 3),
		hold3275Learning("build", "hint", 0.9, 3),
		hold3275Learning("test", "pattern", 0.6, 3),
		hold3275Learning("test", "failure", 0.6, 3),
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

	heldTestKey := a[trajKey{cat: "test", typ: "pattern"}] || a[trajKey{cat: "test", typ: "failure"}]
	if !heldTestKey {
		t.Fatalf("expected a test-category hold, got %v", a)
	}
	s := holdoutTestState(t, dir)
	var heldKey trajKey
	for k := range a {
		if k.cat == "test" {
			heldKey = k
		}
	}
	s.recordHoldoutLocked(trajectoryLearning{Category: heldKey.cat, Type: heldKey.typ})
	s.recordHoldoutOutcome(dir, true)
	led, err := loadHoldoutLedger(trajHoldoutPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range led {
		if e.Category == "test" && e.InsightKey == trajHoldoutKeyString(heldKey) && e.HoldoutRuns != 1 {
			t.Fatalf("holdout run not recorded: %+v", e)
		}
	}

	trajHoldoutDay = func() int64 { return 101 }
	c := trajHoldoutSelect(dir, entries)
	if len(c) != 2 {
		t.Fatalf("rotation lost a category: %v", c)
	}
}

func TestHoldout_RenderSkipAndCounterDecoupling(t *testing.T) {
	dir := t.TempDir()

	entries := []trajectoryLearning{
		hold3275Learning("build", "pattern", 0.9, 3),
		hold3275Learning("build", "failure", 0.7, 3),
	}
	s := holdoutTestState(t, dir, entries...)
	trajHoldoutDay = func() int64 { return 100 }
	t.Cleanup(func() { trajHoldoutDay = func() int64 { return time.Now().UTC().Unix() / 86400 } })

	s.RenderPromptSection(dir)
	if len(s.holdoutThisRun) != 1 {
		t.Fatalf("expected exactly one seeded candidate held out, got %v", s.holdoutThisRun)
	}
	var target trajKey
	for k := range s.holdoutThisRun {
		target = k
	}
	if s.injectedThisRun[target] {
		t.Fatal("held entry must NOT be counted as injected")
	}
	s.recordHoldoutOutcome(dir, true)
	if len(s.holdoutThisRun) != 0 {
		t.Fatal("outcome write-back must consume the per-run set")
	}

	ls, err := s.loadFromFile()
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range ls {
		if trajKeyOf(l) == target {
			if l.InjectedRuns != 3 || l.AfterSuccess != 3 {
				t.Fatalf("control arm leaked into held entry's counters: %+v", l)
			}
		}
	}
}

func TestHoldout_VerdictBoundaries(t *testing.T) {
	t.Run("negative-delta decays and releases", func(t *testing.T) {
		dir := t.TempDir()

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
		s.recordHoldoutOutcome(dir, true)
		ls, err := s.loadFromFile()
		if err != nil {
			t.Fatal(err)
		}
		if got := ls[0].Confidence; got >= 0.9 {
			t.Fatalf("confidence not decayed: %v", got)
		}
		if led, _ := loadHoldoutLedger(trajHoldoutPath(dir)); len(led) != 1 || !led[0].ReleasedUntil.After(time.Now()) {

			t.Fatalf("verdict must release via suppression window, got %+v", led)
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
		seedLedgerWithRuns(t, dir, "build", "pattern", 5, 2)
		s.recordHoldoutLocked(holdoutLearning("build", "pattern", 0.9))
		s.recordHoldoutOutcome(dir, false)
		ls, _ := s.loadFromFile()
		if ls[0].Confidence != 0.9 {
			t.Fatalf("helpful insight must not decay: %v", ls[0].Confidence)
		}
		if led, _ := loadHoldoutLedger(trajHoldoutPath(dir)); len(led) != 1 || !led[0].ReleasedUntil.After(time.Now()) {

			t.Fatalf("helpful verdict must release via suppression window, got %+v", led)
		}
	})
	t.Run("immature injection arm stays held unjudged", func(t *testing.T) {
		dir := t.TempDir()
		s := holdoutTestState(t, dir, holdoutLearning("build", "pattern", 0.9))

		seedLedgerWithRuns(t, dir, "build", "pattern", 5, 0)
		s.recordHoldoutLocked(holdoutLearning("build", "pattern", 0.9))
		s.recordHoldoutOutcome(dir, true)
		led, _ := loadHoldoutLedger(trajHoldoutPath(dir))
		if len(led) != 1 {
			t.Fatal("unmeasured injection arm must keep the entry held")
		}
	})
}

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
