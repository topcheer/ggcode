package agent

import (
	"os"
	"strings"
	"testing"
)

// #4023: the research plan's fire-once must be per-RUN, not per-Agent
// lifetime. A second research run in the same session has to get its own
// plan injection and must not compute report gaps against run 1's
// sub-queries.
func TestIssue4023ResearchPlanFiresOncePerRun(t *testing.T) {
	p := &researchPlanState{}
	if !p.shouldInject() {
		t.Fatal("first research run must inject the plan")
	}
	if p.shouldInject() {
		t.Fatal("second call within the SAME run must not re-inject (fire-once)")
	}
	// New run: resetPlanner path restores injection eligibility.
	p.reset()
	if !p.shouldInject() {
		t.Fatal("after reset (new run), the plan must inject again (#4023: Agent-lifetime-once bug)")
	}
}

// Wiring half: resetPlanner must clear both the injected flag and the
// stale sub-queries (source-pinned like the #3682 family).
func TestIssue4023ResetPlannerClearsPlanState(t *testing.T) {
	src, err := os.ReadFile("planner.go")
	if err != nil {
		t.Fatalf("read planner.go: %v", err)
	}
	text := string(src)
	start := strings.Index(text, "// resetPlanner clears state for a new run.")
	if start < 0 {
		t.Fatal("resetPlanner not found")
	}
	window := text[start : start+1200]
	if !strings.Contains(window, "a.researchPlan.reset()") {
		t.Fatal("resetPlanner must reset researchPlan.injected (#4023)")
	}
	if !strings.Contains(window, "a.researchPlanSubs = nil") {
		t.Fatal("resetPlanner must clear researchPlanSubs so run 2 gaps are not computed against run 1's plan (#4023)")
	}
}
