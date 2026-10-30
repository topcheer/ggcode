package agent

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/tool"
)

// Agent-level integration for the r343/sa-130 research planner wiring:
// research-mode text injects the plan ONCE and persists the planned
// sub-queries; non-research text is a no-op.

func TestMaybeInjectResearchPlan_FireOnceAndPersist(t *testing.T) {
	a := NewAgent(&mockProvider{}, tool.NewRegistry(), "", 5)
	before := len(a.contextManager.Messages())

	a.maybeInjectResearchPlan("research the latest agent memory architectures")
	msgs := a.contextManager.Messages()
	if len(msgs) != before+1 {
		t.Fatalf("research-mode run must inject exactly one plan message, delta=%d", len(msgs)-before)
	}
	last := msgs[len(msgs)-1]
	if len(last.Content) == 0 || !strings.Contains(last.Content[0].Text, "[Research Plan]") {
		t.Errorf("injected message missing plan header: %+v", last)
	}
	if len(a.researchPlanSubs) == 0 {
		t.Fatal("planned sub-queries must persist for the gate's gap computation")
	}

	// Fire-once: a second research-mode call must not inject again.
	a.maybeInjectResearchPlan("research another unrelated topic")
	if got := len(a.contextManager.Messages()); got != before+1 {
		t.Fatalf("plan injection must fire once per agent, got %d messages after 2nd call", got-before)
	}
}

func TestMaybeInjectResearchPlan_NonResearchNoop(t *testing.T) {
	a := NewAgent(&mockProvider{}, tool.NewRegistry(), "", 5)
	before := len(a.contextManager.Messages())

	a.maybeInjectResearchPlan("write a hello world function and fix the typo")
	if got := len(a.contextManager.Messages()); got != before {
		t.Fatalf("non-research text must not inject, delta=%d", got-before)
	}
	if a.researchPlanSubs != nil {
		t.Fatalf("non-research run must leave plan empty, got %+v", a.researchPlanSubs)
	}
}
