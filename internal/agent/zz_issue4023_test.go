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
	src, err := readAgentAndProcessingSources()
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
