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

	// #1840 case 2: critical hints charge a DEDICATED sub-pool instead of
	// competing with advisory in the shared one. The shared pool is
	// allocated first-come-first-served across parallel tool results, so
	// early results' advisory could eat all 2048 bytes and later results'
	// [hardcoded-secret]/[git-destructive] notices were silently dropped -
	// priority only existed WITHIN a single result. The dedicated pool
	// keeps #1197's flood bound (a critical-tag stream is still capped,
	// just separately) while making critical priority real cross-result.
	// This is the BASE value; the runtime cap is elastic (see below).
	guidanceBudgetCriticalBytesPerTurn = 1024
	//
	// Elastic calibration bounds (sa-37 consumption loop). The two pools are
	// no longer fixed constants at runtime: each turn boundary, reset()
	// calibrates the EFFECTIVE caps from the just-measured starvation
	// (suppress-bytes counts recorded by the detector ledger's choke point).
	// This is arXiv:2602.15391's adaptive-calibration loop applied to our own
	// budget: static caps yielded measured starvation (sa-36's ledger exists
	// precisely because silent starvation was invisible); growth is bounded
	// at 2x base so the #1197 flood guarantee keeps a hard ceiling, and decay
	// returns to base when starvation stops. The COUNT cap stays fixed on
	// purpose: it guards attention bandwidth (ACE context-collision), not
	// capacity, and adaptive attention limits would reintroduce alert fatigue.
	guidanceByteCapGrowthDen     = 4 // +25% per starved turn
	guidanceByteCapDecayDen      = 5 // -20% per clean turn
	guidanceByteCapMaxMultiplier = 2
)

// guidanceBudgetBytesPerTurnMax is the hard ceiling for the elastic advisory
// pool; guidanceBudgetCriticalBytesPerTurnMax the critical one.
func guidanceBudgetBytesPerTurnMax() int {
	return guidanceBudgetBytesPerTurn * guidanceByteCapMaxMultiplier
}

func guidanceBudgetCriticalBytesPerTurnMax() int {
	return guidanceBudgetCriticalBytesPerTurn * guidanceByteCapMaxMultiplier
}

// guidanceReject classifies WHY a budget gate rejected a guidance message.
// The detector effectiveness ledger (detector_ledger.go) aggregates these
// per tag so calibration can distinguish "detector starved by byte cap"
// from "superseded by dedup" - different remediation (raise cap vs fix
// repeat-firing detector).
type guidanceReject int

const (
	rejectNone          guidanceReject = iota
	rejectBudgetBytes                  // advisory byte pool exhausted
	rejectBudgetCount                  // per-turn count cap reached
	rejectCriticalBytes                // critical byte pool exhausted
	rejectDedup                        // same head tag already delivered this turn
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
	// criticalBytes is the dedicated critical-hint byte pool (#1840 case 2).
	criticalBytes int
	// delivered records the guidance texts actually delivered this turn
	// across BOTH paths (#1840 case 4) so conflict arbitration can see the
	// union - injectGuidance guidance previously bypassed the conflict
	// detector entirely (it only scanned a single tool result's hint set),
	// so an errorRush "ACT NOW" injection could coexist with a later
	// "EXPLORE" hint with no arbitration.
	delivered []string
	// seenHintTags records tags of tool-result hints already injected this
	// turn (#607 B3: cross-result dedup - the same meta-hint must not be
	// re-injected into every subsequent tool result).
	seenHintTags map[string]bool
	// ledger is the run-scoped detector effectiveness ledger (nil-safe: all
	// ledger methods tolerate nil receivers, and this pointer stays nil for
	// bare guidanceBudget constructions). Recorded inside allow/allowDeduped
	// so EVERY budgeted delivery path - injectGuidance, appendGuidance, and
	// the coalesced tool-result hints - is measured at exactly one choke
	// point (see detector_ledger.go).
	ledger *detectorLedger
	// byteCap / criticalCap are the EFFECTIVE per-turn pools. Zero means
	// "not yet calibrated" (bare struct): allowTagged falls back to the base
	// constants and the next reset() seeds them. reset() adapts them within
	// [base, 2x base] from the previous turn's measured byte-starvation.
	byteCap     int
	criticalCap int
	// suppressedBytesTurn / suppressedCriticalTurn count the current turn's
	// pool rejections (the starvation signal adaptCaps consumes; g.suppressed
	// mixes all reasons so it cannot drive calibration by itself).
	suppressedBytesTurn    int
	suppressedCriticalTurn int
}

