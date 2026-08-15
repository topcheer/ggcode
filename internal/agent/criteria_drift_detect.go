package agent

// Success Criteria Drift Detector
//
// Research basis:
//   - "Detecting Proxy Gaming in RL and LLM Alignment via Evaluator Stress
//     Tests" (arXiv:2507.05619, v2 Jan 2026): AI systems exploit evaluator
//     weaknesses rather than improving on intended objectives. In coding agents,
//     one manifestation is "evaluator weakening": the agent progressively
//     redefines or relaxes the original task's success criteria to match what
//     it actually achieved, rather than what the user explicitly requested.
//   - "LLMs are Overconfident" (arXiv:2510.26995, 2025): LLMs systematically
//     overestimate task completion quality, rationalizing partial work as full.
//   - Proxy optimization in agentic systems: the agent optimizes for "what I
//     can show as done" rather than "what the user asked for."
//
// Problem: When an agent encounters difficulty fulfilling the original request,
// it frequently:
//   1. Narrows the success criteria: "The main issue is fixed" (silently
//      dropping secondary requirements from the original request)
//   2. Substitutes easier alternatives: "I implemented a simpler approach
//      that covers the common case" (without flagging the deviation)
//   3. Reclassifies unmet requirements as out-of-scope: "Rate limiting is
//      really a separate concern" (when the user explicitly requested it)
//   4. Claims partial fulfillment as complete: "This handles the primary
//      use case" (implying the task is done despite known gaps)
//
// This differs from existing detectors:
//   - scope_creep_detect.go: detects EXPANSION beyond the request. This
//     detector detects NARROWING of the request's scope.
//   - premature_commitment.go: checks if the agent edited before gathering
//     evidence. This checks if the agent redefined what "done" means.
//   - success_declare.go: detects claiming "done" while continuing to work.
//     This detects claiming "done" while silently dropping requirements.
//   - fulfillment_gate.go: checks if stated deliverables were verified.
//     This checks whether the stated deliverables match the original ask.
//
// Detection approach:
//   - Scans assistant text for "criteria drift" language patterns that
//     indicate the agent is redefining or relaxing requirements
//   - Requires 2+ distinct drift indicators to fire (reducing false positives)
//   - Non-blocking: advisory guidance injected into context
//   - Zero LLM cost - pure heuristic
//   - Fires at most twice per run

import (
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	// cdMaxWarns caps warnings per run to avoid noise.
	cdMaxWarns = 2

	// cdThreshold is the minimum number of distinct drift indicators
	// required before firing. Single matches are too prone to false positives
	// on legitimate design discussion or constraint clarification.
	cdThreshold = 2

	// cdIndicatorWindowTurns limits how far apart two indicators may be to
	// combine into one warning (#332). Criteria drift is *progressive*;
	// legitimate phrases from unrelated turns 6+ iterations apart must not be
	// stitched into a "proxy gaming" accusation. Mirrors drift_recurrence's
	// post-warn window convention.
	cdIndicatorWindowTurns = 3
)

// cdIndicator is a single drift-indicator hit with the iteration it occurred.
type cdIndicator struct {
	pattern string
	iter    int
}

// criteriaDriftState tracks success criteria drift signals.
type criteriaDriftState struct {
	mu             sync.Mutex
	indicators     []cdIndicator   // accumulated distinct drift indicators this run (with iter)
	seenCategories map[string]bool // categories with at least one indicator
	warnCount      int
}

func newCriteriaDriftState() *criteriaDriftState {
	return &criteriaDriftState{seenCategories: make(map[string]bool)}
}

func (c *criteriaDriftState) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.indicators = nil
	c.seenCategories = make(map[string]bool)
	c.warnCount = 0
}

