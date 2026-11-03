package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

func TestPerfBaselineWarningOncePerSessionAcrossTurns_Issue1180(t *testing.T) {
	cm := &fakePerfCM{}
	a := issue1180RegressionAgent(cm)

	a.maybeInjectPerfRegression()
	if len(cm.added) != 1 {
		t.Fatalf("#1180: turn 1 must warn exactly once, got %d messages", len(cm.added))
	}

	for turn := 2; turn <= 4; turn++ {
		a.perfBaseline.reset()
		a.maybeInjectPerfRegression()
		if len(cm.added) != 1 {
			t.Fatalf("#1180: turn %d re-injected the warning (total %d messages): same regression must stay once-per-session", turn, len(cm.added))
		}
	}
}

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

	writeRuns(t, 10, 10, 10, 20, 22, 10)
	a.maybeInjectPerfRegression()
	if len(a.contextManager.(*fakePerfCM).added) != 1 {
		t.Fatalf("#1180: regressed disk history must warn once, got %d", len(a.contextManager.(*fakePerfCM).added))
	}

	a.perfBaseline.reset()
	writeRuns(t, 10, 10, 10, 10, 10, 11)
	a.maybeInjectPerfRegression()

	added := a.contextManager.(*fakePerfCM).added

	if len(added) != 1 {
		t.Fatalf("#1180: expected the single turn-1 warning to be the only one, got %d", len(added))
	}
}
