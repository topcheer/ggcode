package agent

import (
	"testing"

	ctxpkg "github.com/topcheer/ggcode/internal/context"
	"github.com/topcheer/ggcode/internal/provider"
)

// fakePerfCM is a minimal ContextManager: embedding the interface satisfies
// the full method set; only Add is exercised by maybeInjectPerfRegression.
type fakePerfCM struct {
	ctxpkg.ContextManager
	added []provider.Message
}

func (f *fakePerfCM) Add(msg provider.Message) { f.added = append(f.added, msg) }

// TestIssue1148_FailedRunsDoNotVoteInConsensus guards #1148: the baseline side
// (computeMedianBaseline) only admits successful runs, but the consensus
// window used to take the raw last 3 historical entries. Failed runs
// (Success=false from iteration exhaustion, stream errors, user cancels)
// skew high on iterations/duration/errors, so any two consecutive failures
// voted the regression gate open against a successful-run median.
func TestIssue1148_FailedRunsDoNotVoteInConsensus(t *testing.T) {
	hist := []perfBaselineEntry{
		// Five successful runs form the baseline (iterations=10).
		{Iterations: 10, ToolCalls: 20, DurationSec: 60, Success: true},
		{Iterations: 10, ToolCalls: 20, DurationSec: 60, Success: true},
		{Iterations: 10, ToolCalls: 20, DurationSec: 60, Success: true},
		{Iterations: 10, ToolCalls: 20, DurationSec: 60, Success: true},
		{Iterations: 10, ToolCalls: 20, DurationSec: 60, Success: true},
		// Two failed runs with sky-high metrics, then a normal success.
		// Old code: f1+f2 both hit "iterations" -> consensus -> false
		// regression warning (issue reproduction).
		{Iterations: 25, ToolCalls: 40, DurationSec: 200, Success: false},
		{Iterations: 30, ToolCalls: 45, DurationSec: 240, Success: false},
		{Iterations: 10, ToolCalls: 20, DurationSec: 60, Success: true},
	}
	cm := &fakePerfCM{}
	a := &Agent{
		contextManager: cm,
		perfBaseline: &perfBaselineState{
			historical:  hist,
			baselineMid: perfBaselineEntry{Iterations: 10, ToolCalls: 20, DurationSec: 60},
			hasBaseline: true,
		},
	}

	a.maybeInjectPerfRegression()

	if a.perfBaseline.warnCount != 0 {
		t.Fatalf("failed runs must not vote in the consensus; expected no warning, got warnCount=%d (added=%v)", a.perfBaseline.warnCount, cm.added)
	}
	if len(cm.added) != 0 {
		t.Fatalf("no regression message expected, got: %v", cm.added)
	}
}

// TestIssue1148_SuccessfulRunsStillDetectRegression ensures the filter did
// not break real detection: three recent successful runs with two regressing
// on the same metric must still fire the warning.
func TestIssue1148_SuccessfulRunsStillDetectRegression(t *testing.T) {
	hist := []perfBaselineEntry{
		{Iterations: 10, ToolCalls: 20, DurationSec: 60, Success: true},
		{Iterations: 10, ToolCalls: 20, DurationSec: 60, Success: true},
		{Iterations: 10, ToolCalls: 20, DurationSec: 60, Success: true},
		{Iterations: 20, ToolCalls: 20, DurationSec: 60, Success: true},
		{Iterations: 22, ToolCalls: 20, DurationSec: 60, Success: true},
		{Iterations: 10, ToolCalls: 20, DurationSec: 60, Success: true},
	}
	cm := &fakePerfCM{}
	a := &Agent{
		contextManager: cm,
		perfBaseline: &perfBaselineState{
			historical:  hist,
			baselineMid: perfBaselineEntry{Iterations: 10, ToolCalls: 20, DurationSec: 60},
			hasBaseline: true,
		},
	}

	a.maybeInjectPerfRegression()

	if a.perfBaseline.warnCount != 1 {
		t.Fatalf("real regression across successful runs must still warn, got warnCount=%d", a.perfBaseline.warnCount)
	}
	if len(cm.added) != 1 {
		t.Fatalf("expected exactly one regression message, got %d", len(cm.added))
	}
}

// TestIssue1148_TwoFailuresPlusOlderSuccess uses only two successful runs in
// total history: the consensus window must stay short (no warning), not
// reach back and mix populations to fabricate a third vote.
func TestIssue1148_TwoFailuresPlusOlderSuccess(t *testing.T) {
	hist := []perfBaselineEntry{
		{Iterations: 10, ToolCalls: 20, DurationSec: 60, Success: true},
		{Iterations: 25, ToolCalls: 40, DurationSec: 200, Success: false},
		{Iterations: 30, ToolCalls: 45, DurationSec: 240, Success: false},
	}
	cm := &fakePerfCM{}
	a := &Agent{
		contextManager: cm,
		perfBaseline: &perfBaselineState{
			historical:  hist,
			baselineMid: perfBaselineEntry{Iterations: 10, ToolCalls: 20, DurationSec: 60},
			hasBaseline: true,
		},
	}

	a.maybeInjectPerfRegression()

	if a.perfBaseline.warnCount != 0 || len(cm.added) != 0 {
		t.Fatalf("insufficient successful runs must not warn, got warnCount=%d added=%v", a.perfBaseline.warnCount, cm.added)
	}
}
