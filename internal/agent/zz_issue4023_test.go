package agent

// #4023 companion tests: the research plan latch must re-arm every run.
// researchPlanState promised "fires ONCE per run" but injected was never
// reset - actual semantics were once per Agent LIFETIME: a second research
// run got no plan injection, and the stale researchPlanSubs from run 1 kept
// feeding the r365 gap report, mis-flagging every run-1 sub-query as
// uncovered and steering run 2 off-target.

import (
	"os"
	"strings"
	"testing"
)

// Behavioral: after the latch fires it stays latched (fire-once within a
// run), and reset() re-arms it for the next run.
func TestIssue4023_LatchReArmsPerRun(t *testing.T) {
	p := &researchPlanState{}
	if !p.shouldInject() {
		t.Fatal("first call within run 1 must inject")
	}
	if p.shouldInject() {
		t.Fatal("second call within run 1 must be suppressed (fire-once per run)")
	}
	// Per-run reset (the wiring this fix restores at run start).
	p.reset()
	if !p.shouldInject() {
		t.Fatal("after reset, run 2 must inject again - latch stuck = #4023 regression")
	}
}

// Source guard: the per-run reset block must re-arm the research plan latch
// AND clear the stale sub-query set, next to resetPlanner.
func TestIssue4023_PerRunResetWiring(t *testing.T) {
	src, err := os.ReadFile("agent.go")
	if err != nil {
		t.Skipf("agent.go not readable: %v", err)
	}
	text := string(src)
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
