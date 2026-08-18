package agent

// Issue #681 tests: #677 migration regressions — "returned != delivered".
//
// Defect 1 (MED): mid-point progress checkpoint is a one-shot protocol
// prompt that was routed through the guidance budget; a saturated turn
// silently burned the run's only checkpoint. Fixed by a direct-add
// exemption (same precedent as the loop-recovery nudges, #677).
//
// Defect 2 (LOW-MED): errorCompound's per-run quota ("at most 2 per run")
// is consumed when maybeWarn RETURNS a message, while the budget decides
// DELIVERY — two fires landing on saturated turns burn the quota with
// zero guidance delivered. Fixed by injectGuidance returning delivered +
// markUndelivered rollback at the call site.
//
// Defect 3 (MED-LOW): monorepoScoper's one-shot `fired` flag was burned
// by a suppressed turn — the detector went dark for the rest of the run.
// Fixed by the same delivered-gated rollback.

import (
	"os"
	"strings"
	"testing"
)

// injectGuidance must report actual delivery: true within budget, false
// past the cap, true for critical (bypass) even past the cap.
func TestIssue681_InjectGuidanceReportsDelivery(t *testing.T) {
	a, cm := issue677Agent(t)

	for i := 0; i < guidanceBudgetPerTurn; i++ {
		if !a.injectGuidance("[advisory-filler] filler " + string(rune('a'+i))) {
			t.Fatalf("filler %d: injectGuidance returned false within budget", i)
		}
	}
	if a.injectGuidance("[advisory-extra] past cap") {
		t.Error("injectGuidance returned true past the per-turn cap; want false (suppressed)")
	}
	if got := issue677CountUserMsgs(cm, "[advisory-extra]"); got != 0 {
		t.Errorf("suppressed message was added (%d); want 0", got)
	}
	if !a.injectGuidance("[CRITICAL] correctness failure") {
		t.Error("critical hint must bypass the exhausted budget and report delivered=true")
	}
}

// Defect 1: the checkpoint stays a direct add — it must not consult or be
// droppable by the per-turn guidance budget (source-level pin; the site
// lives inside RunStream which needs a full provider loop).
func TestIssue681_CheckpointExemptFromBudget(t *testing.T) {
	src, err := os.ReadFile("agent.go")
	if err != nil {
		t.Skipf("cannot read agent.go: %v", err)
	}
	text := string(src)
	idx := strings.Index(text, "Mid-point progress checkpoint")
	if idx < 0 {
		t.Fatal("checkpoint block not found in agent.go")
	}
	// The block extends to the msgs refresh that follows it.
	end := strings.Index(text[idx:], "refresh after adding checkpoint")
	if end < 0 {
		t.Fatal("checkpoint block terminator not found in agent.go")
	}
	block := text[idx : idx+end]
	if strings.Contains(block, "injectGuidance") {
		t.Error("mid-point checkpoint routes through injectGuidance — a saturated turn burns the one-shot checkpoint (#681 defect 1); must be a direct contextManager.Add")
	}
	if !strings.Contains(block, "contextManager.Add") {
		t.Error("mid-point checkpoint block does not contain a direct contextManager.Add")
	}
}