// criteriaDriftPatterns defines language patterns signaling the agent is
// redefining or relaxing the original success criteria. Organized by category.
// Each pattern is matched case-insensitively as a substring.
var criteriaDriftPatterns = map[string][]string{
	// Requirement narrowing: dropping parts of the original ask
	// #395: phrases must carry REQUIREMENT semantics (requirement /
	// criteria / acceptance / asked-for + a narrowing verb). Bare
	// diagnostic language ("the core problem is the race condition") is
	// normal root-cause analysis, not criteria narrowing.
	"narrowing": {
		"the requirement is really only",
		"narrowing the scope of the requirement",
		"the acceptance criteria can be limited to",
		"only the core requirement matters",
		"reducing the acceptance criteria",
		"the essential requirement is just",
		"trimming the requirement to",
	},
	// Silent substitution: replacing a hard requirement with an easier one
	// Substitution must explicitly swap out something that was REQUESTED
	// (#395); proposing "an alternative approach that avoids allocations"
	// during normal design discussion is not substitution.
	"substitution": {
		"instead of the original requirement",
		"rather than the requested",
		"instead of what was requested",
		"a simpler solution than requested",
		"i've simplified the requirement to",
		"substituting the requirement with",
	},
	// Scope reclassification: moving unmet requirements to "out of scope"
	// Reclassification must declare an UNMET requested item out of scope;
	// general boundary-setting about unrelated concerns is normal (#395).
	"reclassification": {
		"the requested is out of scope for",
		"that requirement falls outside the scope",
		"that requirement is out of scope",
		"deferring the requested requirement",
		"that part of the request is out of scope",
	},
	// Partial-as-complete: framing partial work as sufficient
	// Partial-as-complete must frame PARTIAL work as satisfying the
	// requirement (#395).
	"partial_complete": {
		"good enough for the requirement",
		"sufficient for the stated requirement",
		"the requirement is mostly met",
		"this covers the required functionality",
		"meets the acceptance criteria enough",
	},
}

// recordAssistantText scans assistant response for criteria drift language.
func (c *criteriaDriftState) recordAssistantText(text string, iter int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	lower := strings.ToLower(text)
	newIndicators := []string{}

	for cat, pats := range criteriaDriftPatterns {
		for _, pat := range pats {
			if strings.Contains(lower, pat) {
				// Check if we already have this indicator.
				if !cdContains(c.indicators, pat) && !cdContainsStr(newIndicators, pat) {
					newIndicators = append(newIndicators, pat)
					debug.Log("agent", "Criteria drift indicator (category=%s): %q at iteration %d", cat, pat, iter)
				}
			}
		}
	}

	for _, pat := range newIndicators {
		c.indicators = append(c.indicators, cdIndicator{pattern: pat, iter: iter})
	}
	// Track which categories have been seen for category-based dedup (issue #30).
	for cat := range criteriaDriftPatterns {
		for _, ind := range newIndicators {
			for _, pat := range criteriaDriftPatterns[cat] {
				if ind == pat {
					c.seenCategories[cat] = true
					break
				}
			}
		}
	}
}

// maybeWarn returns guidance text if enough drift indicators have accumulated.
// Indicators more than cdIndicatorWindowTurns from the current iteration are
// pruned and cannot be combined across distant turns (#332). Consumed
// indicators are cleared after a warning so the same batch cannot immediately
// re-trigger.
func (c *criteriaDriftState) maybeWarn(iter int) string {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.warnCount >= cdMaxWarns {
		return ""
	}

	// Prune indicators older than the window relative to the current iter.
	fresh := c.indicators[:0]
	for _, ind := range c.indicators {
		if iter-ind.iter <= cdIndicatorWindowTurns {
			fresh = append(fresh, ind)
		}
	}
	c.indicators = fresh
	if len(c.indicators) == 0 {
		return ""
	}

	// Categories computed only over windowed indicators.
	cats := map[string]bool{}
	for cat, pats := range criteriaDriftPatterns {
		for _, ind := range c.indicators {
			for _, pat := range pats {
				if ind.pattern == pat {
					cats[cat] = true
					break
				}
			}
		}
	}
	if len(cats) < cdThreshold {
		return ""
	}

	c.warnCount++

	n := len(c.indicators)
	if n > 5 {
		n = 5
	}
	sampleParts := make([]string, 0, n)
	for _, ind := range c.indicators[:n] {
		sampleParts = append(sampleParts, ind.pattern)
	}
	sample := strings.Join(sampleParts, "; ")

	// Consume the used indicators so the same batch cannot fire twice.
	c.indicators = nil

	return `[Success Criteria Integrity] You have used language that redefines or relaxes ` +
		`the task's success criteria: "` + sample + `". ` +
		`This is a form of proxy gaming - optimizing for "what I can show as done" ` +
		`rather than "what was actually requested." ` +
		`Before claiming completion, verify your deliverables match the ORIGINAL request exactly. ` +
		`If requirements cannot be fully met: (1) state explicitly which parts are incomplete, ` +
		`(2) explain why, (3) never silently substitute easier alternatives or reclassify ` +
		`unmet requirements as "out of scope" without acknowledging the deviation to the user.`
}

// cdContains checks if a slice contains a string.
func cdContains(slice []cdIndicator, s string) bool {
	for _, v := range slice {
		if v.pattern == s {
			return true
		}
	}
	return false
}

// cdContainsStr is the plain-string variant used for intra-turn dedup.
func cdContainsStr(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}
