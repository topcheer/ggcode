package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #1180: perf_baseline.go reset() only cleared warnCount and the in-session
// baseline snapshot stayed frozen, so the same regression warning was
// re-injected as a full user message on every user turn - violating the
// "at most once per session" contract documented for the detector. reset()
// now keeps warnedThisSession (session-scoped, survives per-turn resets) and
// drops the frozen snapshot so the next run reloads fresh data from disk.
// #1167's atomic write keeps that reload safe against concurrent writers.

func issue1180RegressionAgent(cm *fakePerfCM) *Agent {
	hist := []perfBaselineEntry{
		{Iterations: 10, ToolCalls: 20, DurationSec: 60, Success: true},
		{Iterations: 10, ToolCalls: 20, DurationSec: 60, Success: true},
		{Iterations: 10, ToolCalls: 20, DurationSec: 60, Success: true},
		{Iterations: 20, ToolCalls: 20, DurationSec: 60, Success: true},
		{Iterations: 22, ToolCalls: 20, DurationSec: 60, Success: true},
		{Iterations: 10, ToolCalls: 20, DurationSec: 60, Success: true},
	}
	return &Agent{
		contextManager: cm,
		perfBaseline: &perfBaselineState{
			historical:  hist,
			baselineMid: perfBaselineEntry{Iterations: 10, ToolCalls: 20, DurationSec: 60},
			hasBaseline: true,
		},
	}
}

// TestPerfBaselineWarningOncePerSessionAcrossTurns_Issue1180 reproduces
// #1180: turn 1 fires the warning; reset() runs at the start of every
// subsequent user turn; the same regression must NOT warn again.
func TestPerfBaselineWarningOncePerSessionAcrossTurns_Issue1180(t *testing.T) {
	cm := &fakePerfCM{}
	a := issue1180RegressionAgent(cm)

	a.maybeInjectPerfRegression() // user turn 1
	if len(cm.added) != 1 {
		t.Fatalf("#1180: turn 1 must warn exactly once, got %d messages", len(cm.added))
	}

	for turn := 2; turn <= 4; turn++ {
		a.perfBaseline.reset() // agent.go calls this on every user turn
		a.maybeInjectPerfRegression()
		if len(cm.added) != 1 {
			t.Fatalf("#1180: turn %d re-injected the warning (total %d messages): same regression must stay once-per-session", turn, len(cm.added))
		}
	}
}

// TestPerfBaselineResetDropsFrozenSnapshot_Issue1180 verifies the other half
// of the fix: reset() must discard the in-memory baseline snapshot so the
// next run reloads fresh data from disk (which the regression history file
// provides) instead of replaying the stale conclusion.
func TestPerfBaselineResetDropsFrozenSnapshot_Issue1180(t *testing.T) {
	cm := &fakePerfCM{}
	a := issue1180RegressionAgent(cm)

	a.maybeInjectPerfRegression()
	a.perfBaseline.reset()

	if a.perfBaseline.hasBaseline {
		t.Fatal("#1180: reset() must drop the frozen hasBaseline snapshot")
	}
	if a.perfBaseline.historical != nil {
		t.Fatal("#1180: reset() must drop the frozen historical slice")
	}
	if a.perfBaseline.warnedThisSession != true {
		t.Fatal("#1180: warnedThisSession must survive reset() (that is the once-per-session fix)")
	}
}

// TestPerfBaselineReloadUsesFreshDiskDataAfterReset_Issue1180 end-to-end
// checks that after a reset the detector reads a freshly written history
// file (.ggcode/perf-baseline.json, written atomically since #1167) instead
// of the frozen snapshot: healthy data on disk means the run's own fresh
// read sees no regression.
func TestPerfBaselineReloadUsesFreshDiskDataAfterReset_Issue1180(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".ggcode"), 0o755); err != nil {
		t.Fatalf("mkdir .ggcode: %v", err)
	}
	a := &Agent{workingDir: dir, contextManager: &fakePerfCM{}, perfBaseline: newPerfBaselineState()}

	writeRuns := func(t *testing.T, iters ...int) {
		t.Helper()
		runs := make([]string, len(iters))
		for i, it := range iters {
			runs[i] = fmt.Sprintf(`{"iter":%d,"tc":20,"dur":60,"ok":true}`, it)
		}
		body := `{"runs":[` + strings.Join(runs, ",") + "]}"
		if err := os.WriteFile(perfBaselinePath(dir), []byte(body), 0o644); err != nil {
			t.Fatalf("write baseline: %v", err)
		}
	}

	// Regressed history on disk; baselineMid is recomputed by the run-start
	// load (historical/baselineMid empty -> hasBaseline false -> load path).
	writeRuns(t, 10, 10, 10, 20, 22, 10)
	a.maybeInjectPerfRegression()
	if len(a.contextManager.(*fakePerfCM).added) != 1 {
		t.Fatalf("#1180: regressed disk history must warn once, got %d", len(a.contextManager.(*fakePerfCM).added))
	}

	// User turn 2: reset(), then healthy history replaces the file on disk.
	a.perfBaseline.reset()
	writeRuns(t, 10, 10, 10, 10, 10, 11)
	a.maybeInjectPerfRegression()

	added := a.contextManager.(*fakePerfCM).added
	// Once-per-session flag keeps the injected total at exactly 1, and the
	// frozen regressed snapshot is gone (fresh read of healthy data would
	// not warn anyway).
	if len(added) != 1 {
		t.Fatalf("#1180: expected the single turn-1 warning to be the only one, got %d", len(added))
	}
}
