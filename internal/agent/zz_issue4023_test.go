package agent

import (
	"strings"
	"testing"

	ctxpkg "github.com/topcheer/ggcode/internal/context"
)

// #4023: researchPlanState's fire-once semantics are per RUN, not per Agent
// lifetime. researchPlanState.reset (called at run start together with
// researchPlanSubs=nil) must restore injectability so the second research
// request in the same session gets a fresh plan and a clean sub-query set.
func TestIssue4023ResearchPlanStateResetRestoresInjection(t *testing.T) {
	p := &researchPlanState{}
	if !p.shouldInject() {
		t.Fatal("first call must allow injection")
	}
	if p.shouldInject() {
		t.Fatal("second call must deny injection (fire-once within a run)")
	}
	p.reset()
	if !p.shouldInject() {
		t.Fatal("after reset the next run must allow injection again")
	}
}

// End-to-end guard wiring: a second research run must receive a NEW plan
// injection with a fresh sub-query set, not run1's residual set.
func TestIssue4023SecondRunGetsFreshPlan(t *testing.T) {
	a := &Agent{contextManager: ctxpkg.NewManager(200000)}
	run1 := "compare how to use it and its security risks and design"
	run2 := "investigate the deployment options and their trade-offs"

	// Run 1: plan injected, subs saved.
	a.maybeInjectResearchPlan(run1)
	msgs1 := len(a.contextManager.Messages())
	subs1 := len(a.researchPlanSubs)
	if msgs1 != 1 || subs1 == 0 {
		t.Fatalf("run1: want 1 injected plan and non-empty subs, got %d msgs / %d subs", msgs1, subs1)
	}

	// Same run, second call: denied (fire-once within the run).
	a.maybeInjectResearchPlan(run1)
	if len(a.contextManager.Messages()) != 1 {
		t.Fatal("same-run repeat call must not inject again")
	}

	// Run 2 start: the run-start reset from agent.go (#4023).
	a.researchPlan.reset()
	a.researchPlanSubs = nil

	// Run 2: a NEW plan injects for the new topic and subs repopulate.
	a.maybeInjectResearchPlan(run2)
	msgs := a.contextManager.Messages()
	if len(msgs) != 2 {
		t.Fatalf("run2 must inject a new plan, got %d messages", len(msgs))
	}
	if !strings.Contains(msgs[1].Content[0].Text, "[Research Plan]") {
		t.Fatalf("run2 message must be a research plan, got %q", msgs[1].Content[0].Text)
	}
	if len(a.researchPlanSubs) == 0 {
		t.Fatal("run2 must repopulate researchPlanSubs")
	}
}
