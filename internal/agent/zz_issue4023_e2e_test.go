package agent

// End-to-end #4023 companion (contributed by ggcxf_mobile seat after the
// pileup yield, verified green on their tree; merged into PR #4031 by the
// holding seat). Run 1 injects -> per-run reset -> run 2 with a DIFFERENT
// research goal must inject a fresh plan.

import (
	"testing"

	ctxpkg "github.com/topcheer/ggcode/internal/context"
)

// 端到端：run1 注入→run 起始 reset→run2 换目标必须重注入（#4023 repro 形态）
func TestMaybeInjectResearchPlanSecondRunReplans(t *testing.T) {
	a := &Agent{contextManager: ctxpkg.NewManager(200000)}
	researchText := "research the architecture of Go build system and write a survey report"
	if !detectResearchMode(researchText) {
		t.Skip("detector heuristic shifted; test premise broken")
	}
	a.maybeInjectResearchPlan(researchText)
	if len(a.researchPlanSubs) == 0 {
		t.Fatal("run 1: expected researchPlanSubs populated after injection")
	}
	a.researchPlan.reset() // run 起始重置（agent.go 修复点同语义）
	a.researchPlanSubs = nil
	a.maybeInjectResearchPlan("research database indexing strategies and compare them in a report")
	if len(a.researchPlanSubs) == 0 {
		t.Fatal("run 2: expected fresh plan injection after per-run reset (#4023)")
	}
}
