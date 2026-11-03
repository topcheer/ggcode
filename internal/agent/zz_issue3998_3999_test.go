package agent

import (
	"strings"
	"testing"
)

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

func TestSynthesizeResearchReportPerPerspectiveGaps(t *testing.T) {

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

func TestSynthesizeResearchReportGapNotAllOrNothing(t *testing.T) {
	findings := []ResearchFinding{
		{Tool: "web_search", Query: "agent memory architecture", URL: "https://a.io/x", Snippet: "s"},
	}
	planned := []ResearchSubQuery{
		{Perspective: "technical", Query: "agent memory architecture technical details and mechanisms", Priority: 1},
		{Perspective: "risk", Query: "agent memory architecture known risks and limitations", Priority: 4},
	}
	draft := SynthesizeResearchReport("goal", findings, planned)

	if strings.Contains(draft, "technical details and mechanisms") {
		t.Errorf("technical covered (probe hit + taxonomy word 'architecture') but reported as gap:\n%s", draft)
	}

	if !strings.Contains(draft, "known risks and limitations") {
		t.Errorf("risk lacks risk-perspective vocabulary but not reported as gap:\n%s", draft)
	}
}

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
