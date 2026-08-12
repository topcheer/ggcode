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
)

// criteriaDriftState tracks success criteria drift signals.
type criteriaDriftState struct {
	mu             sync.Mutex
	indicators     []string        // accumulated distinct drift indicators this run
	seenCategories map[string]bool // categories with at least one indicator (issue #30)
	warnCount      int
	fired          bool
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
	c.fired = false
}

// criteriaDriftPatterns defines language patterns signaling the agent is
// redefining or relaxing the original success criteria. Organized by category.
// Each pattern is matched case-insensitively as a substring.
var criteriaDriftPatterns = map[string][]string{
	// Requirement narrowing: dropping parts of the original ask
	"narrowing": {
		"the main issue is",
		"the primary concern is",
		"the core problem is",
		"this handles the main case",
		"this covers the primary",
		"the essential functionality",
		"the critical path is",
		"focuses on the key",
	},
	// Silent substitution: replacing a hard requirement with an easier one
	"substitution": {
		"a simpler approach",
		"a simpler solution",
		"a simpler implementation",
		"an alternative approach that",
		"instead of the original",
		"rather than the requested",
		"i took a different approach",
		"i've simplified this to",
	},
	// Scope reclassification: moving unmet requirements to "out of scope"
	"reclassification": {
		"is really a separate concern",
		"is a separate issue",
		"is out of scope for",
		"falls outside the scope",
		"beyond what was asked",
		"is better addressed separately",
		"should be a follow-up",
		"is a different task",
	},
	// Partial-as-complete: framing partial work as sufficient
	"partial_complete": {
		"covers the common case",
		"handles the main scenario",
		"works for the typical use",
		"good enough for now",
		"sufficient for the majority",
		"addresses the immediate need",
		"this should work in most cases",
		"functionally equivalent for",
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
				if !cdContains(c.indicators, pat) && !cdContains(newIndicators, pat) {
					newIndicators = append(newIndicators, pat)
					debug.Log("agent", "Criteria drift indicator (category=%s): %q at iteration %d", cat, pat, iter)
				}
			}
		}
	}

	c.indicators = append(c.indicators, newIndicators...)
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
func (c *criteriaDriftState) maybeWarn(iter int) string {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.fired || c.warnCount >= cdMaxWarns {
		return ""
	}
	if len(c.seenCategories) < cdThreshold {
		return ""
	}

	c.fired = true
	c.warnCount++

	n := len(c.indicators)
	if n > 5 {
		n = 5
	}
	sample := strings.Join(c.indicators[:n], "; ")

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
func cdContains(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}
