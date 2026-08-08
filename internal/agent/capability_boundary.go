package agent

// Capability Boundary Escalation Detector
//
// Research basis:
//   - ICML 2025 (arXiv:2506.05109): "Truly Self-Improving Agents Require
//     Intrinsic Metacognitive Learning" - Liu & van der Schaar argue that
//     effective agents must possess "metacognitive knowledge": the ability
//     to self-assess their own capability boundaries and recognize when a
//     task exceeds their current competence.
//   - HyperAgents / DGM (Meta, 2025): self-referential agents must know
//     when to stop and escalate rather than thrashing.
//   - SICA (arXiv:2504.15228, NeurIPS 2025): identifies trajectory waste
//     as the primary bottleneck - agents burning iterations on approaches
//     that repeatedly fail is a top waste pattern.
//   - Agent-R (2025): self-training framework shows that recognizing
//     capability limits improves success rate more than any single tool.
//
// Problem: AI coding agents sometimes stubbornly persist on a problem
// after multiple distinct failed attempts, burning tokens and context
// without making progress. This is the inverse of premature surrender:
// instead of giving up too early, the agent refuses to give up at all,
// cycling through different approaches that all fail for the same
// underlying reason it cannot diagnose.
//
// Real-world examples:
//  1. Build fails -> tries 5 different fix approaches, each introducing
//     a new error -> never escalates to "this requires human guidance"
//  2. Test fails -> keeps rewriting the test, never questions whether
//     the underlying assumption is wrong
//  3. Dependency conflict -> tries alternative imports, version bumps,
//     build flag changes - all fail - never says "I need help"
//  4. Race condition -> adds locks, channels, mutexes, sync.WaitGroup
//     - each new attempt reveals the same deadlock - never escalates
//
// Existing ggcode detectors that are RELATED but do NOT cover this:
//   - premature_surrender.go: detects giving up too EARLY (inverse problem).
//   - error_strategy_loop.go: detects repeating the SAME error strategy.
//     This detector catches DISTINCT approaches all failing.
//   - error_compounding.go: detects errors in rapid succession.
//     This detector specifically tracks distinct APPROACH pivots.
//   - tool_error_fallback.go: suggests fallback for a single tool error.
//     This detector addresses cross-attempt pattern recognition.
//
// Gap: No detector tracks when the agent has exhausted N distinct
// approaches to the same problem without success, indicating it has
// hit a capability boundary that requires human escalation.
//
// Design:
//   - Tracks "approach pivots" - significant strategy changes after a
//     failure (detected via text markers like "let me try a different",
//     "alternatively", "another approach", combined with prior errors).
//   - Also counts consecutive failed tool results (non-empty error
//     fields) interspersed with these pivots.
//   - Threshold: 3+ distinct approaches that all failed -> inject
//     guidance to escalate to the user or reconsider the fundamental
//     approach.
//   - Zero LLM cost - pure deterministic text pattern matching.
//   - Fires at most 1 time per run (high-confidence, actionable).

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	// capBoundaryPivotThreshold: after this many failed approach pivots,
	// inject escalation guidance.
	capBoundaryPivotThreshold = 3

	// capBoundaryMaxWarnings: max warnings per run.
	capBoundaryMaxWarnings = 1
)

// approachPivotRe matches language indicating the agent is switching to
// a fundamentally different strategy after a prior failure.
var approachPivotRe = regexp.MustCompile(`(?i)(?:` +
	`let\s+me\s+try\s+(?:a\s+)?(?:different|another|alternative|new)\s+(?:approach|strategy|way|method|solution|tactic)` +
	`|alternatively[,!.]` +
	`|another\s+(?:approach|strategy|way|method|option)\s+(?:would|could|to|is)` +
	`|let\s+me\s+(?:reconsider|rethink|revisit|take\s+a\s+step\s+back)` +
	`|i\s+'ll\s+(?:try\s+)?(?:a\s+)?(?:different|another|alternative)\s+(?:approach|strategy|way|tactic)` +
	`|switching\s+(?:to|approach|strategy)` +
	`|pivoting\s+(?:to|from)` +
	`|instead\s+of\s+.{2,60}[,;.]\s+let(?:'s|\s+me)\s+` +
	`|different\s+(?:tack|strategy|approach)` +
	`)`)

