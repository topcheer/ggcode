package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestIssue3271_GlobalTierOutcomeWrittenToGlobalFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	gp := filepath.Join(home, ".ggcode", "trajectory-learnings.jsonl")
	if err := os.MkdirAll(filepath.Dir(gp), 0o755); err != nil {
		t.Fatal(err)
	}
	g := mkLearning(time.Now().Add(-time.Minute), "strategy", "global_cat", "from elsewhere")
	if err := writeTrajFileAt3271(gp, g); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	writeLearnings(t, dir, []trajectoryLearning{
		mkLearning(time.Now(), "recovery", "local_cat", "local insight"),
	})

	s := newTrajIntelState()

	s.mu.Lock()
	s.recordInjectedLocked(g)
	s.recordInjectedLocked(mkLearning(time.Now(), "recovery", "local_cat", "local insight"))
	s.mu.Unlock()

	s.recordInjectionOutcome(dir, false)

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

	wsRows, err := loadTrajFile(filepath.Join(dir, ".ggcode", "trajectory-learnings.jsonl"))
	if err != nil || len(wsRows) != 1 {
		t.Fatalf("workspace read: %v rows=%d", err, len(wsRows))
	}
	if wsRows[0].InjectedRuns != 1 || wsRows[0].AfterFail != 1 {
		t.Fatalf("workspace counters wrong: %+v", wsRows)
	}

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

	s.clearInjectedRun()

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
