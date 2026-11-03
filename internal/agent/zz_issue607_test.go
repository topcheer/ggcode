package agent

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/tool"
)

func TestIssue607_B1_IsCriticalTagCaseInsensitive(t *testing.T) {

	for _, tag := range []string{
		"Hardcoded-Secret",
		"hardcoded-secret",
		"HARDCODED-SECRET",
		"Hardcoded-secret",
		"Path-Traversal",
		"path-traversal",
		"Git-Destructive",
		"git-destructive",
		"Pre-Commit-Build-Gate",
		"pre-commit-build-gate",
	} {
		if !isCriticalTag(tag) {
			t.Errorf("isCriticalTag(%q) = false, want true (case-insensitive match)", tag)
		}
	}

	for _, tag := range []string{
		"Hardcoded-Secret-Tip",
		"hardcoded-secrets",
		"Security-Tip",
		"Blocked-For-Now",
		"",
	} {
		if isCriticalTag(tag) {
			t.Errorf("isCriticalTag(%q) = true, want false (exact match only)", tag)
		}
	}
}

func TestIssue607_B1_CoalesceRetainsPascalCriticalTag(t *testing.T) {

	hints := []string{
		"[advisory-one] first advisory directive text",
		"[advisory-two] second advisory directive text",
		"[Hardcoded-Secret] security: secret literal detected in source",
		"[advisory-three] third advisory directive text",
	}
	got := coalesceGuidance(hints)

	found := false
	for _, h := range got {
		if strings.Contains(h, "Hardcoded-Secret") && !strings.HasPrefix(h, "[guidance-coalesced]") {
			found = true
		}
	}
	if !found {
		t.Errorf("critical Pascal-form hint suppressed by cap; got: %v", got)
	}
}

func TestIssue607_B1_BudgetBypassForPascalCriticalHint(t *testing.T) {
	var b guidanceBudget

	for i := 0; i < guidanceBudgetPerTurn; i++ {
		if !b.allow("[advisory] filler directive") {
			t.Fatalf("advisory hint %d unexpectedly suppressed before cap", i)
		}
	}

	if !b.allow("[Hardcoded-Secret] security: secret literal detected") {
		t.Error("critical Pascal-form hint suppressed after budget exhausted; want budget bypass")
	}

	if !b.allow("[CRITICAL] something went very wrong") {
		t.Error("[CRITICAL] hint suppressed after budget exhausted; want budget bypass")
	}
}

func issue607Agent() *Agent {
	a := &Agent{}
	a.guidanceBudget.reset()
	return a
}

func TestIssue607_B2_ToolResultHintsRespectBudget(t *testing.T) {
	a := issue607Agent()

	for i := 0; i < guidanceBudgetPerTurn; i++ {
		res := &tool.Result{Content: "ok"}
		a.applyToolResultGuidance(res, "", "[advisory-"+string(rune('a'+i))+"] hint number "+string(rune('a'+i)), "", "", "")
		if !strings.Contains(res.Content, "[advisory-") {
			t.Fatalf("result %d: advisory hint unexpectedly suppressed before budget cap; content=%q", i, res.Content)
		}
	}

	res := &tool.Result{Content: "ok"}
	a.applyToolResultGuidance(res, "", "[advisory-over] should be suppressed", "", "", "")
	if strings.Contains(res.Content, "[advisory-over]") {
		t.Errorf("tool-result hint bypassed per-turn budget cap (B2); content=%q", res.Content)
	}
	if res.Content != "ok" {
		t.Errorf("suppressed result content mutated: %q", res.Content)
	}
}

func TestIssue607_B2_CriticalHintPassesExhaustedBudget(t *testing.T) {
	a := issue607Agent()
	for i := 0; i < guidanceBudgetPerTurn; i++ {
		res := &tool.Result{Content: "ok"}
		a.applyToolResultGuidance(res, "", "[advisory-"+string(rune('a'+i))+"] filler", "", "", "")
	}

	res := &tool.Result{Content: "ok"}
	a.applyToolResultGuidance(res, "[CRITICAL] critical failure detected", "", "", "", "")
	if !strings.Contains(res.Content, "[CRITICAL]") {
		t.Error("critical hint suppressed by exhausted budget; want bypass")
	}
}

func TestIssue607_B3_ConflictHintCountsTowardCap(t *testing.T) {
	a := issue607Agent()
	res := &tool.Result{Content: "ok"}

	a.applyToolResultGuidance(res, "",
		"[analysis-paralysis] ACT NOW: make your best-guess edit.",
		"[explore-expand] Explore more to understand before editing.", "", "")

	injectedMsgs := a.guidanceBudget.injected
	if injectedMsgs > guidanceBudgetPerTurn {
		t.Errorf("budget injected=%d exceeds cap %d (conflict meta-hint not counted)", injectedMsgs, guidanceBudgetPerTurn)
	}
	if injectedMsgs < 2 {
		t.Errorf("budget injected=%d, expected conflict meta-hint + surviving hint to consume slots", injectedMsgs)
	}
}

