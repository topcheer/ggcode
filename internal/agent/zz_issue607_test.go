package agent

// Issue #607 tests: guidance coalescer + budget integration fixes.
//
// B1: isCriticalTag case-normalization ("Hardcoded-Secret" Pascal form
//     previously missed criticalHintTags because the fallback probed
//     ToUpper(tag) against lowercase-hyphen map keys).
// B2: applyToolResultGuidance now routes through the per-turn guidance
//     budget (previously it bypassed guidanceBudget.allow entirely).
// B3: conflict meta-hint counts against the cap, is deduplicated across
//     tool results, and conflict detection strips self-injected
//     [guidance-coalesced] summaries.

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/tool"
)

// --- B1: isCriticalTag case normalization ---

func TestIssue607_B1_IsCriticalTagCaseInsensitive(t *testing.T) {
	// Pascal/Title forms must match lowercase-hyphen map keys.
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

	// Exact-match-only semantics preserved (#441): lookalike tags still
	// must NOT inherit critical exemption.
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
	// A Pascal-case critical hint must survive the per-result cap and not
	// be relegated to the suppression summary.
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
	// Exhaust the budget.
	for i := 0; i < guidanceBudgetPerTurn; i++ {
		if !b.allow("[advisory] filler directive") {
			t.Fatalf("advisory hint %d unexpectedly suppressed before cap", i)
		}
	}
	// Pascal-form critical hint must still bypass the exhausted budget.
	if !b.allow("[Hardcoded-Secret] security: secret literal detected") {
		t.Error("critical Pascal-form hint suppressed after budget exhausted; want budget bypass")
	}
	// Sanity: all-critical tags still bypass (regression guard).
	if !b.allow("[CRITICAL] something went very wrong") {
		t.Error("[CRITICAL] hint suppressed after budget exhausted; want budget bypass")
	}
}

// --- B2: applyToolResultGuidance goes through the per-turn budget ---

// issue607Agent builds a bare Agent with a fresh guidance budget, enough
// for exercising applyToolResultGuidance without a full Agent stack.
func issue607Agent() *Agent {
	a := &Agent{}
	a.guidanceBudget.reset()
	return a
}

func TestIssue607_B2_ToolResultHintsRespectBudget(t *testing.T) {
	a := issue607Agent()

	// Fill the budget with tool-result hints. Each call injects 1 hint
	// (coalesceMaxHints=1), so after guidanceBudgetPerTurn results the
	// budget must start suppressing.
	for i := 0; i < guidanceBudgetPerTurn; i++ {
		res := &tool.Result{Content: "ok"}
		a.applyToolResultGuidance(res, "", "[advisory-"+string(rune('a'+i))+"] hint number "+string(rune('a'+i)), "", "")
		if !strings.Contains(res.Content, "[advisory-") {
			t.Fatalf("result %d: advisory hint unexpectedly suppressed before budget cap; content=%q", i, res.Content)
		}
	}

	// Next result in the SAME turn must be suppressed by the budget.
	res := &tool.Result{Content: "ok"}
	a.applyToolResultGuidance(res, "", "[advisory-over] should be suppressed", "", "")
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
		a.applyToolResultGuidance(res, "", "[advisory-"+string(rune('a'+i))+"] filler", "", "")
	}
	// Critical hint still injects even with the budget exhausted.
	res := &tool.Result{Content: "ok"}
	a.applyToolResultGuidance(res, "[CRITICAL] critical failure detected", "", "", "")
	if !strings.Contains(res.Content, "[CRITICAL]") {
		t.Error("critical hint suppressed by exhausted budget; want bypass")
	}
}

// --- B3: conflict meta-hint cap counting + cross-result dedup + summary stripping ---

func TestIssue607_B3_ConflictHintCountsTowardCap(t *testing.T) {
	a := issue607Agent()
	res := &tool.Result{Content: "ok"}
	// Two conflicting hints: coalesce keeps 1 advisory; conflict detection
	// then prepends a [guidance-conflict] meta-hint. The result may carry
	// the conflict hint + the surviving hint, but total guidance messages
	// in this tool result must not exceed the budget accounting — i.e. the
	// conflict hint consumed a budget slot, visible when the very next
	// result's hint is suppressed after exactly guidanceBudgetPerTurn
	// total injections across the turn.
	a.applyToolResultGuidance(res, "",
		"[analysis-paralysis] ACT NOW: make your best-guess edit.",
		"[explore-expand] Explore more to understand before editing.", "")

	injectedMsgs := a.guidanceBudget.injected
	if injectedMsgs > guidanceBudgetPerTurn {
		t.Errorf("budget injected=%d exceeds cap %d (conflict meta-hint not counted)", injectedMsgs, guidanceBudgetPerTurn)
	}
	if injectedMsgs < 2 {
		t.Errorf("budget injected=%d, expected conflict meta-hint + surviving hint to consume slots", injectedMsgs)
	}
}

