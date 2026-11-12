package agent

import (
	"strings"
	"testing"
)

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

func TestIssue681_CheckpointExemptFromBudget(t *testing.T) {
	// #5543: the mid-point progress checkpoint moved out of agent.go;
	// scan the stream-loop source files (shape-agnostic file targeting).
	text, err := readStreamLoopSource("agent.go", "run_detector_guidance.go")
	if err != nil {
		t.Skipf("cannot read stream-loop source: %v", err)
	}
	idx := strings.Index(text, "a.maxIter >= 20 && !progressCheckInjected")
	if idx < 0 {
		t.Fatal("checkpoint block not found in agent.go")
	}

	// #5543: the checkpoint block moved into runDetectorGuidance; the
	// block now ends at that method's return (applyAdaptiveEffort runs
	// right after the runDetectorGuidance call in the main loop).
	end := strings.Index(text[idx:], "return msgs, progressCheckInjected")
	if end < 0 {
		t.Fatal("checkpoint block terminator not found")
	}
	block := text[idx : idx+end]
	if strings.Contains(block, "injectGuidance") {
		t.Error("mid-point checkpoint routes through injectGuidance — a saturated turn burns the one-shot checkpoint (#681 defect 1); must be a direct contextManager.Add")
	}
	if !strings.Contains(block, "contextManager.Add") {
		t.Error("mid-point checkpoint block does not contain a direct contextManager.Add")
	}
}

func TestIssue681_MonorepoOneShotRetriesAfterSuppression(t *testing.T) {
	a, cm := issue677Agent(t)
	a.monorepoScoper.enabled = true
	a.monorepoScoper.rootDir = "/repo"
	a.monorepoScoper.touchedDirs = map[string]int{"pkg-a": 2, "pkg-b": 1, "pkg-c": 3}

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

	a.monorepoScoper.markUndelivered()
	if a.monorepoScoper.fired {
		t.Error("markUndelivered did not restore the one-shot fired flag")
	}

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

func TestIssue681_ErrorCompoundQuotaNotBurnedBySuppression(t *testing.T) {
	a, cm := issue677Agent(t)

	for i := 0; i < 8; i++ {
		a.errorCompound.recordStep(true)
	}

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

	a.errorCompound.markUndelivered()
	if a.errorCompound.warningCount != 0 {
		t.Errorf("warningCount=%d after markUndelivered; want 0 (quota burned by suppressed fire, #681 defect 2)", a.errorCompound.warningCount)
	}

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

func TestIssue681_MarkUndeliveredDoesNotOverRollback(t *testing.T) {
	a, _ := issue677Agent(t)
	for i := 0; i < 8; i++ {
		a.errorCompound.recordStep(true)
	}
	if msg := a.errorCompound.maybeWarn(1); msg == "" {
		t.Fatal("setup: warning did not fire")
	}

	a.errorCompound.markUndelivered()
	a.errorCompound.markUndelivered()
	if a.errorCompound.warningCount != 0 {
		t.Errorf("warningCount=%d after double markUndelivered; want 0 (no over-rollback below zero-adjacent state)", a.errorCompound.warningCount)
	}
}
