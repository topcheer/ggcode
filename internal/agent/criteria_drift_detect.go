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
	// #582: user-authorized descoping (e.g., "as requested, that requirement is out of scope")
	// is exempt - it reflects faithful execution of explicit user instruction, not proxy gaming.
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

	// #586 F7: Track indicators per sentence for deduplication
	// This prevents two synonymous phrases in the same sentence from counting twice.
	sentenceIndicators := make(map[string]map[string]bool) // sentence -> category -> seen

	// Split text into sentences first for per-sentence dedup
	sentences := cdSplitSentences(lower)
	for _, sentence := range sentences {
		sentenceIndicators[sentence] = make(map[string]bool)
	}

	for cat, pats := range criteriaDriftPatterns {
		for _, pat := range pats {
			if strings.Contains(lower, pat) {
				// #586 F7: Skip if we already have this category in the same sentence
				matchedSentence := cdFindSentenceForPattern(lower, pat)
				if matchedSentence != "" {
					// Initialize sentence map if not exists
					if sentenceIndicators[matchedSentence] == nil {
						sentenceIndicators[matchedSentence] = make(map[string]bool)
					}
					if sentenceIndicators[matchedSentence][cat] {
						debug.Log("agent", "Criteria drift indicator SKIPPED (dedupe) (category=%s, sentence=%q): %q at iteration %d", cat, matchedSentence, pat, iter)
						continue
					}
				}

				// Check if we already have this indicator globally.
				if !cdContains(c.indicators, pat) && !cdContainsStr(newIndicators, pat) {
					// #582: Skip patterns with authorization context.
					if cdIsAuthExempt(text, pat, cat) {
						debug.Log("agent", "Criteria drift indicator EXEMPT (auth context) (category=%s): %q at iteration %d", cat, pat, iter)
						continue
					}
					newIndicators = append(newIndicators, pat)
					debug.Log("agent", "Criteria drift indicator (category=%s): %q at iteration %d", cat, pat, iter)
					// Mark this category as seen in this sentence
					if matchedSentence != "" {
						sentenceIndicators[matchedSentence][cat] = true
					}
				}
			}
		}
	}

	for _, pat := range newIndicators {
		c.indicators = append(c.indicators, cdIndicator{pattern: pat, iter: iter})
	}
	// Track which categories have been seen (legacy; kept for compatibility).
	// Note: After #582 fix, the threshold uses indicator count, not category count.
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

// cdAuthMarkers are phrases that indicate user-authorized descoping (#582).
// When these appear before a drift pattern, the indicator is exempt
// because it reflects faithful execution of explicit user instruction.
// #586: Markers in negation or non-authorization context (e.g., "NOT as requested",
// "you asked me about X", "mentioned") do NOT authorize descoping.
var cdAuthMarkers = []string{
	"as requested",
	"as you requested",
	"per your instruction",
	"user asked",
	"as you asked",
	"you asked me",
	"you instructed",
	"as instructed",
}

// cdHasAuthContext checks if the given text contains authorization markers
// that indicate user-authorized descoping. Returns true if any marker is found.
// #586: Only markers in non-negated, authorization context count.
func cdHasAuthContext(text string) bool {
	lower := strings.ToLower(text)
	for _, marker := range cdAuthMarkers {
		idx := strings.Index(lower, marker)
		if idx == -1 {
			continue
		}
		// #586 F1: Check for negation before the marker
		if idx > 0 {
			prevChar := lower[idx-1]
			// Skip if marker is immediately preceded by negation word or apostrophe
			if prevChar == ' ' && idx > 5 {
				// Check 5 chars before the space for negation words
				prefix := lower[idx-5 : idx]
				if strings.Contains(prefix, "not ") || strings.Contains(prefix, "never") || strings.Contains(prefix, "n't") {
					continue
				}
			}
		}
		// #586 F1: Skip non-authorization verb forms (asked about, mentioned, etc.)
		// "you asked me about X" is a question, not authorization
		if idx+len(marker) < len(lower) {
			suffix := lower[idx+len(marker):]
			if strings.HasPrefix(suffix, " me about") || strings.HasPrefix(suffix, " about") {
				continue
			}
			if strings.Contains(suffix, "mentioned") || strings.Contains(suffix, "noted") {
				continue
			}
		}
		return true
	}
	return false
}

