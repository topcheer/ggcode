package agent

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// #3998: researchPlan fire-once must be per-run ("fires ONCE per run"),
// not per-Agent-lifetime. The latch itself must clear on reset.
func TestResearchPlanStateResetPerTurn(t *testing.T) {
	p := &researchPlanState{}
	if !p.shouldInject() {
		t.Fatal("first inject must pass")
	}
	if p.shouldInject() {
		t.Fatal("second inject within one run must be blocked")
	}
	p.reset()
	if !p.shouldInject() {
		t.Fatal("after reset the next run must inject again")
	}
}

// #3998: resetTurnDetectors must clear both the fire-once latch and the
// stale plan so task 2's gap detection is not fed task 1's sub-queries.
func TestResetTurnDetectorsClearsResearchPlan(t *testing.T) {
	mp := &mockProvider{
		chatResp: &provider.ChatResponse{
			Message: provider.Message{
				Role:    "assistant",
				Content: []provider.ContentBlock{{Type: "text", Text: "ok"}},
			},
			Usage: provider.TokenUsage{InputTokens: 1, OutputTokens: 1},
		},
	}
	a := NewAgent(mp, tool.NewRegistry(), "", 1)
	a.researchPlan.injected = true
	a.researchPlanSubs = []ResearchSubQuery{
		{Perspective: "risk", Query: "stale task-1 query", Priority: 4},
	}
	a.resetTurnDetectors()
	if a.researchPlan.injected {
		t.Error("resetTurnDetectors left researchPlan.injected set: plan never fires again this session")
	}
	if len(a.researchPlanSubs) != 0 {
		t.Errorf("resetTurnDetectors left stale researchPlanSubs (%d entries): task-2 gap detection would consume the task-1 plan", len(a.researchPlanSubs))
	}
}

// #3999: gap detection must keep per-perspective granularity. All planner
// templates start with the goal text, so a goal-prefix probe alone is
// IDENTICAL for every sibling perspective - pre-fix, one matching finding
// marked ALL planned sub-queries covered.
func TestSynthesizeResearchReportPerPerspectiveGaps(t *testing.T) {
	// Finding covers the goal prefix plus only the practical perspective
	// (its taxonomy keyword "tutorial"); no risk vocabulary anywhere.
	findings := []ResearchFinding{
		{Tool: "web_search", Query: "agent memory architecture tutorial", URL: "https://a.io/x", Snippet: "s"},
	}
	planned := []ResearchSubQuery{
		{Perspective: "practical", Query: "agent memory architecture real-world usage and examples", Priority: 3},
		{Perspective: "risk", Query: "agent memory architecture known risks and limitations", Priority: 4},
	}
	draft := SynthesizeResearchReport("goal", findings, planned)
	if strings.Contains(draft, "real-world usage and examples") {
		t.Errorf("practical sub-query is covered (probe hit + keyword 'tutorial') but reported as gap:\n%s", draft)
	}
	if !strings.Contains(draft, "known risks and limitations") {
		t.Errorf("risk sub-query has zero risk-perspective coverage but not reported as gap (per-perspective granularity lost):\n%s", draft)
	}
}

// #3999 regression shape straight from the issue: goal-prefix match alone
// must not mark sibling perspectives covered.
func TestSynthesizeResearchReportGapNotAllOrNothing(t *testing.T) {
	findings := []ResearchFinding{
		{Tool: "web_search", Query: "agent memory architecture", URL: "https://a.io/x", Snippet: "s"},
	}
	planned := []ResearchSubQuery{
		{Perspective: "technical", Query: "agent memory architecture technical details and mechanisms", Priority: 1},
		{Perspective: "risk", Query: "agent memory architecture known risks and limitations", Priority: 4},
	}
	draft := SynthesizeResearchReport("goal", findings, planned)
	// "architecture" is itself in the technical taxonomy, so technical is
	// genuinely covered here.
	if strings.Contains(draft, "technical details and mechanisms") {
		t.Errorf("technical covered (probe hit + taxonomy word 'architecture') but reported as gap:\n%s", draft)
	}
	// Risk is NOT covered: the probe hits but no risk vocabulary appears.
	if !strings.Contains(draft, "known risks and limitations") {
		t.Errorf("risk lacks risk-perspective vocabulary but not reported as gap:\n%s", draft)
	}
}

// #3999: unknown perspectives (e.g. "general", whose query is the bare
// goal) fall back to the goal-prefix probe alone.
func TestSubQueryCoveredGeneralFallback(t *testing.T) {
	p := ResearchSubQuery{Perspective: "general", Query: "vector db market"}
	if !subQueryCovered("vector db market overview", p) {
		t.Error("general perspective must fall back to goal-prefix probe alone")
	}
	if subQueryCovered("completely unrelated finding", p) {
		t.Error("probe miss must report a gap")
	}
	if !subQueryCovered("anything", ResearchSubQuery{Perspective: "technical", Query: ""}) {
		t.Error("empty query has nothing to probe; must be treated as covered")
	}
}
