package agent

import (
	"reflect"
	"testing"
)

func TestIssue2723DualHitRunVotesAllMetrics(t *testing.T) {
	mid := perfBaselineEntry{Iterations: 10, ToolCalls: 50, Errors: 0, DurationSec: 100, ContextPeak: 5000}

	run1 := perfBaselineEntry{Iterations: 20, ToolCalls: 50, DurationSec: 200, ContextPeak: 5000}

	run2 := perfBaselineEntry{Iterations: 10, ToolCalls: 50, DurationSec: 200, ContextPeak: 5000}

	run3 := perfBaselineEntry{Iterations: 10, ToolCalls: 50, DurationSec: 100, ContextPeak: 5000}

	got := collectRunRegressionMetrics(run1, mid)
	if !reflect.DeepEqual(got, []string{"iterations", "duration"}) {
		t.Fatalf("dual-hit run collected %v, want [iterations duration]", got)
	}
	if got := collectRunRegressionMetrics(run3, mid); len(got) != 0 {
		t.Fatalf("normal run collected %v, want none", got)
	}

	counts := map[string]int{}
	for _, r := range []perfBaselineEntry{run1, run2, run3} {
		for _, m := range collectRunRegressionMetrics(r, mid) {
			counts[m]++
		}
	}
	if pickConsensusPerfMetric(counts) != "duration" {
		t.Fatalf("consensus = %q, want duration (2/3 runs regressed on it)", pickConsensusPerfMetric(counts))
	}

	worst := selectWorstPerfHit([]perfBaselineEntry{run1, run2, run3}, mid, "duration")
	if worst.DurationSec != 200 || worst.Iterations != 20 {
		t.Fatalf("worst duration hit = %+v, want run1 (the dual-hit 200s run)", worst)
	}
}
