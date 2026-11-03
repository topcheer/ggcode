package agent

import (
	"testing"

	ctxpkg "github.com/topcheer/ggcode/internal/context"
)

func TestMaybeInjectResearchPlanSecondRunReplans(t *testing.T) {
	a := &Agent{contextManager: ctxpkg.NewManager(200000)}
	researchText := "research the architecture of Go build system and write a survey report"
	if !detectResearchMode(researchText) {
		t.Fatal("test premise broken: research-mode detector no longer fires on plain research request text - update the fixed text to re-lock the e2e scenario")
	}
	a.maybeInjectResearchPlan(researchText)
	if len(a.researchPlanSubs) == 0 {
		t.Fatal("run 1: expected researchPlanSubs populated after injection")
	}
	a.researchPlan.reset()
	a.researchPlanSubs = nil
	a.maybeInjectResearchPlan("research database indexing strategies and compare them in a report")
	if len(a.researchPlanSubs) == 0 {
		t.Fatal("run 2: expected fresh plan injection after per-run reset (#4023)")
	}
}
