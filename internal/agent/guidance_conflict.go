package agent

// Guidance Conflict Detector - Cross-Component Interference (CCI)
//
// Research basis:
//   - arXiv:2605.05716 "More Is Not Always Better: Cross-Component
//     Interference in LLM Agent Scaffolding" (May 2026): stacking all
//     scaffolding components (planning, tools, memory, self-reflection,
//     retrieval) is consistently suboptimal. Components interact
//     destructively: 183/325 submodularity violations found. A maximally-
//     equipped agent underperforms task-specific subset selection.
//   - "Arbiter: Detecting Interference in LLM Agent System Prompts"
//     (2025): identifies that when scaffolding directives conflict, the
//     agent's effective performance drops - it cannot satisfy contradictory
//     guidance simultaneously, leading to partial compliance or paralysis.
//   - ACE Framework (ICLR 2026) "context collision": 3+ guidance
//     directives arriving in the same turn with conflicting action
//     imperatives prevent the model from committing to any course.
//
// Problem: ggcode has 128+ intelligence modules that can independently
// inject guidance. The coalesceGuidance function deduplicates and caps
// hints but does NOT detect semantic conflicts between retained hints.
// When two surviving hints push in opposite directions (e.g., one says
// "act now, stop exploring" while another says "explore more before
// acting"), the agent receives contradictory imperatives - classic CCI.
//
// Design:
//   - Called AFTER coalesceGuidance, operating on the final retained set.
//   - Scans hint pairs for known conflict patterns (keyword-based, zero LLM).
//   - When a conflict is found, returns a [guidance-conflict] meta-hint
//     that tells the agent which directive takes priority, resolving the
//     ambiguity instead of leaving the model to guess.
//   - Max 1 conflict warning per tool result (no cascading alert inflation).
//   - Zero LLM cost - pure deterministic pattern matching.

import (
	"strings"
)

// conflictPair defines a pair of mutually-exclusive action imperatives.
// Each side is a list of keywords/phrases that indicate that directive.
type conflictPair struct {
	sideA    []string
	sideB    []string
	priority string
}

// guidanceConflicts defines known contradictory imperative pairs.
// Each is drawn from actual detector outputs in the ggcode system.
var guidanceConflicts = []conflictPair{
	{
		// "act now" vs "explore more" - the classic explore/exploit CCI.
		sideA:    []string{"ACT NOW", "STOP EXPLOR", "PAUSE EXPLOR", "MAKE YOUR BEST-GUESS EDIT"},
		sideB:    []string{"EXPLOR", "BROADEN", "WIDEN SCOPE", "READ MORE", "UNDERSTAND BEFORE"},
		priority: "Resolve by acting on the highest-priority task directive; do not stall on further exploration.",
	},
	{
		// "narrow scope" vs "broaden search" - opposing search directives.
		sideA:    []string{"NARROW SCOPE", "NARROWER", "SCOPE-NARROW", "REDUCE SCOPE", "TOO BROAD"},
		sideB:    []string{"BROADEN", "WIDER", "BROAD SEARCH", "EXPAND SCOPE", "TOO NARROW"},
		priority: "Pick one search strategy based on task phase; do not alternate.",
	},
	{
		// "speed up / reduce iterations" vs "be thorough / verify more"
		sideA:    []string{"REDUCE ITERAT", "SPEED UP", "FEWER CALLS", "MINIMIZE CALLS", "TOO MANY CALLS", "CALL ECONOMY"},
		sideB:    []string{"VERIFY", "BE THOROUGH", "DOUBLE-CHECK", "MORE TESTS", "COMPREHENSIVE", "DON'T SKIP"},
		priority: "Balance speed and thoroughness per the current task's risk level.",
	},
	{
		// "commit now" vs "don't commit yet"
		// #462: "IS READY TO COMMIT" — the bare form matched inside the
		// negated "NOT READY TO COMMIT", judging two same-direction
		// don't-commit hints as contradictory.
		sideA:    []string{"COMMIT NOW", "STAGE AND COMMIT", "IS READY TO COMMIT"},
		sideB:    []string{"DON'T COMMIT", "NOT READY", "BEFORE COMMITTING", "VERIFY BEFORE COMMIT", "DO NOT COMMIT"},
		priority: "Do not commit until verification passes; the conservative directive wins.",
	},
}

// detectGuidanceConflict scans a slice of coalesced hints for semantic
// conflicts between retained directives. If found, it returns a conflict
// resolution hint. Returns "" if no conflict is detected.
//
// This runs AFTER coalesceGuidance, so the input is already deduplicated
// and capped (typically 3 or fewer hints).
func detectGuidanceConflict(hints []string) string {
	if len(hints) < 2 {
		return ""
	}

	// Precompute uppercase versions for matching.
	upperHints := make([]string, len(hints))
	for idx, h := range hints {
		upperHints[idx] = strings.ToUpper(h)
	}

	for _, cp := range guidanceConflicts {
		// #462: mutual exclusion — a hint matching sideA of a pair must not
		// simultaneously serve as the sideB matcher ("STOP EXPLORING, act
		// now" + "stop exploring further and edit" are both stop-exploring
		// hints, not a conflict).
		for i, ui := range upperHints {
			if !containsAnyKeyword(ui, cp.sideA) || containsAnyKeyword(ui, cp.sideB) {
				continue
			}
			for j, uj := range upperHints {
				if i == j {
					continue
				}
				if containsAnyKeyword(uj, cp.sideB) && !containsAnyKeyword(uj, cp.sideA) {
					return "[guidance-conflict] Contradictory directives detected in concurrent guidance " +
						"(cross-component interference). Two retained hints push in opposite directions. " +
						cp.priority
				}
			}
		}
	}

	return ""
}

// containsAnyKeyword checks if s contains any of the keywords with every
// keyword token anchored at a word start (#462: bare substring matching hit
// stems inside other words, e.g. "EXPLOR" matching inside "STOP EXPLORING"
// from the opposite side's perspective).
func containsAnyKeyword(s string, subs []string) bool {
	for _, sub := range subs {
		if keywordTokensAtWordStart(s, sub) {
			return true
		}
	}
	return false
}

// keywordTokensAtWordStart reports whether keyword appears in s as a
// contiguous phrase starting at a word boundary (#462: bare substring
// matching hit stems inside other words, e.g. "EXPLOR" matching inside
// "STOP EXPLORING" from the opposite side's perspective). The boundary
// check only guards the START of the phrase — prefix keywords like
// "EXPLOR" still match inflected forms (EXPLORE/EXPLORING), while
// mid-word hits ("WIDESPREAD" hitting "WIDER") and non-adjacent token
// splices ("TOO" + "NARROW" from unrelated words) never match.
func keywordTokensAtWordStart(s, keyword string) bool {
	if keyword == "" {
		return false
	}
	idx := 0
	for {
		j := strings.Index(s[idx:], keyword)
		if j < 0 {
			return false
		}
		at := idx + j
		if at == 0 || !isWordRune(s[at-1]) {
			return true
		}
		idx = at + 1
	}
}

// isWordRune reports whether b is a word-continuation byte.
func isWordRune(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '_'
}