// reset clears the budget at the start of a new iteration. The adaptation
// step runs FIRST, against the just-finished turn's counters - clearing them
// before calibrating would make the loop read zeros forever.
func (g *guidanceBudget) reset() {
	if g.suppressed > 0 {
		debug.Log("guidance-budget", "previous turn: %d guidance messages suppressed (budget=%d)",
			g.suppressed, guidanceBudgetPerTurn)
	}
	g.adaptCaps()
	g.injected = 0
	g.suppressed = 0
	g.seenHintTags = nil
	g.appendedBytes = 0
	g.criticalBytes = 0
	g.delivered = nil
	g.suppressedBytesTurn = 0
	g.suppressedCriticalTurn = 0
}

// adaptCaps is the consumption half of the sa-37 feedback loop: the detector
// ledger measures per-tag starvation at the budget choke point; here that
// measurement adjusts the elastic pools. A turn with byte rejections grows
// the corresponding pool by 25% (bounded at 2x base); a clean turn decays it
// 20% toward base. Single-direction hysteresis (grow only on measured
// starvation, decay only when clean) keeps the pool stable around the run's
// real demand instead of oscillating.
func (g *guidanceBudget) adaptCaps() {
	if g.byteCap <= 0 {
		g.byteCap = guidanceBudgetBytesPerTurn
	}
	if g.criticalCap <= 0 {
		g.criticalCap = guidanceBudgetCriticalBytesPerTurn
	}
	if g.suppressedBytesTurn > 0 {
		grown := g.byteCap + g.byteCap/guidanceByteCapGrowthDen
		if max := guidanceBudgetBytesPerTurnMax(); grown > max {
			grown = max
		}
		if grown != g.byteCap {
			debug.Log("guidance-budget", "advisory pool starved (%d byte rejections last turn): cap %d -> %d (max %d)",
				g.suppressedBytesTurn, g.byteCap, grown, guidanceBudgetBytesPerTurnMax())
		}
		g.byteCap = grown
	} else {
		shrunk := g.byteCap - g.byteCap/guidanceByteCapDecayDen
		if shrunk < guidanceBudgetBytesPerTurn {
			shrunk = guidanceBudgetBytesPerTurn
		}
		g.byteCap = shrunk
	}
	if g.suppressedCriticalTurn > 0 {
		grown := g.criticalCap + g.criticalCap/guidanceByteCapGrowthDen
		if max := guidanceBudgetCriticalBytesPerTurnMax(); grown > max {
			grown = max
		}
		if grown != g.criticalCap {
			debug.Log("guidance-budget", "critical pool starved (%d byte rejections last turn): cap %d -> %d (max %d)",
				g.suppressedCriticalTurn, g.criticalCap, grown, guidanceBudgetCriticalBytesPerTurnMax())
		}
		g.criticalCap = grown
	} else {
		shrunk := g.criticalCap - g.criticalCap/guidanceByteCapDecayDen
		if shrunk < guidanceBudgetCriticalBytesPerTurn {
			shrunk = guidanceBudgetCriticalBytesPerTurn
		}
		g.criticalCap = shrunk
	}
}

// effectiveByteCap returns this turn's advisory pool (base for bare structs
// that were never reset).
func (g *guidanceBudget) effectiveByteCap() int {
	if g.byteCap <= 0 {
		return guidanceBudgetBytesPerTurn
	}
	return g.byteCap
}

// effectiveCriticalCap is effectiveByteCap for the critical pool.
func (g *guidanceBudget) effectiveCriticalCap() int {
	if g.criticalCap <= 0 {
		return guidanceBudgetCriticalBytesPerTurn
	}
	return g.criticalCap
}

// allow checks whether a guidance message with the given text should be
// injected. Returns true if the message should proceed (either within
// budget or critical), false if it should be suppressed.
func (g *guidanceBudget) allow(text string) bool {
	ok, _ := g.allowTagged(text, extractHintTag(text))
	return ok
}

