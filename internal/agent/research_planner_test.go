package agent

import (
	"strings"
	"testing"
)

func TestPlanResearchSubQueries_PerspectiveHit(t *testing.T) {
	subs := PlanResearchSubQueries("Compare how RAG vs long-context LLM architectures handle memory", 0)
	if len(subs) == 0 {
		t.Fatal("perspective keywords matched, want non-empty plan")
	}
	seen := map[string]bool{}
	for _, s := range subs {
		if s.Query == "" || s.Perspective == "" {
			t.Fatalf("sub-query missing fields: %+v", s)
		}
		seen[s.Perspective] = true
	}
	// "how" -> technical, "compare"/" vs" -> comparison: both must fire.
	if !seen["technical"] {
		t.Errorf("technical perspective not matched: %v", seen)
	}
	if !seen["comparison"] {
		t.Errorf("comparison perspective not matched: %v", seen)
	}
}

func TestPlanResearchSubQueries_ChineseGoal(t *testing.T) {
	subs := PlanResearchSubQueries("研究向量数据库的选型对比与实践风险", 0)
	if len(subs) == 0 {
		t.Fatal("Chinese perspective keywords matched, want non-empty plan")
	}
	found := false
	for _, s := range subs {
		if s.Perspective == "comparison" || s.Perspective == "risk" || s.Perspective == "practical" {
			found = true
		}
	}
	if !found {
		t.Errorf("no Chinese keyword perspective fired: %+v", subs)
	}
}

func TestPlanResearchSubQueries_NoPerspectiveFallsBackToGeneral(t *testing.T) {
	subs := PlanResearchSubQueries("latest news about Mars rovers", 0)
	if len(subs) != 1 || subs[0].Perspective != "general" {
		t.Fatalf("unmatched goal should yield one general seed, got %+v", subs)
	}
	if subs[0].Query != "latest news about Mars rovers" {
		t.Errorf("general seed must be the goal verbatim, got %q", subs[0].Query)
	}
}

func TestPlanResearchSubQueries_EmptyGoal(t *testing.T) {
	if subs := PlanResearchSubQueries("   ", 6); subs != nil {
		t.Fatalf("empty goal must yield nil, got %+v", subs)
	}
}

func TestPlanResearchSubQueries_Cap(t *testing.T) {
	// Hit all four perspectives (8 sub-queries uncapped) with cap 3.
	subs := PlanResearchSubQueries("compare how to use it and its security risks and design", 3)
	if len(subs) != 3 {
		t.Fatalf("cap must be honored, got %d", len(subs))
	}
}

func TestPlanResearchSubQueries_NegativeCapUsesDefault(t *testing.T) {
	subs := PlanResearchSubQueries("compare how to use it and its security risks and design", -1)
	if len(subs) == 0 || len(subs) > defaultResearchSubQueryCap {
		t.Fatalf("cap<=0 must fall back to default cap, got %d", len(subs))
	}
}

func TestResearchPlanText_Renders(t *testing.T) {
	msg := researchPlanText([]ResearchSubQuery{
		{Perspective: "technical", Query: "Q1", Priority: 1},
		{Perspective: "risk", Query: "Q2", Priority: 4},
	})
	for _, want := range []string{"[Research Plan]", "1. [technical] Q1", "2. [risk] Q2"} {
		if !strings.Contains(msg, want) {
			t.Errorf("plan text missing %q:\n%s", want, msg)
		}
	}
	if researchPlanText(nil) != "" {
		t.Error("empty plan must render empty text")
	}
}

func TestResearchPlanState_FireOnce(t *testing.T) {
	p := &researchPlanState{}
	if !p.shouldInject() {
		t.Fatal("first call must allow injection")
	}
	if p.shouldInject() {
		t.Fatal("second call must deny injection")
	}
}
