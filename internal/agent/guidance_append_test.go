package agent

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/tool"
)

// Batch 3 of the guidance-noise cleanup: tool-result hint appends now go
// through the unified per-turn guidance budget (appendGuidance), instead of
// bypassing it with direct Content concatenation.

func TestAppendGuidanceWithinBudget(t *testing.T) {
	a := NewAgent(nil, tool.NewRegistry(), "", 5)
	a.guidanceBudget.reset()

	res := &tool.Result{Content: "ok"}
	if !a.appendGuidance(res, "[advisory-1] first hint") {
		t.Fatal("first hint should be appended within budget")
	}
	if !strings.Contains(res.Content, "[advisory-1]") {
		t.Fatalf("hint missing from result: %q", res.Content)
	}
}

func TestAppendGuidanceDedupesTag(t *testing.T) {
	a := NewAgent(nil, tool.NewRegistry(), "", 5)
	a.guidanceBudget.reset()

	res := &tool.Result{Content: "ok"}
	a.appendGuidance(res, "[dup-tag] same hint")
	res2 := &tool.Result{Content: "ok2"}
	if a.appendGuidance(res2, "[dup-tag] same hint") {
		t.Fatal("duplicate-tag hint must be suppressed across results in the same turn")
	}
}

func TestAppendGuidanceBudgetCap(t *testing.T) {
	a := NewAgent(nil, tool.NewRegistry(), "", 5)
	a.guidanceBudget.reset()

	allowed := 0
	for i := 0; i < guidanceBudgetPerTurn+3; i++ {
		res := &tool.Result{Content: "ok"}
		if a.appendGuidance(res, "[cap-"+strings.Repeat("x", i+1)+"] distinct hint") {
			allowed++
		}
	}
	if allowed != guidanceBudgetPerTurn {
		t.Fatalf("expected exactly %d appends within budget, got %d", guidanceBudgetPerTurn, allowed)
	}
}

func TestAppendGuidanceCriticalBypass(t *testing.T) {
	a := NewAgent(nil, tool.NewRegistry(), "", 5)
	a.guidanceBudget.reset()

	// Burn the whole budget with advisory hints.
	for i := 0; i < guidanceBudgetPerTurn; i++ {
		a.appendGuidance(&tool.Result{Content: "x"}, "[burn-"+strings.Repeat("y", i+1)+"] advisory")
	}
	res := &tool.Result{Content: "ok"}
	if !a.appendGuidance(res, "[CRITICAL] must pass even with budget exhausted") {
		t.Fatal("critical hint must bypass the budget cap")
	}
}
