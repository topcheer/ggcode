package agent

// Issue #2723 probe: checkSingleRunRegression returned on the FIRST metric
// hit, so a run that regressed on several metrics cast only one vote and
// same-metric 2-of-3 consensus was systematically missed (iterations and
// duration are strongly correlated). The fix collects ALL hit metrics per
// run; pickConsensusPerfMetric / perfMetricOrder semantics are unchanged.

import (
	"reflect"
	"testing"
)

func TestIssue2723DualHitRunVotesAllMetrics(t *testing.T) {
	mid := perfBaselineEntry{Iterations: 10, ToolCalls: 50, Errors: 0, DurationSec: 100, ContextPeak: 5000}

	// run1 regresses on BOTH iterations (20 > 1.5*10) and duration
	// (200 > 1.5*100) -- the exact correlated case from the issue.
	run1 := perfBaselineEntry{Iterations: 20, ToolCalls: 50, DurationSec: 200, ContextPeak: 5000}
	// run2 regresses on duration only.
	run2 := perfBaselineEntry{Iterations: 10, ToolCalls: 50, DurationSec: 200, ContextPeak: 5000}
	// run3 normal.
	run3 := perfBaselineEntry{Iterations: 10, ToolCalls: 50, DurationSec: 100, ContextPeak: 5000}

	got := collectRunRegressionMetrics(run1, mid)
	if !reflect.DeepEqual(got, []string{"iterations", "duration"}) {
		t.Fatalf("dual-hit run collected %v, want [iterations duration]", got)
	}
	if got := collectRunRegressionMetrics(run3, mid); len(got) != 0 {
		t.Fatalf("normal run collected %v, want none", got)
	}

	// Voting: each hit metric gets one vote per run -> duration reaches
	// 2-of-3 consensus even though run1's first-hit was iterations.
	counts := map[string]int{}
	for _, r := range []perfBaselineEntry{run1, run2, run3} {
		for _, m := range collectRunRegressionMetrics(r, mid) {
			counts[m]++
		}
	}
	if pickConsensusPerfMetric(counts) != "duration" {
		t.Fatalf("consensus = %q, want duration (2/3 runs regressed on it)", pickConsensusPerfMetric(counts))
	}

	// selectWorstPerfHit must treat run1 as a duration hit too (it regressed
	// hardest: 200s) even though iterations is checked first.
	worst := selectWorstPerfHit([]perfBaselineEntry{run1, run2, run3}, mid, "duration")
	if worst.DurationSec != 200 || worst.Iterations != 20 {
		t.Fatalf("worst duration hit = %+v, want run1 (the dual-hit 200s run)", worst)
	}
}