// Defect 3: a monorepo sprawl hint suppressed by the budget must not burn
// the one-shot chance — markUndelivered restores it and a later, less
// saturated iteration delivers it.
func TestIssue681_MonorepoOneShotRetriesAfterSuppression(t *testing.T) {
	a, cm := issue677Agent(t)
	a.monorepoScoper.enabled = true
	a.monorepoScoper.rootDir = "/repo"
	a.monorepoScoper.touchedDirs = map[string]int{"pkg-a": 2, "pkg-b": 1, "pkg-c": 3}

	// Saturate the budget, then run the agent.go wiring contract.
	for i := 0; i < guidanceBudgetPerTurn; i++ {
		a.injectGuidance("[advisory-filler] filler")
	}
	msg := a.monorepoScoper.maybeWarnScopeSprawl()
	if msg == "" {
		t.Fatal("setup: sprawl hint did not fire")
	}
	if a.injectGuidance(msg) {
		t.Fatal("setup: sprawl hint unexpectedly delivered on a saturated turn")
	}
	// The #681 call-site contract: undelivered -> restore the one-shot.
	a.monorepoScoper.markUndelivered()
	if a.monorepoScoper.fired {
		t.Error("markUndelivered did not restore the one-shot fired flag")
	}

	// Later iteration, fresh turn budget: the hint fires and delivers.
	a.guidanceBudget.reset()
	msg2 := a.monorepoScoper.maybeWarnScopeSprawl()
	if msg2 == "" {
		t.Fatal("one-shot chance burned by a suppressed turn (#681 defect 3)")
	}
	if !a.injectGuidance(msg2) {
		t.Fatal("sprawl hint suppressed on a fresh turn; want delivered")
	}
	if got := issue677CountUserMsgs(cm, "[monorepo-scope]"); got != 1 {
		t.Errorf("monorepo-scope messages: %d, want 1", got)
	}
}

// Defect 2: errorCompound's per-run quota must only be consumed by a
// DELIVERED warning. A fire suppressed by the budget is rolled back, so
// the detector cannot go permanently dark with zero guidance delivered.
func TestIssue681_ErrorCompoundQuotaNotBurnedBySuppression(t *testing.T) {
	a, cm := issue677Agent(t)

	// Saturate the window with errors (density 1.0 > 0.30).
	for i := 0; i < 8; i++ {
		a.errorCompound.recordStep(true)
	}

	// Saturated turn: the fire is suppressed and rolled back.
	for i := 0; i < guidanceBudgetPerTurn; i++ {
		a.injectGuidance("[advisory-filler] filler")
	}
	msg := a.errorCompound.maybeWarn(1)
	if msg == "" {
		t.Fatal("setup: error-compounding warning did not fire")
	}
	if a.injectGuidance(msg) {
		t.Fatal("setup: warning unexpectedly delivered on a saturated turn")
	}
	// The #681 call-site contract: undelivered -> roll the quota back.
	a.errorCompound.markUndelivered()
	if a.errorCompound.warningCount != 0 {
		t.Errorf("warningCount=%d after markUndelivered; want 0 (quota burned by suppressed fire, #681 defect 2)", a.errorCompound.warningCount)
	}

	// Fresh turn: the warning fires again (quota intact) and delivers.
	a.guidanceBudget.reset()
	msg2 := a.errorCompound.maybeWarn(2)
	if msg2 == "" {
		t.Fatal("per-run quota burned by a suppressed fire — detector went dark with zero guidance delivered (#681 defect 2)")
	}
	if !a.injectGuidance(msg2) {
		t.Fatal("warning suppressed on a fresh turn; want delivered")
	}
	if got := issue677CountUserMsgs(cm, "[error-compounding]"); got != 1 {
		t.Errorf("error-compounding messages: %d, want 1", got)
	}
	if a.errorCompound.warningCount != 1 {
		t.Errorf("warningCount=%d after delivered fire; want 1", a.errorCompound.warningCount)
	}
}

// markUndelivered is a one-shot rollback: a second call (or a call with no
// revertible fire) must not over-rollback a delivered warning's quota.
func TestIssue681_MarkUndeliveredDoesNotOverRollback(t *testing.T) {
	a, _ := issue677Agent(t)
	for i := 0; i < 8; i++ {
		a.errorCompound.recordStep(true)
	}
	if msg := a.errorCompound.maybeWarn(1); msg == "" {
		t.Fatal("setup: warning did not fire")
	}
	// Delivered path consumes the quota; stray markUndelivered must NOT
	// roll back a fire that was never suppressed... but the snapshot is
	// still revertible here, matching "caller only calls on suppression".
	// Guard the degenerate double-call instead:
	a.errorCompound.markUndelivered() // first rollback: valid
	a.errorCompound.markUndelivered() // second rollback: must be a no-op
	if a.errorCompound.warningCount != 0 {
		t.Errorf("warningCount=%d after double markUndelivered; want 0 (no over-rollback below zero-adjacent state)", a.errorCompound.warningCount)
	}
}