// allowTagged is allow with a pre-extracted head tag (allowDeduped already
// has it) that records the outcome - delivered vs suppressed-by-reason -
// into the run-scoped detector ledger.
func (g *guidanceBudget) allowTagged(text string, tag string) (bool, guidanceReject) {
	// Critical messages first, against their dedicated pool (#1840 case 2).
	if isCriticalGuidance(text) {
		if g.criticalBytes+len(text) > g.effectiveCriticalCap() {
			g.suppressed++
			g.suppressedCriticalTurn++
			g.ledger.noteSuppressed(tag, rejectCriticalBytes)
			return false, rejectCriticalBytes
		}
		g.ledger.noteDelivered(tag, len(text))
		return true, rejectNone
	}
	// Byte-level flood cap for advisory (#1197: a stream of tagged notices
	// can otherwise drown a result just as effectively as noise).
	if g.appendedBytes+len(text) > g.effectiveByteCap() {
		g.suppressed++
		g.suppressedBytesTurn++
		g.ledger.noteSuppressed(tag, rejectBudgetBytes)
		return false, rejectBudgetBytes
	}
	if g.injected < guidanceBudgetPerTurn {
		g.injected++
		g.ledger.noteDelivered(tag, len(text))
		return true, rejectNone
	}
	g.suppressed++
	g.ledger.noteSuppressed(tag, rejectBudgetCount)
	return false, rejectBudgetCount
}

// allowDeduped is the tool-result-hint variant of allow (#607 B2/B3).
// In addition to the per-turn budget, it deduplicates by hint tag across
// ALL tool results in the same turn - a hint whose tag was already
// injected into a previous tool result this turn is suppressed instead of
// being repeated verbatim in every subsequent result.
func (g *guidanceBudget) allowDeduped(text string) bool {
	// Critical messages always pass through, but still record their tag so
	// later duplicate copies of the same critical hint are deduplicated.
	rawTag := extractHintTag(text)
	tag := strings.ToLower(rawTag)
	if tag != "" {
		if g.seenHintTags == nil {
			g.seenHintTags = make(map[string]bool)
		}
		if g.seenHintTags[tag] {
			g.suppressed++
			g.ledger.noteSuppressed(rawTag, rejectDedup)
			return false
		}
		// #1840 case 1: mark the dedup slot ONLY on delivery. Marking
		// before allow() left the tag permanently recorded when the byte
		// pool rejected the hint - the same tag (including later CRITICAL
		// copies, which check dedup before the critical bypass) was then
		// blocked for the rest of the turn while the hint was never
		// delivered: the #681 "returned != delivered" residue.
	}
	ok, _ := g.allowTagged(text, rawTag)
	if ok {
		if tag != "" {
			if g.seenHintTags == nil {
				g.seenHintTags = make(map[string]bool)
			}
			g.seenHintTags[tag] = true
		}
		g.chargeBytes(len(text), isCriticalGuidance(text))
		g.delivered = append(g.delivered, text)
	}
	return ok
}

// chargeBytes records that n bytes of hint text were actually appended to
// a tool result (#1197). Called by allowDeduped after a successful allow.
func (g *guidanceBudget) chargeBytes(n int, critical bool) {
	if critical {
		g.criticalBytes += n
		return
	}
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
	// #1840 case 4: iteration-level guidance joins the conflict scan set.
	// detectGuidanceConflict previously ran only over one tool result's
	// retained hints; this path's injections (errorRush "ACT NOW" etc.)
	// could contradict them unimpeded.
	if ch := detectGuidanceConflict(append(append([]string{}, a.guidanceBudget.delivered...), text)); ch != "" && a.guidanceBudget.allowDeduped(ch) {
		a.contextManager.Add(provider.Message{
			Role: "user",
			Content: []provider.ContentBlock{{
				Type: "text",
				Text: ch,
			}},
		})
	}
	a.guidanceBudget.chargeBytes(len(text), isCriticalGuidance(text))
	a.guidanceBudget.delivered = append(a.guidanceBudget.delivered, text)
	a.contextManager.Add(provider.Message{
		Role: "user",
		Content: []provider.ContentBlock{{
			Type: "text",
			Text: text,
		}},
	})
	return true
}
