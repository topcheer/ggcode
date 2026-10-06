package agent

// r461 outcome-feedback-loop tests: injection counting (render-time key
// recording), run-end write-back (InjectedRuns/AfterSuccess/AfterFail),
// idempotence per run, and the effectiveness gate at render time.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// trajOutcomeIsolationHome isolates HOME so tests never touch any
// home-relative state (same discipline as the sibling traj_*_test.go files).
func trajOutcomeIsolationHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // Windows parity, harmless elsewhere
	return home
}

// trajOutcomeStorePath returns the learnings file path under dir.
func trajOutcomeStorePath(dir string) string {
	return filepath.Join(dir, ".ggcode", "trajectory-learnings.jsonl")
}

// writeTrajOutcomeFile seeds the store with the given entries (JSONL).
func writeTrajOutcomeFile(t *testing.T, dir string, entries ...trajectoryLearning) {
	t.Helper()
	path := trajOutcomeStorePath(dir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	var b strings.Builder
	for _, l := range entries {
		raw, err := json.Marshal(l)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		b.Write(raw)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatalf("write store: %v", err)
	}
}

// loadTrajOutcomeFile reads the store entries back for assertions.
func loadTrajOutcomeFile(t *testing.T, dir string) []trajectoryLearning {
	t.Helper()
	s := newTrajIntelState()
	s.filePath = trajOutcomeStorePath(dir)
	entries, err := s.loadFromFile()
	if err != nil {
		t.Fatalf("load store: %v", err)
	}
	return entries
}

// trajOutcomeFindByInsight returns the entry whose Insight matches.
func trajOutcomeFindByInsight(t *testing.T, entries []trajectoryLearning, insight string) trajectoryLearning {
	t.Helper()
	for _, l := range entries {
		if l.Insight == insight {
			return l
		}
	}
	t.Fatalf("entry with insight %q not found in store (%d entries)", insight, len(entries))
	return trajectoryLearning{}
}

// TestEffectivenessGatedThreshold pins the gate semantics directly:
// >=3 measured injections AND success ratio <0.4 retires an entry;
// fewer samples or a higher ratio never gate, whatever the raw counts.
func TestEffectivenessGatedThreshold(t *testing.T) {
	cases := []struct {
		name         string
		injectedRuns int
		afterSuccess int
		wantGated    bool
	}{
		{"no data never gates", 0, 0, false},
		{"one bad run does not gate (sample floor)", 1, 0, false},
		{"two bad runs do not gate (sample floor)", 2, 0, false},
		{"3 runs 1 success = 0.33 < 0.4 gates", 3, 1, true},
		{"3 runs 0 success gates", 3, 0, true},
		{"3 runs 2 success = 0.67 keeps", 3, 2, false},
		{"5 runs 1 success = 0.2 gates", 5, 1, true},
		{"5 runs 2 success = 0.4 exactly keeps (strict <)", 5, 2, false},
		{"4 runs 4 success keeps", 4, 4, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l := trajectoryLearning{InjectedRuns: tc.injectedRuns, AfterSuccess: tc.afterSuccess}
			if got := effectivenessGated(l); got != tc.wantGated {
				t.Errorf("effectivenessGated(InjectedRuns=%d, AfterSuccess=%d) = %v, want %v",
					tc.injectedRuns, tc.afterSuccess, got, tc.wantGated)
			}
		})
	}
}

