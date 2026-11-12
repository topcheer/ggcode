package agent

import (
	"strings"
	"testing"
)

func TestIssue4023_LatchReArmsPerRun(t *testing.T) {
	p := &researchPlanState{}
	if !p.shouldInject() {
		t.Fatal("first call within run 1 must inject")
	}
	if p.shouldInject() {
		t.Fatal("second call within run 1 must be suppressed (fire-once per run)")
	}

	p.reset()
	if !p.shouldInject() {
		t.Fatal("after reset, run 2 must inject again - latch stuck = #4023 regression")
	}
}

func TestIssue4023_PerRunResetWiring(t *testing.T) {
	// #5543: the per-run reset block moved to reset_turn.go; scan the
	// stream-loop source files (shape-agnostic file targeting).
	text, err := readStreamLoopSource("agent.go", "reset_turn.go")
	if err != nil {
		t.Skipf("stream-loop source not readable: %v", err)
	}
	anchor := "a.resetPlanner()"
	idx := strings.Index(text, anchor)
	if idx < 0 {
		t.Fatal("per-run reset anchor (resetPlanner) not found")
	}
	window := text[idx:min(idx+700, len(text))]
	if !strings.Contains(window, "a.researchPlan.reset()") {
		t.Fatal("per-run reset block no longer re-arms the research plan latch (#4023 wiring lost)")
	}
	if !strings.Contains(window, "a.researchPlanSubs = nil") {
		t.Fatal("stale researchPlanSubs not cleared per run - r365 gap report would keep consuming run 1's plan")
	}
}