// cdIsAuthExempt checks if a pattern match is exempt due to user authorization.
// For patterns in authorized categories, we require that an authorization marker
// appears BEFORE the pattern in the same sentence.
// This prevents false exemptions when auth markers appear in unrelated contexts
// or after the pattern (retrospective justification).
// #586: Expanded to narrowing/reclassification/dead-end/success-criterion categories.
func cdIsAuthExempt(text, pattern string, category string) bool {
	// #586 F3: Expand exemption to all 4 categories, not just reclassification
	// Users may authorize narrowing ("just focus on the core"), dead-end ("that's impossible"),
	// success-criterion ("good enough is fine"), or reclassification ("that's out of scope").
	if category != "narrowing" && category != "reclassification" && category != "substitution" && category != "partial_complete" {
		return false
	}

	lower := strings.ToLower(text)
	patternLower := strings.ToLower(pattern)

	// #586 F6: For each pattern occurrence, find the nearest preceding marker
	// This handles multiple patterns in the same sentence correctly.
	patternIdx := 0
	for {
		relativeIdx := strings.Index(lower[patternIdx:], patternLower)
		if relativeIdx == -1 {
			break
		}
		patternIdx += relativeIdx // Adjust to absolute position

		// Find the sentence containing this pattern occurrence
		// #586 F2: Sentence boundaries include .!? ; and newline.
		// Semicolon and newline create separate sentences to prevent false exemptions.
		sentenceStart := 0
		for i := patternIdx; i >= 0; i-- {
			if i > 0 && (lower[i-1] == '.' || lower[i-1] == '!' || lower[i-1] == '?' || lower[i-1] == ';' || lower[i-1] == '\n') {
				// Sentence starts AFTER the boundary character and any following whitespace
				sentenceStart = i
				// Skip the boundary character itself
				for sentenceStart < len(lower) && (lower[sentenceStart] == '.' || lower[sentenceStart] == '!' || lower[sentenceStart] == '?' || lower[sentenceStart] == ';' || lower[sentenceStart] == '\n') {
					sentenceStart++
				}
				// Skip whitespace after boundary
				for sentenceStart < len(lower) && (lower[sentenceStart] == ' ' || lower[sentenceStart] == '\t' || lower[sentenceStart] == '\r') {
					sentenceStart++
				}
				break
			}
		}

		sentenceEnd := len(lower)
		for i := patternIdx; i < len(lower); i++ {
			if lower[i] == '.' || lower[i] == '!' || lower[i] == '?' || lower[i] == ';' || lower[i] == '\n' {
				sentenceEnd = i + 1
				break
			}
		}

		sentence := lower[sentenceStart:sentenceEnd]
		// Only look for markers BEFORE this pattern occurrence
		relativePatternIdx := patternIdx - sentenceStart
		patternPrefix := sentence[:relativePatternIdx]

		// Check if there's an authorization marker BEFORE the pattern in this sentence
		if cdHasAuthContext(patternPrefix) {
			return true
		}

		patternIdx += len(patternLower)
	}

	return false
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
	// #582: Count distinct indicators, not categories. The header says
	// "Requires 2+ distinct drift indicators" and this is the correct semantics.
	if len(c.indicators) < cdThreshold {
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

// #586 F2: cdSplitSentences splits text into sentences.
// Sentence boundaries: .!? ; newline followed by whitespace or end of string.
func cdSplitSentences(text string) []string {
	var sentences []string
	start := 0
	for i := 0; i < len(text); i++ {
		if text[i] == '.' || text[i] == '!' || text[i] == '?' || text[i] == ';' || text[i] == '\n' {
			end := i + 1
			// Skip trailing whitespace
			for end < len(text) && (text[end] == ' ' || text[end] == '\t' || text[end] == '\n' || text[end] == '\r') {
				end++
			}
			if end > start {
				sentences = append(sentences, text[start:end])
			}
			start = end
		}
	}
	if start < len(text) {
		sentences = append(sentences, text[start:])
	}
	return sentences
}

// #586 F7: cdFindSentenceForPattern finds which sentence contains the pattern.
func cdFindSentenceForPattern(text, pattern string) string {
	idx := strings.Index(text, pattern)
	if idx == -1 {
		return ""
	}
	// Find sentence start
	start := 0
	for i := idx; i >= 0; i-- {
		if i > 0 && (text[i-1] == '.' || text[i-1] == '!' || text[i-1] == '?' || text[i-1] == ';' || text[i-1] == '\n') {
			if i < len(text) && (text[i] == ' ' || text[i] == '\t' || text[i] == '\n' || text[i] == '\r') {
				start = i
				break
			}
		}
	}
	// Find sentence end
	end := len(text)
	for i := idx; i < len(text); i++ {
		if text[i] == '.' || text[i] == '!' || text[i] == '?' || text[i] == ';' || text[i] == '\n' {
			end = i + 1
			break
		}
	}
	return text[start:end]
}