// TestInjectionOutcomeCountingIdempotent verifies the full write-back loop:
// render records the injected identities, run-close bumps each entry once,
// a second close without an intervening render is a no-op, and a fresh
// render + failed run bumps the fail counter.
func TestInjectionOutcomeCountingIdempotent(t *testing.T) {
	trajHoldoutEnabled = false
	t.Cleanup(func() { trajHoldoutEnabled = true })
	trajOutcomeIsolationHome(t)
	dir := t.TempDir()
	now := time.Now()
	writeTrajOutcomeFile(t, dir,
		trajectoryLearning{Timestamp: now, Type: "optimization", Category: "over-exploration", Insight: "insight a"},
		trajectoryLearning{Timestamp: now, Type: "recovery", Category: "error-recovery", Insight: "insight b"},
	)

	s := newTrajIntelState()

	// This run's system prompt injected both entries.
	section := s.RenderPromptSection(dir)
	if !strings.Contains(section, "insight a") || !strings.Contains(section, "insight b") {
		t.Fatalf("both entries should render before outcome accounting, got: %q", section)
	}
	if len(s.injectedThisRun) != 2 {
		t.Fatalf("injectedThisRun should hold 2 keys, got %d", len(s.injectedThisRun))
	}

	// Run ends successfully.
	s.recordInjectionOutcome(dir, true)

	entries := loadTrajOutcomeFile(t, dir)
	for _, insight := range []string{"insight a", "insight b"} {
		l := trajOutcomeFindByInsight(t, entries, insight)
		if l.InjectedRuns != 1 {
			t.Errorf("%q InjectedRuns = %d, want 1", insight, l.InjectedRuns)
		}
		if l.AfterSuccess != 1 || l.AfterFail != 0 {
			t.Errorf("%q AfterSuccess=%d AfterFail=%d, want 1/0", insight, l.AfterSuccess, l.AfterFail)
		}
	}

	// Second close without a fresh render must not double-count (the
	// injected-key set is consumed by the first close).
	s.recordInjectionOutcome(dir, false)
	entries = loadTrajOutcomeFile(t, dir)
	for _, insight := range []string{"insight a", "insight b"} {
		l := trajOutcomeFindByInsight(t, entries, insight)
		if l.InjectedRuns != 1 {
			t.Errorf("after second close %q InjectedRuns = %d, want still 1", insight, l.InjectedRuns)
		}
		if l.AfterFail != 0 {
			t.Errorf("after second close %q AfterFail = %d, want 0 (no injection recorded)", insight, l.AfterFail)
		}
	}

	// A new run re-renders (fresh injection) and fails: counters move once.
	s.RenderPromptSection(dir)
	s.recordInjectionOutcome(dir, false)
	entries = loadTrajOutcomeFile(t, dir)
	for _, insight := range []string{"insight a", "insight b"} {
		l := trajOutcomeFindByInsight(t, entries, insight)
		if l.InjectedRuns != 2 {
			t.Errorf("after failed run %q InjectedRuns = %d, want 2", insight, l.InjectedRuns)
		}
		if l.AfterSuccess != 1 || l.AfterFail != 1 {
			t.Errorf("after failed run %q AfterSuccess=%d AfterFail=%d, want 1/1", insight, l.AfterSuccess, l.AfterFail)
		}
	}
}

// TestInjectionOutcomeRenderRepeatNoDoubleCount verifies that rendering the
// same store multiple times within one run (system prompt is rebuilt per
// iteration) counts each entry at most once at run close (set semantics).
func TestInjectionOutcomeRenderRepeatNoDoubleCount(t *testing.T) {
	trajHoldoutEnabled = false
	t.Cleanup(func() { trajHoldoutEnabled = true })
	trajOutcomeIsolationHome(t)
	dir := t.TempDir()
	writeTrajOutcomeFile(t, dir,
		trajectoryLearning{Timestamp: time.Now(), Type: "optimization", Category: "over-exploration", Insight: "insight a"},
	)

	s := newTrajIntelState()
	for i := 0; i < 3; i++ {
		if section := s.RenderPromptSection(dir); !strings.Contains(section, "insight a") {
			t.Fatalf("render %d should inject the entry, got: %q", i, section)
		}
	}
	if len(s.injectedThisRun) != 1 {
		t.Fatalf("repeated renders must keep injectedThisRun at 1 key (set semantics), got %d", len(s.injectedThisRun))
	}

	s.recordInjectionOutcome(dir, true)
	l := trajOutcomeFindByInsight(t, loadTrajOutcomeFile(t, dir), "insight a")
	if l.InjectedRuns != 1 || l.AfterSuccess != 1 {
		t.Errorf("after 3 renders + 1 close: InjectedRuns=%d AfterSuccess=%d, want 1/1", l.InjectedRuns, l.AfterSuccess)
	}
}

