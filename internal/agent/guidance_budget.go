package agent

// Guidance Budget Limiter - Per-Turn Injection Cap
//
// Research basis:
//   - arXiv:2506.05109 "Truly Self-Improving Agents Require Intrinsic
//     Metacognitive Learning" (2025): when too many meta-guidance messages
//     fire simultaneously, the agent's metacognitive bandwidth is overwhelmed.
//   - ACE Framework (ICLR 2026): "context collision" - when 3+ guidance
//     directives arrive in the same turn, the model cannot prioritize.
//   - Anthropic "Context Engineering" (Sep 2025): every token of guidance
//     competes for attention budget.
//   - Google SRE Book Ch.6 (2025 update): alert fatigue - when too many
//     simultaneous alerts fire, each alert's marginal value drops to zero.
//
// Problem: ggcode has 190+ independent detectors. In a single iteration,
// 5-10+ detectors can fire simultaneously, each injecting a separate
// `Role: "user"` message into contextManager. This creates:
//  1. Context pollution: N messages consume N * ~200 tokens of context
//  2. Attention dilution: the model cannot determine which directive matters
//  3. Contradictory guidance: some say "explore more", others say "act now"
//  4. Alert fatigue: the model learns to ignore all guidance
//
// This file provides a per-turn budget that caps total guidance injections.
// When the budget is exceeded, subsequent guidance is silently dropped
// (with a debug log). Critical safety hints bypass the cap.
//
// This complements guidance_coalesce.go (which deduplicates by tag) by
// providing a hard per-turn limit across ALL detectors combined.

import (
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

const (
	// guidanceBudgetPerTurn is the maximum number of non-critical guidance
	// messages injected per agent iteration. When this cap is reached,
	// subsequent advisory guidance is suppressed.
	guidanceBudgetPerTurn = 5
)

// criticalGuidanceKeywords are substrings that mark a guidance message as
// critical/safety-relevant. Messages containing these bypass the budget cap.
var criticalGuidanceKeywords = []string{
	"[CRITICAL",
	"[PERMISSION",
	"[IRREVERSIBLE",
	"[DESTRUCTIVE",
	"[SAFETY",
	"[SECURITY",
	"[BLOCKED",
	"[FATAL",
	"[pre-commit-build-gate",
	"[hardcoded-secret",
	"[path-traversal",
	"[git-destructive",
}

// guidanceBudget tracks how many guidance messages have been injected
// in the current agent iteration.
type guidanceBudget struct {
	injected   int
	suppressed int
}

// reset clears the budget at the start of a new iteration.
func (g *guidanceBudget) reset() {
	if g.suppressed > 0 {
		debug.Log("guidance-budget", "previous turn: %d guidance messages suppressed (budget=%d)",
			g.suppressed, guidanceBudgetPerTurn)
	}
	g.injected = 0
	g.suppressed = 0
}

// allow checks whether a guidance message with the given text should be
// injected. Returns true if the message should proceed (either within
// budget or critical), false if it should be suppressed.
func (g *guidanceBudget) allow(text string) bool {
	// Critical messages always pass through.
	if isCriticalGuidance(text) {
		return true
	}
	if g.injected < guidanceBudgetPerTurn {
		g.injected++
		return true
	}
	g.suppressed++
	return false
}

// isCriticalGuidance returns true if the guidance text contains a
// critical/safety keyword.
func isCriticalGuidance(text string) bool {
	upper := strings.ToUpper(text)
	for _, kw := range criticalGuidanceKeywords {
		if strings.Contains(upper, strings.ToUpper(kw)) {
			return true
		}
	}
	// Also check via the coalescer's tag system for consistency.
	if tag := extractHintTag(text); tag != "" && isCriticalTag(tag) {
		return true
	}
	return false
}

// injectGuidance is the budget-aware replacement for direct
// contextManager.Add(provider.Message{Role: "user", ...}) calls from
// detectors. It checks the per-turn budget before injecting.
func (a *Agent) injectGuidance(text string) {
	if !a.guidanceBudget.allow(text) {
		debug.Log("guidance-budget", "suppressing guidance message (budget exceeded, %d suppressed this turn)",
			a.guidanceBudget.suppressed)
		return
	}
	a.contextManager.Add(provider.Message{
		Role: "user",
		Content: []provider.ContentBlock{{
			Type: "text",
			Text: text,
		}},
	})
}
