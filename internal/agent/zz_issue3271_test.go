package agent

// Probes for #3271:
//   - A: global-tier injected entries must land their outcome counters on
//     the GLOBAL file (the workspace pass cannot match their keys by
//     construction). Without the dual sink the r461 retirement gate never
//     fired for the whole global tier.
//   - B: a cancelled run must drop its injected-key set instead of leaking
//     it into the next run's accounting.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestIssue3271_GlobalTierOutcomeWrittenToGlobalFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home) // redirect TrajGlobalPath

	// Global store: one entry whose category the workspace lacks.
	gp := filepath.Join(home, ".ggcode", "trajectory-learnings.jsonl")
	if err := os.MkdirAll(filepath.Dir(gp), 0o755); err != nil {
		t.Fatal(err)
	}
	g := mkLearning(time.Now().Add(-time.Minute), "strategy", "global_cat", "from elsewhere")
	if err := writeTrajFileAt3271(gp, g); err != nil {
		t.Fatal(err)
	}

	// Workspace store: a DIFFERENT category only.
	dir := t.TempDir()
	writeLearnings(t, dir, []trajectoryLearning{
		mkLearning(time.Now(), "recovery", "local_cat", "local insight"),
	})

	s := newTrajIntelState()
	// Simulate a run that injected BOTH rows (renderer would top up from
	// global for global_cat).
	s.mu.Lock()
	s.recordInjectedLocked(g)
	s.recordInjectedLocked(mkLearning(time.Now(), "recovery", "local_cat", "local insight"))
	s.mu.Unlock()

	s.recordInjectionOutcome(dir, false)

	// Workspace counters: local_cat got the failure.
	views := TrajListLearnings(dir)
	var local *TrajLearningView
	for i := range views {
		if views[i].Category == "local_cat" {
			local = &views[i]
		}
	}
	if local == nil || local.Reinforced != 0 {
		t.Fatalf("setup sanity: local row missing")
	}
	// (InjectedRuns is not in the view; read the file directly.)
	wsRows, err := loadTrajFile(filepath.Join(dir, ".ggcode", "trajectory-learnings.jsonl"))
	if err != nil || len(wsRows) != 1 {
		t.Fatalf("workspace read: %v rows=%d", err, len(wsRows))
	}
	if wsRows[0].InjectedRuns != 1 || wsRows[0].AfterFail != 1 {
		t.Fatalf("workspace counters wrong: %+v", wsRows)
	}

	// Global counters: global_cat must ALSO have been bumped (the bug: 0).
	gRows, gErr := loadTrajFile(gp)
	if gErr != nil || len(gRows) != 1 {
		t.Fatalf("global read: %v rows=%d", gErr, len(gRows))
	}
	if gRows[0].InjectedRuns != 1 || gRows[0].AfterFail != 1 {
		t.Fatalf("global-tier counters not written (retirement gate dead for global tier): InjectedRuns=%d AfterFail=%d",
			gRows[0].InjectedRuns, gRows[0].AfterFail)
	}
}

func TestIssue3271_CancelledRunDropsInjectedKeys(t *testing.T) {
	dir := t.TempDir()
	writeLearnings(t, dir, []trajectoryLearning{
		mkLearning(time.Now(), "strategy", "s1", "insight"),
	})

	s := newTrajIntelState()
	s.mu.Lock()
	s.recordInjectedLocked(mkLearning(time.Now(), "strategy", "s1", "insight"))
	s.mu.Unlock()

	// Cancelled path: clear instead of record.
	s.clearInjectedRun()

	// A later (completed) run must find NOTHING to account: no double
	// counting from the leaked keys.
	s.recordInjectionOutcome(dir, true)
	wsRows2, err := loadTrajFile(filepath.Join(dir, ".ggcode", "trajectory-learnings.jsonl"))
	if err != nil || len(wsRows2) != 1 {
		t.Fatalf("read: %v rows=%d", err, len(wsRows2))
	}
	if wsRows2[0].InjectedRuns != 0 {
		t.Fatalf("cancelled-run keys leaked into later accounting: %+v", wsRows2)
	}
}

func writeTrajFileAt3271(path string, ls ...trajectoryLearning) error {
	var body []byte
	for _, l := range ls {
		b, err := json.Marshal(l)
		if err != nil {
			return err
		}
		body = append(body, b...)
		body = append(body, '\n')
	}
	return os.WriteFile(path, body, 0o644)
}