func TestIssue607_B3_ExemptHintBypassesCapWithoutConsumingBudget(t *testing.T) {
	a := issue607Agent()

	for i := 0; i < guidanceBudgetPerTurn; i++ {
		res := &tool.Result{Content: "ok"}
		a.applyToolResultGuidance(res, "", "[advisory-"+string(rune('a'+i))+"] filler", "", "", "")
	}
	before := a.guidanceBudget.injected

	res := &tool.Result{Content: "ok"}
	a.applyToolResultGuidance(res, "", "[guidance-conflict] contradictory directives detected; proceed with the retained hint", "", "", "")
	if !strings.Contains(res.Content, "[guidance-conflict]") {
		t.Error("exempt hint suppressed by exhausted budget; want bypass")
	}

	if a.guidanceBudget.injected != before {
		t.Errorf("exempt hint consumed budget: injected %d -> %d", before, a.guidanceBudget.injected)
	}
}

func TestIssue607_B3_MetaHintDedupedAcrossResults(t *testing.T) {
	a := issue607Agent()

	conflict := "[analysis-paralysis] ACT NOW: make your best-guess edit."

	res1 := &tool.Result{Content: "ok"}
	a.applyToolResultGuidance(res1, "", conflict, "", "", "")
	if !strings.Contains(res1.Content, "[analysis-paralysis]") {
		t.Fatalf("first result: expected advisory hint; content=%q", res1.Content)
	}

	for i := 2; i <= 3; i++ {
		res := &tool.Result{Content: "ok"}
		a.applyToolResultGuidance(res, "", conflict, "", "", "")
		if strings.Contains(res.Content, "[analysis-paralysis]") {
			t.Errorf("result %d: duplicate hint repeated across tool results; content=%q", i, res.Content)
		}
		if strings.Contains(res.Content, "[guidance-coalesced]") {
			t.Errorf("result %d: duplicate suppression summary injected; content=%q", i, res.Content)
		}
	}
}

func TestIssue607_B3_ConflictDetectionStripsCoalescedSummaries(t *testing.T) {

	a := issue607Agent()
	res := &tool.Result{Content: "ok"}
	a.applyToolResultGuidance(res, "",
		"[analysis-paralysis] ACT NOW: make your best-guess edit.",
		"[explore-expand] Explore more to understand before editing.", "", "")
	if strings.Contains(res.Content, "[guidance-conflict]") {
		t.Errorf("pseudo-conflict fabricated from [guidance-coalesced] summary: %q", res.Content)
	}

	hints := []string{
		"[analysis-paralysis] ACT NOW: make your best-guess edit.",
		"[guidance-coalesced] 3 additional guidance message(s) suppressed to prevent alert overload: [exploration-expansion], [read-more], [broaden-scope] (+1 more)",
	}
	if ch := detectGuidanceConflict(stripCoalescedSummaries(hints)); ch != "" {
		t.Errorf("pseudo-conflict fabricated from stripped-summary input: %q", ch)
	}

	if ch := detectGuidanceConflict(hints); ch == "" {
		t.Error("expected raw (unstripped) input to fabricate a pseudo-conflict; test fixture no longer reproduces the bug")
	}

	stripped := stripCoalescedSummaries(hints)
	if len(stripped) != 1 {
		t.Fatalf("stripCoalescedSummaries returned %d hints, want 1: %v", len(stripped), stripped)
	}
	if strings.Contains(strings.Join(stripped, " "), "guidance-coalesced") {
		t.Error("stripCoalescedSummaries left a [guidance-coalesced] summary behind")
	}

	real := []string{
		"[analysis-paralysis] ACT NOW: make your best-guess edit.",
		"[explore-expand] Explore more to understand before editing.",
	}
	if ch := detectGuidanceConflict(real); ch == "" {
		t.Error("real conflict between retained hints not detected after fix")
	}
}

func TestIssue607_B3_ConflictHintDoesNotExceedCap(t *testing.T) {
	a := issue607Agent()

	res := &tool.Result{Content: "ok"}
	a.applyToolResultGuidance(res, "",
		"[analysis-paralysis] ACT NOW: make your best-guess edit.",
		"[explore-expand] Explore more to understand before editing.", "", "")

	messages := strings.Count(res.Content, "\n\n")

	if messages > 3 {
		t.Errorf("too many guidance messages in result (cap bypass): %q", res.Content)
	}
}

func TestIssue607_B3_ConflictDetectedAcrossResults(t *testing.T) {
	a := issue607Agent()

	res1 := &tool.Result{Content: "ok"}
	a.applyToolResultGuidance(res1, "", "[explore-expand] Explore more to understand before editing.", "", "", "")
	if !strings.Contains(res1.Content, "[explore-expand]") {
		t.Fatalf("first result: expected advisory hint; content=%q", res1.Content)
	}

	res2 := &tool.Result{Content: "ok"}
	a.applyToolResultGuidance(res2, "", "[analysis-paralysis] ACT NOW: make your best-guess edit.", "", "", "")
	if !strings.Contains(res2.Content, "[guidance-conflict]") {
		t.Errorf("conflicting hint delivered on a later tool result did not trigger conflict meta-hint; content=%q", res2.Content)
	}
}
