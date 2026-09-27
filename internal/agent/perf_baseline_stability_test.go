package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Repeated-run dispersion qualification (arXiv:2603.25764): the perf
// regression advisory must report the uncertainty of its own baseline --
// when the historical population is widely dispersed, the 1.5x/2x point
// thresholds sit within normal run-to-run variation and the advisory is
// qualified as advisory-only. Annotation-only: the #1143/#1148 consensus
// verdict and every threshold are untouched.

func TestPerfMetricRelMAD_StableAndDispersed(t *testing.T) {
	stable := successfulPerfRuns([]perfBaselineEntry{
		{Success: true, Iterations: 10},
		{Success: true, Iterations: 10},
		{Success: true, Iterations: 11},
		{Success: true, Iterations: 10},
		{Success: true, Iterations: 10},
		{Success: true, Iterations: 12},
	})
	if got := perfMetricRelMAD(stable, "iterations"); got > 0.1 {
		t.Fatalf("stable population relMAD = %v, want <= 0.1", got)
	}

	dispersed := successfulPerfRuns([]perfBaselineEntry{
		{Success: true, DurationSec: 20},
		{Success: true, DurationSec: 40},
		{Success: true, DurationSec: 50},
		{Success: true, DurationSec: 60},
		{Success: true, DurationSec: 800},
		{Success: true, DurationSec: 800},
		{Success: true, DurationSec: 800},
	})
	// durations sorted [20 40 50 60 800 800 800]: median 60, abs devs sorted
	// [0 10 20 40 740 740 740] -> MAD 40 -> relMAD 40/60 = 0.667.
	if got := perfMetricRelMAD(dispersed, "duration"); got < 0.6 {
		t.Fatalf("dispersed population relMAD = %v, want >= 0.6", got)
	}

	if got := perfMetricRelMAD(stable[:2], "iterations"); got != 0 {
		t.Fatalf("degenerate sample (<%d runs) relMAD = %v, want 0", perfBaselineMinRuns, got)
	}

	zeroMetric := successfulPerfRuns([]perfBaselineEntry{
		{Success: true, Compactions: 0},
		{Success: true, Compactions: 0},
		{Success: true, Compactions: 0},
		{Success: true, Compactions: 0},
		{Success: true, Compactions: 0},
	})
	if got := perfMetricRelMAD(zeroMetric, "compaction"); got != 0 {
		t.Fatalf("zero-median metric relMAD = %v, want 0", got)
	}
}

func TestPerfMetricRelMAD_ErrorRateUsesRates(t *testing.T) {
	// Dispersion must follow the rate (errors/tool_calls), not raw counts:
	// equal denominators here make counts and rates move together, but the
	// values are ratios (0.02, 0.02, 0.05, 0.2, 0.2): median 0.05, MAD 0.03
	// -> relMAD 0.6.
	runs := successfulPerfRuns([]perfBaselineEntry{
		{Success: true, Errors: 2, ToolCalls: 100},
		{Success: true, Errors: 2, ToolCalls: 100},
		{Success: true, Errors: 5, ToolCalls: 100},
		{Success: true, Errors: 20, ToolCalls: 100},
		{Success: true, Errors: 20, ToolCalls: 100},
	})
	if got := perfMetricRelMAD(runs, "error_rate"); got < 0.6 {
		t.Fatalf("error_rate dispersion relMAD = %v, want >= 0.6 (rate-based)", got)
	}
}

func TestPerfBaselineStabilityNote_Gating(t *testing.T) {
	stable := []perfBaselineEntry{
		{Success: true, Iterations: 10},
		{Success: true, Iterations: 10},
		{Success: true, Iterations: 10},
		{Success: true, Iterations: 11},
		{Success: true, Iterations: 12},
	}
	if note := perfBaselineStabilityNote(stable, "iterations"); note != "" {
		t.Fatalf("stable baseline must not be qualified, got %q", note)
	}

	// [4 4 10 10 15 15 30 30 30]: median 15, MAD 11 -> relMAD 0.733.
	dispersed := []perfBaselineEntry{
		{Success: true, Iterations: 4},
		{Success: true, Iterations: 4},
		{Success: true, Iterations: 10},
		{Success: true, Iterations: 10},
		{Success: true, Iterations: 15},
		{Success: true, Iterations: 15},
		{Success: true, Iterations: 30},
		{Success: true, Iterations: 30},
		{Success: true, Iterations: 30},
	}
	note := perfBaselineStabilityNote(dispersed, "iterations")
	if note == "" {
		t.Fatal("dispersed baseline must produce a stability note")
	}
	if !strings.Contains(note, "noisy") || !strings.Contains(note, "advisory") {
		t.Fatalf("stability note must state noise + advisory, got %q", note)
	}
	if !strings.Contains(note, "across 9") {
		t.Fatalf("stability note must report sample size 9, got %q", note)
	}

	if note := perfBaselineStabilityNote(dispersed[:3], "iterations"); note != "" {
		t.Fatalf("under-quorum sample must not be qualified, got %q", note)
	}
}

