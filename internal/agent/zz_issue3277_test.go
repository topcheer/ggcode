package agent

// Probes for #3277: the cancelled-run branch (#3273, f80326935) cleared
// only the injected-key set. The holdout (control-arm) per-run set leaked
// across runs: a key held in the cancelled run survived into the next
// run's recordHoldoutOutcome, where it was counted as held even though
// the next run (after a day-rotation slot move) may have injected it
// normally - double-arming the r462 delta verdict. clearInjectedRun must
// drop BOTH per-run sets: a cancelled run neither counts nor carries.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestIssue3277_CancelledRunDropsBothPerRunSets(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home) // isolate any global-tier access

	dir := t.TempDir()
	// Persist two learnings so a leaked key WOULD bump on-disk counters
	// if the cancelled run's sets survived.
	held := mkLearning(time.Now(), "strategy", "cancel_cat", "held in run1")
	inj := mkLearning(time.Now(), "recovery", "cancel_cat2", "injected in run1")
	writeLearnings(t, dir, []trajectoryLearning{held, inj})

	s := newTrajIntelState()
	// Run 1 had both arms active: one injected entry, one held entry.
	s.mu.Lock()
	s.recordInjectedLocked(inj)
	s.recordHoldoutLocked(held)
	s.mu.Unlock()
	if len(s.injectedThisRun) != 1 || len(s.holdoutThisRun) != 1 {
		t.Fatalf("setup: both per-run sets must hold one key, got inj=%v held=%v",
			s.injectedThisRun, s.holdoutThisRun)
	}

	// Cancelled branch: neither counts nor carries.
	s.clearInjectedRun()

	if len(s.injectedThisRun) != 0 {
		t.Fatalf("injected set must be cleared, got %v", s.injectedThisRun)
	}
	if len(s.holdoutThisRun) != 0 {
		t.Fatalf("#3277: holdout set must be cleared on cancel, got %v", s.holdoutThisRun)
	}

	// The next (completed) run must record NOTHING from the cancelled
	// run's keys: no holdout ledger row, no injected-counter bump.
	s.recordHoldoutOutcome(dir, true)
	s.recordInjectionOutcome(dir, true)

	if _, err := os.Stat(trajHoldoutPath(dir)); !os.IsNotExist(err) {
		t.Fatalf("leaked held keys must not reach the holdout ledger; ledger state: %v", err)
	}

	for _, l := range loadLearningsRaw3277(t, dir) {
		if l.InjectedRuns != 0 {
			t.Fatalf("#3277: leaked injected key counted on next run: %+v", l)
		}
	}
}

// loadLearningsRaw3277 reads the workspace store back for counter checks.
func loadLearningsRaw3277(t *testing.T, dir string) []trajectoryLearning {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, ".ggcode", "trajectory-learnings.jsonl"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	var out []trajectoryLearning
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var l trajectoryLearning
		if err := json.Unmarshal([]byte(line), &l); err != nil {
			t.Fatalf("parse learning line %q: %v", line, err)
		}
		out = append(out, l)
	}
	return out
}
