package agent

import (
	"testing"

	ctxpkg "github.com/topcheer/ggcode/internal/context"
	"github.com/topcheer/ggcode/internal/provider"
)

type fakePerfCM struct {
	ctxpkg.ContextManager
	added []provider.Message
}

func (f *fakePerfCM) Add(msg provider.Message) { f.added = append(f.added, msg) }

func TestIssue1148_FailedRunsDoNotVoteInConsensus(t *testing.T) {
	hist := []perfBaselineEntry{

		{Iterations: 10, ToolCalls: 20, DurationSec: 60, Success: true},
		{Iterations: 10, ToolCalls: 20, DurationSec: 60, Success: true},
		{Iterations: 10, ToolCalls: 20, DurationSec: 60, Success: true},
		{Iterations: 10, ToolCalls: 20, DurationSec: 60, Success: true},
		{Iterations: 10, ToolCalls: 20, DurationSec: 60, Success: true},

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