// approachPivotHit records a detected strategy pivot.
type approachPivotHit struct {
	excerpt string
}

// capabilityBoundaryState tracks approach pivots and failures across a run.
type capabilityBoundaryState struct {
	warnings   int
	pivots     []approachPivotHit
	failErrors int // count of tool results with errors since last success
}

func newCapabilityBoundaryState() *capabilityBoundaryState {
	return &capabilityBoundaryState{}
}

func (s *capabilityBoundaryState) reset() {
	s.warnings = 0
	s.pivots = nil
	s.failErrors = 0
}

// recordApproachPivot adds a detected strategy change.
func (s *capabilityBoundaryState) recordApproachPivot(excerpt string) {
	s.pivots = append(s.pivots, approachPivotHit{excerpt: excerpt})
}

// recordToolResult tracks whether the last tool result was an error or success.
func (s *capabilityBoundaryState) recordToolResult(isError bool) {
	if isError {
		s.failErrors++
	} else {
		s.failErrors = 0
	}
}

// scanApproachPivots detects strategy-change language in assistant text.
func scanApproachPivots(text string) []approachPivotHit {
	if len(text) == 0 {
		return nil
	}
	var hits []approachPivotHit
	matches := approachPivotRe.FindAllStringIndex(text, -1)
	for _, m := range matches {
		start := m[0]
		excerptStart := start - 20
		if excerptStart < 0 {
			excerptStart = 0
		}
		excerptEnd := m[1] + 40
		if excerptEnd > len(text) {
			excerptEnd = len(text)
		}
		excerpt := strings.TrimSpace(text[excerptStart:excerptEnd])
		if len(excerpt) > 80 {
			excerpt = excerpt[:80] + "..."
		}
		hits = append(hits, approachPivotHit{excerpt: excerpt})
	}
	return hits
}

// maybeWarnCapabilityBoundary checks whether the agent has exhausted
// multiple distinct failed approaches and should escalate.
// Returns empty string if no warning is needed.
func (a *Agent) maybeWarnCapabilityBoundary(text string) string {
	if a.capBoundary == nil {
		return ""
	}
	if a.capBoundary.warnings >= capBoundaryMaxWarnings {
		return ""
	}

	// Record new pivots from this iteration's text
	newPivots := scanApproachPivots(text)
	for _, np := range newPivots {
		a.capBoundary.recordApproachPivot(np.excerpt)
	}

	// Need threshold pivots AND evidence of recent failures
	if len(a.capBoundary.pivots) < capBoundaryPivotThreshold {
		return ""
	}
	if a.capBoundary.failErrors < 2 {
		// pivots without errors might be exploration, not stuck
		return ""
	}

	a.capBoundary.warnings++

	var excerpts []string
	for idx, ph := range a.capBoundary.pivots {
		if idx >= 3 {
			break
		}
		excerpts = append(excerpts, fmt.Sprintf("  pivot #%d: ...%s...", idx+1, ph.excerpt))
	}

	msg := fmt.Sprintf("[capability-boundary] %d distinct approach pivots detected with %d+ recent failures. ",
		len(a.capBoundary.pivots), a.capBoundary.failErrors)
	msg += "This pattern suggests the problem may be at the edge of autonomous solvability. "
	msg += "The agent has tried multiple fundamentally different strategies and all have failed. "
	msg += "Metacognitive self-assessment (ICML 2025, arXiv:2506.05109) indicates that recognizing "
	msg += "capability boundaries is critical for efficient agent operation. "
	msg += "Consider: (1) Escalate to the user - explain what you've tried and ask for guidance. "
	msg += "(2) Re-examine the root cause - all approaches failing suggests a shared underlying issue. "
	msg += "(3) Simplify - try the simplest possible reproduction before another fix.\n"
	msg += "Detected strategy pivots:\n" + strings.Join(excerpts, "\n") + "\n"
	return msg
}