// TestLowSuccessRateFiltering verifies entries with InjectedRuns>=3 and
// success ratio <0.4 are skipped at render time while healthy and unmeasured
// entries still consume slots. Categories differ so category dedupe does not
// interfere with the gate under test.
func TestLowSuccessRateFiltering(t *testing.T) {
	trajHoldoutEnabled = false
	t.Cleanup(func() { trajHoldoutEnabled = true })
	trajOutcomeIsolationHome(t)
	dir := t.TempDir()
	now := time.Now()
	writeTrajOutcomeFile(t, dir,
		trajectoryLearning{Timestamp: now, Type: "optimization", Category: "over-exploration", Insight: "bad insight", InjectedRuns: 5, AfterSuccess: 1, AfterFail: 4}, // 0.2 < 0.4 -> skip
		trajectoryLearning{Timestamp: now, Type: "recovery", Category: "error-recovery", Insight: "good insight", InjectedRuns: 5, AfterSuccess: 4, AfterFail: 1},      // 0.8 -> keep
		trajectoryLearning{Timestamp: now, Type: "strategy", Category: "efficient-completion", Insight: "fresh insight"},                                               // no data -> keep
	)

	s := newTrajIntelState()
	section := s.RenderPromptSection(dir)
	if strings.Contains(section, "bad insight") {
		t.Error("low-success entry should be filtered out of rendered prompt")
	}
	if !strings.Contains(section, "good insight") {
		t.Error("high-success entry should be retained")
	}
	if !strings.Contains(section, "fresh insight") {
		t.Error("entry with no outcome data (InjectedRuns < 3) must not be filtered")
	}
	// The gated entry must not even be counted as injected for this run.
	if s.injectedThisRun[trajKey{cat: "over-exploration", typ: "optimization"}] {
		t.Error("gated entry must not be recorded as injected for outcome accounting")
	}
}

// TestRenderNoDataNoFiltering verifies that when no entry has outcome data,
// nothing is filtered.
func TestRenderNoDataNoFiltering(t *testing.T) {
	trajHoldoutEnabled = false
	t.Cleanup(func() { trajHoldoutEnabled = true })
	trajOutcomeIsolationHome(t)
	dir := t.TempDir()
	now := time.Now()
	writeTrajOutcomeFile(t, dir,
		trajectoryLearning{Timestamp: now, Type: "optimization", Category: "over-exploration", Insight: "insight x"},
		trajectoryLearning{Timestamp: now, Type: "recovery", Category: "error-recovery", Insight: "insight y"},
	)

	s := newTrajIntelState()
	section := s.RenderPromptSection(dir)
	if !strings.Contains(section, "insight x") || !strings.Contains(section, "insight y") {
		t.Errorf("entries without outcome data must both render, got: %q", section)
	}
}

// TestRenderHighSuccessRetainedAfterWriteBack covers the full loop end to
// end through the persist path: learnings with outcome counters survive a
// persist (pending buffer -> consolidation -> JSONL with omitempty tags) and
// reload, the healthy entry still renders, and the retired one does not.
func TestRenderHighSuccessRetainedAfterWriteBack(t *testing.T) {
	trajHoldoutEnabled = false
	t.Cleanup(func() { trajHoldoutEnabled = true })
	trajOutcomeIsolationHome(t)
	dir := t.TempDir()

	s := newTrajIntelState()
	s.filePath = trajOutcomeStorePath(dir)
	s.mu.Lock()
	s.learnings = []trajectoryLearning{
		{Timestamp: time.Now(), Type: "optimization", Category: "keep-cat", Insight: "keep insight", InjectedRuns: 4, AfterSuccess: 3, AfterFail: 1},
		{Timestamp: time.Now(), Type: "recovery", Category: "drop-cat", Insight: "drop insight", InjectedRuns: 4, AfterSuccess: 1, AfterFail: 3},
	}
	if err := s.persistLocked(); err != nil {
		s.mu.Unlock()
		t.Fatalf("persistLocked: %v", err)
	}
	s.mu.Unlock()

	if _, err := os.Stat(trajOutcomeStorePath(dir)); err != nil {
		t.Fatalf("expected learnings file: %v", err)
	}

	// A fresh state (new agent process) renders from the persisted store.
	s2 := newTrajIntelState()
	section := s2.RenderPromptSection(dir)
	if !strings.Contains(section, "keep insight") {
		t.Error("high-success entry must survive persist/reload and render")
	}
	if strings.Contains(section, "drop insight") {
		t.Error("low-success entry must be filtered after persist/reload")
	}
}