func TestIssue607_B3_MetaHintDedupedAcrossResults(t *testing.T) {
	a := issue607Agent()

	conflict := "[analysis-paralysis] ACT NOW: make your best-guess edit."

	// First result in the turn: the hint is injected.
	res1 := &tool.Result{Content: "ok"}
	a.applyToolResultGuidance(res1, "", conflict, "", "")
	if !strings.Contains(res1.Content, "[analysis-paralysis]") {
		t.Fatalf("first result: expected advisory hint; content=%q", res1.Content)
	}

	// Second and third results with the same hint in the SAME turn: the
	// duplicate tag must be suppressed (cross-result dedup).
	for i := 2; i <= 3; i++ {
		res := &tool.Result{Content: "ok"}
		a.applyToolResultGuidance(res, "", conflict, "", "")
		if strings.Contains(res.Content, "[analysis-paralysis]") {
			t.Errorf("result %d: duplicate hint repeated across tool results; content=%q", i, res.Content)
		}
		if strings.Contains(res.Content, "[guidance-coalesced]") {
			t.Errorf("result %d: duplicate suppression summary injected; content=%q", i, res.Content)
		}
	}
}

func TestIssue607_B3_ConflictDetectionStripsCoalescedSummaries(t *testing.T) {
	// Through applyToolResultGuidance, a summary naming an "explore" style
	// tag must not fabricate a [guidance-conflict] against a retained
	// "act now" hint (the pre-fix pipeline prepended a bogus meta-hint).
	a := issue607Agent()
	res := &tool.Result{Content: "ok"}
	a.applyToolResultGuidance(res, "",
		"[analysis-paralysis] ACT NOW: make your best-guess edit.",
		"[explore-expand] Explore more to understand before editing.", "")
	if strings.Contains(res.Content, "[guidance-conflict]") {
		t.Errorf("pseudo-conflict fabricated from [guidance-coalesced] summary: %q", res.Content)
	}

	// Stripped input must yield no conflict even though the raw hint list
	// pairs "ACT NOW" with a summary quoting explore tags.
	hints := []string{
		"[analysis-paralysis] ACT NOW: make your best-guess edit.",
		"[guidance-coalesced] 3 additional guidance message(s) suppressed to prevent alert overload: [exploration-expansion], [read-more], [broaden-scope] (+1 more)",
	}
	if ch := detectGuidanceConflict(stripCoalescedSummaries(hints)); ch != "" {
		t.Errorf("pseudo-conflict fabricated from stripped-summary input: %q", ch)
	}

	// Sanity: raw (unstripped) input DOES fabricate one — proving the
	// strip is what closes the loop and the fixture still reproduces the bug.
	if ch := detectGuidanceConflict(hints); ch == "" {
		t.Error("expected raw (unstripped) input to fabricate a pseudo-conflict; test fixture no longer reproduces the bug")
	}

	// The stripping helper itself.
	stripped := stripCoalescedSummaries(hints)
	if len(stripped) != 1 {
		t.Fatalf("stripCoalescedSummaries returned %d hints, want 1: %v", len(stripped), stripped)
	}
	if strings.Contains(strings.Join(stripped, " "), "guidance-coalesced") {
		t.Error("stripCoalescedSummaries left a [guidance-coalesced] summary behind")
	}

	// A REAL conflict (two retained directives) must still be detected.
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
	// coalesceMaxHints=1 per result. With a conflict pair, the output is
	// conflict meta-hint + surviving hint = at most 2 messages, but only
	// because the conflict hint is prepended — it must not stack a THIRD
	// message beyond the cap.
	res := &tool.Result{Content: "ok"}
	a.applyToolResultGuidance(res, "",
		"[analysis-paralysis] ACT NOW: make your best-guess edit.",
		"[explore-expand] Explore more to understand before editing.", "")

	messages := strings.Count(res.Content, "\n\n")
	// content "ok" + N injected hints => N-1 separators of the hints
	// themselves plus 1 separator after "ok". Cap: conflict(1) + retained(1).
	if messages > 3 { // "ok" sep + conflict + retained = 3 separators max
		t.Errorf("too many guidance messages in result (cap bypass): %q", res.Content)
	}
}