// End-to-end: a consensus regression over a dispersed baseline must inject
// the advisory WITH the stability qualifier; the same consensus over a
// stable baseline must stay unqualified.
func TestPerfBaselineWarningCarriesStabilityQualifier(t *testing.T) {
	newAgent := func(hist []perfBaselineEntry) *Agent {
		return &Agent{
			contextManager: &fakePerfCM{},
			perfBaseline: &perfBaselineState{
				historical:  hist,
				baselineMid: computeMedianBaseline(hist),
				hasBaseline: true,
			},
		}
	}

	dispersed := []perfBaselineEntry{
		// Population [4 4 10 10 15 15 30 30 30]: baseline iterations median
		// 15, relMAD 0.733 (noisy); the last 3 successful runs (30) all pass
		// 1.5x (22.5) -> iterations consensus -> advisory + qualifier.
		{Success: true, Iterations: 4, ToolCalls: 20, DurationSec: 60},
		{Success: true, Iterations: 4, ToolCalls: 20, DurationSec: 60},
		{Success: true, Iterations: 10, ToolCalls: 20, DurationSec: 60},
		{Success: true, Iterations: 10, ToolCalls: 20, DurationSec: 60},
		{Success: true, Iterations: 15, ToolCalls: 20, DurationSec: 60},
		{Success: true, Iterations: 15, ToolCalls: 20, DurationSec: 60},
		{Success: true, Iterations: 30, ToolCalls: 20, DurationSec: 60},
		{Success: true, Iterations: 30, ToolCalls: 20, DurationSec: 60},
		{Success: true, Iterations: 30, ToolCalls: 20, DurationSec: 60},
	}
	a := newAgent(dispersed)
	a.maybeInjectPerfRegression()
	added := a.contextManager.(*fakePerfCM).added
	if len(added) != 1 {
		t.Fatalf("dispersed regression must warn exactly once, got %d", len(added))
	}
	if got := added[0].Content[0].Text; !strings.Contains(got, "noisy") || !strings.Contains(got, "advisory") {
		t.Fatalf("dispersed warning must carry stability qualifier, got %q", got)
	}

	stable := []perfBaselineEntry{
		// Same shape as the #1180 fixture: median 10, relMAD exactly 0.5 ->
		// at (not above) the noise gate -> advisory without qualifier.
		{Success: true, Iterations: 10, ToolCalls: 20, DurationSec: 60},
		{Success: true, Iterations: 10, ToolCalls: 20, DurationSec: 60},
		{Success: true, Iterations: 10, ToolCalls: 20, DurationSec: 60},
		{Success: true, Iterations: 20, ToolCalls: 20, DurationSec: 60},
		{Success: true, Iterations: 22, ToolCalls: 20, DurationSec: 60},
		{Success: true, Iterations: 10, ToolCalls: 20, DurationSec: 60},
	}
	a2 := newAgent(stable)
	a2.maybeInjectPerfRegression()
	added2 := a2.contextManager.(*fakePerfCM).added
	if len(added2) != 1 {
		t.Fatalf("stable regression must warn exactly once, got %d", len(added2))
	}
	if got := added2[0].Content[0].Text; strings.Contains(got, "noisy") {
		t.Fatalf("stable warning must NOT carry stability qualifier, got %q", got)
	}
}

// Disk-load path: the qualifier is computed from the freshly loaded history
// (the same successfulPerfRuns population computeMedianBaseline uses), so a
// dispersed perf-baseline.json yields a qualified advisory on the run-start
// load path too.
func TestPerfBaselineStabilityFromDiskHistory(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".ggcode"), 0o755); err != nil {
		t.Fatalf("mkdir .ggcode: %v", err)
	}
	iters := []int{4, 4, 10, 10, 15, 15, 30, 30, 30}
	runs := make([]string, len(iters))
	for i, it := range iters {
		runs[i] = `{"iter":` + intToStr(it) + `,"tc":20,"dur":60,"ok":true}`
	}
	body := `{"runs":[` + strings.Join(runs, ",") + "]}"
	if err := os.WriteFile(perfBaselinePath(dir), []byte(body), 0o644); err != nil {
		t.Fatalf("write baseline: %v", err)
	}
	a := &Agent{workingDir: dir, contextManager: &fakePerfCM{}, perfBaseline: newPerfBaselineState()}
	a.maybeInjectPerfRegression()
	added := a.contextManager.(*fakePerfCM).added
	if len(added) != 1 {
		t.Fatalf("dispersed disk history must warn exactly once, got %d", len(added))
	}
	if got := added[0].Content[0].Text; !strings.Contains(got, "noisy") {
		t.Fatalf("disk-loaded warning must carry stability qualifier, got %q", got)
	}
}
