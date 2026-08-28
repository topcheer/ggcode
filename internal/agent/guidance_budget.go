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
	guidanceBudgetPerTurn = 2

	// guidanceBudgetBytesPerTurn caps the total bytes of guidance injected per
	// iteration across BOTH paths: tool-result hints (appendGuidance →
	// allowDeduped) and iteration-level detector guidance (injectGuidance)
	// (#1197: a single failing bash result could stack critical-tagged hints
	// without limit - count-based caps alone cannot stop byte-level flooding;
	// #1206: the two paths must share one pool symmetrically - gate AND charge
	// - or tool-hint bytes silently starve detector guidance and vice versa).
	// Critical hints bypass the count cap but still charge this byte cap.
	guidanceBudgetBytesPerTurn = 2048
)

// #441: critical classification uses ONLY the head tag (extractHintTag)
// matched exactly against criticalHintTags - the single keyword source
// shared with the coalescer. The old full-text substring scan let any
// advisory guidance that merely QUOTED '[CRITICAL' bypass the 5/turn
// cap forever.

// guidanceBudget tracks how many guidance messages have been injected
// in the current agent iteration.
type guidanceBudget struct {
	injected   int
	suppressed int
	// appendedBytes is the total size of guidance text injected this iteration
	// (#1197 byte-level flood cap; see guidanceBudgetBytesPerTurn). Both the
	// tool-result hint path (allowDeduped) and injectGuidance charge against it
	// (#1206 symmetry fix).
	appendedBytes int
	// seenHintTags records tags of tool-result hints already injected this
	// turn (#607 B3: cross-result dedup - the same meta-hint must not be
	// re-injected into every subsequent tool result).
	seenHintTags map[string]bool
}

// reset clears the budget at the start of a new iteration.
func (g *guidanceBudget) reset() {
	if g.suppressed > 0 {
		debug.Log("guidance-budget", "previous turn: %d guidance messages suppressed (budget=%d)",
			g.suppressed, guidanceBudgetPerTurn)
	}
	g.injected = 0
	g.suppressed = 0
	g.seenHintTags = nil
	g.appendedBytes = 0
}

// allow checks whether a guidance message with the given text should be
// injected. Returns true if the message should proceed (either within
// budget or critical), false if it should be suppressed.
func (g *guidanceBudget) allow(text string) bool {
	// Byte-level flood cap applies to EVERYTHING, including critical hints:
	// a stream of [SECURITY]-tagged notices can otherwise drown a result
	// just as effectively as advisory noise (#1197).
	if g.appendedBytes+len(text) > guidanceBudgetBytesPerTurn {
		g.suppressed++
		return false
	}
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

// allowDeduped is the tool-result-hint variant of allow (#607 B2/B3).
// In addition to the per-turn budget, it deduplicates by hint tag across
// ALL tool results in the same turn - a hint whose tag was already
// injected into a previous tool result this turn is suppressed instead of
// being repeated verbatim in every subsequent result.
func (g *guidanceBudget) allowDeduped(text string) bool {
	// Critical messages always pass through, but still record their tag so
	// later duplicate copies of the same critical hint are deduplicated.
	tag := strings.ToLower(extractHintTag(text))
	if tag != "" {
		if g.seenHintTags == nil {
			g.seenHintTags = make(map[string]bool)
		}
		if g.seenHintTags[tag] {
			g.suppressed++
			return false
		}
		g.seenHintTags[tag] = true
	}
	ok := g.allow(text)
	if ok {
		g.chargeBytes(len(text))
	}
	return ok
}

// chargeBytes records that n bytes of hint text were actually appended to
// a tool result (#1197). Called by allowDeduped after a successful allow.
func (g *guidanceBudget) chargeBytes(n int) {
	g.appendedBytes += n
}

// isCriticalGuidance returns true if the guidance text's HEAD TAG marks it
// as critical/safety-relevant (#441: exact tag match only - never a
// full-text substring scan).
func isCriticalGuidance(text string) bool {
	tag := extractHintTag(text)
	return isCriticalTag(tag)
}

// injectGuidance is the budget-aware replacement for direct
// contextManager.Add(provider.Message{Role: "user", ...}) calls from
// detectors. It checks the per-turn budget before injecting.
//
// #677: ALL iteration-level detector injections in agent.go's run loop
// (errorRush, solutionFixation, errorCompound, correctionSpiral,
// momentumLoss, targetScatter, redundantReverify, verifyDebt, ...) route
// through this method, so the "hard per-turn limit across ALL detectors"
// promise holds for the iteration-level cluster too - not just the
// tool-result hint path (#441/#607). Loop-recovery protocol nudges
// (empty-response retry, truncation continuation, inline-tool-call format
// correction) intentionally stay direct adds: they carry their own hard
// caps, and budget suppression would break loop recovery.
//
// #681: returns whether the message was actually DELIVERED (false = the
// per-turn budget suppressed it). Returning a message from a detector is
// not delivering it: callers with one-shot semantics (monorepo scope hint)
// or per-run warning quotas (errorCompound's "at most 2 per run") must
// only consume their one chance / quota when this returns true, otherwise
// a saturated detector turn burns the quota with ZERO guidance delivered
// (the detector goes permanently dark - "returned != delivered").
func (a *Agent) injectGuidance(text string) bool {
	if !a.guidanceBudget.allow(text) {
		debug.Log("guidance-budget", "suppressing guidance message (budget exceeded, %d suppressed this turn)",
			a.guidanceBudget.suppressed)
		return false
	}
	// #1206: a successful injection must charge the shared byte pool exactly
	// like the tool-result hint path does - previously injectGuidance was
	// gated by the byte cap but never charged it, so tool-hint bytes could
	// silently starve iteration-level detector guidance (and unlimited
	// iteration injections never filled the pool for tool hints either).
	a.guidanceBudget.chargeBytes(len(text))
	a.contextManager.Add(provider.Message{
		Role: "user",
		Content: []provider.ContentBlock{{
			Type: "text",
			Text: text,
		}},
	})
	return true
}
