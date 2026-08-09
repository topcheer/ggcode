package agent

// Belief Defense Escalation Detector
//
// Research basis: "When Agents Commit Too Soon: Diagnosing Premature
// Commitment in LLM Agents" (arXiv:2606.22936, Jun 2026) shows that
// long-horizon LLM agents settle on one reading of the evidence EARLY and
// then "spend the rest of the run defending it" even as contradicting
// evidence accumulates. The paper calls this premature commitment: a hidden
// process failure where the trajectory collapses to a stable path too soon.
// Critically, "commitment tells us whether an agent has settled, not whether
// it is right" -- the committed-wrong and committed-correct cases are not
// separable by activation similarity alone, so the failure is invisible to
// final-answer scoring.
//
// This detector targets the OBSERVABLE behavioral surface of that failure:
//   1. The agent states a belief/hypothesis in its text (e.g. "the bug is in
//      the auth middleware", "this is a race condition", "tests should pass").
//   2. A subsequent tool output contradicts that belief (error, no-match,
//      failing test, not-found).
//   3. The agent RE-STATES the same belief in a later iteration instead of
//      updating -- "defending" the settled position against evidence.
//
// Distinction from existing detectors:
//   - premature_commit.go: checks whether ENOUGH evidence was gathered before
//     the FIRST edit. This detector checks whether the agent UPDATES beliefs
//     AFTER contradictory evidence arrives across multiple iterations.
//   - narrative_evidence.go: single-iteration contradiction between text and
//     the immediately preceding tool output. This detector requires the
//     belief to PERSIST across iterations after a contradiction.
//   - solution_fixation.go: repeating the same concrete EDIT approach. This
//     detector tracks conceptual BELIEFS that survive refutation.
//   - diagnostic_fixation.go: re-stating the same root-cause hypothesis while
//     ignoring diagnostics -- narrower, single claim. This detector tracks
//     a broader class of beliefs (causal hypotheses, status claims, existence
//     claims) that survive contradicting evidence.
//
// Detection approach:
//   1. Extract "belief seeds" from assistant text -- statements that commit
//      the agent to a factual/causal position (root cause claims, status
//      claims, existence assertions).
//   2. Record contradicting tool outputs (errors, no-match, failures).
//   3. When the agent re-states a belief that matches an earlier seed AND a
//      contradicting tool output occurred between the two statements, flag it
//      as belief defense escalation.
//
// Design:
//   - Zero LLM cost -- pure heuristic pattern matching.
//   - Non-blocking advisory hint, capped at 2 injections per run.
//   - Fires only when a belief is re-stated AFTER a contradiction (temporal
//     ordering matters), not merely repeated.
//   - Resets each run.

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	beliefDefenseMaxWarnings = 2
	beliefDefenseMaxSeeds    = 12 // ring buffer of belief seeds
)

// beliefSeed captures an early-stated belief and its iteration.
type beliefSeed struct {
	normalized string // normalized key for re-match
	raw        string // raw text snippet for reporting
	iteration  int
	category   string // "root_cause", "status", "existence"
}

// beliefContradiction captures a contradicting tool output.
type beliefContradiction struct {
	iteration int
	toolName  string
	signal    string // short description of the contradicting signal
}

// beliefDefenseState tracks belief seeds and contradictions across the run.
type beliefDefenseState struct {
	warnings       int
	seeds          []beliefSeed
	contradictions []beliefContradiction
}

func newBeliefDefenseState() *beliefDefenseState {
	return &beliefDefenseState{}
}

func (s *beliefDefenseState) reset() {
	s.warnings = 0
	s.seeds = nil
	s.contradictions = nil
}

// --- Belief seed extraction patterns ---

// Root-cause belief: "the issue is X", "the bug is in X", "this is caused by X".
var beliefRootCauseRe = regexp.MustCompile(`(?i)(?:the (?:issue|problem|bug|error|cause|root cause)\s+(?:is|lies in|stems from|is caused by)\s+|this is (?:caused by|due to|because of)\s+|likely (?:cause|culprit)\s*:?\s+|the (?:root )?cause (?:is|appears to be)\s+)([^\n.;]{6,80})`)

// Status belief: "tests pass", "build is clean", "it works", "config is correct".
var beliefStatusRe = regexp.MustCompile(`(?i)(tests? (?:pass|passing|passed|succeed)|build(?:s| is)? (?:clean|green|pass(?:ing|ed)?)|it (?:works|compiles)|config(?:uration)? (?:is )?(?:correct|valid|proper)|no (?:bugs|issues|errors|problems))(?:[.\s]|$)`)

// Existence belief: "X exists", "there is a X", "X is defined".
var beliefExistenceRe = regexp.MustCompile(`(?i)(there (?:is|are) (?:a |an |the )?|the function|the variable|the method|the class|the type|the field|the config)\s+([a-zA-Z_][\w.-]{2,40})\s+(?:exists|is defined|is declared|is present|is available)`)

// --- Contradiction detection in tool outputs ---

// beliefErrorSignalRe matches strong error/failure signals that contradict
// positive status beliefs.
var beliefErrorSignalRe = regexp.MustCompile(`(?i)(---\s*FAIL|FAIL(?:ED|URES?)?|panic:|undefined:\s*\w+|cannot find|not found|no such (?:file|directory)|0 tests? pass|0%.*pass|BUILD FAILURE|compilation failed|syntax error|\.go:\d+:.*error)`)

// beliefNoMatchRe matches empty/no-result signals that contradict existence beliefs.
var beliefNoMatchRe = regexp.MustCompile(`(?i)(no (?:results?|matches?|files? found)|0 (?:results?|matches?|files?)|nothing (?:found|matching)|empty (?:result|set))`)

// recordAssistantText extracts belief seeds from assistant text and checks for
// belief defense escalation. Returns a non-empty hint if escalation is detected.
func (s *beliefDefenseState) recordAssistantText(text string, iteration int) string {
	if s.warnings >= beliefDefenseMaxWarnings || len(text) < 20 {
		// Still extract seeds even if we can't warn, so future logic has data.
		s.extractSeeds(text, iteration)
		return ""
	}

	// First, check if this iteration re-states an earlier belief that has been
	// contradicted. Do this BEFORE adding new seeds from this iteration.
	hint := s.checkEscalation(text, iteration)

	// Extract and store new seeds from this text.
	s.extractSeeds(text, iteration)

	return hint
}

// recordToolResult checks for contradicting signals in tool outputs.
func (s *beliefDefenseState) recordToolResult(toolName, content string, iteration int, isError bool) {
	if isError {
		s.contradictions = append(s.contradictions, beliefContradiction{
			iteration: iteration,
			toolName:  toolName,
			signal:    "error",
		})
		return
	}
	if content == "" {
		return
	}
	if beliefErrorSignalRe.MatchString(content) {
		s.contradictions = append(s.contradictions, beliefContradiction{
			iteration: iteration,
			toolName:  toolName,
			signal:    "failure_signal",
		})
	} else if beliefNoMatchRe.MatchString(content) {
		s.contradictions = append(s.contradictions, beliefContradiction{
			iteration: iteration,
			toolName:  toolName,
			signal:    "no_match",
		})
	}
}

// extractSeeds finds belief statements and stores them as seeds.
func (s *beliefDefenseState) extractSeeds(text string, iteration int) {
	// Root-cause beliefs.
	for _, m := range beliefRootCauseRe.FindAllStringSubmatch(text, 2) {
		if len(m) > 1 {
			raw := strings.TrimSpace(m[1])
			key := normalizeBeliefKey(raw)
			if key != "" {
				s.addSeed(beliefSeed{normalized: key, raw: raw, iteration: iteration, category: "root_cause"})
			}
		}
	}
	// Status beliefs.
	for _, m := range beliefStatusRe.FindAllString(text, 2) {
		raw := strings.Trim(strings.TrimSpace(m), ".,;:!?")
		key := normalizeBeliefKey(raw)
		if key != "" {
			s.addSeed(beliefSeed{normalized: "status:" + key, raw: raw, iteration: iteration, category: "status"})
		}
	}
	// Existence beliefs.
	for _, m := range beliefExistenceRe.FindAllStringSubmatch(text, 2) {
		if len(m) > 2 {
			raw := strings.TrimSpace(m[2])
			key := normalizeBeliefKey(raw)
			if key != "" {
				s.addSeed(beliefSeed{normalized: "exists:" + key, raw: raw, iteration: iteration, category: "existence"})
			}
		}
	}
}

// addSeed adds a seed to the ring buffer (deduplicated within the same
// iteration to avoid self-matching).
func (s *beliefDefenseState) addSeed(seed beliefSeed) {
	// Deduplicate: if an identical normalized seed exists from the same iter, skip.
	for _, existing := range s.seeds {
		if existing.normalized == seed.normalized && existing.iteration == seed.iteration {
			return
		}
	}
	s.seeds = append(s.seeds, seed)
	if len(s.seeds) > beliefDefenseMaxSeeds {
		s.seeds = s.seeds[len(s.seeds)-beliefDefenseMaxSeeds:]
	}
}

// checkEscalation detects when a belief from an earlier iteration is re-stated
// in the current iteration AND a contradicting tool output occurred in between.
func (s *beliefDefenseState) checkEscalation(text string, iteration int) string {
	lowerText := strings.ToLower(text)

	for _, seed := range s.seeds {
		// Only consider seeds from EARLIER iterations.
		if seed.iteration >= iteration {
			continue
		}
		// Check if the belief is re-stated in this text.
		if !beliefRestated(lowerText, seed) {
			continue
		}
		// Check if a contradicting tool output occurred AFTER the seed and
		// BEFORE (or at) this iteration.
		var matchingContra *beliefContradiction
		for idx := range s.contradictions {
			c := &s.contradictions[idx]
			if c.iteration > seed.iteration && c.iteration <= iteration {
				// For existence beliefs, "no_match" is the relevant contradiction.
				// For status beliefs, "failure_signal" or "error" is relevant.
				// For root_cause, any contradiction can refute.
				if seed.category == "existence" && c.signal != "no_match" {
					continue
				}
				matchingContra = c
				break
			}
		}
		if matchingContra == nil {
			continue
		}

		// Escalation detected: belief re-stated after contradiction.
		s.warnings++
		return formatBeliefDefenseHint(seed, *matchingContra, iteration)
	}
	return ""
}

// beliefRestated checks whether the belief seed is re-asserted in the text.
func beliefRestated(lowerText string, seed beliefSeed) bool {
	switch seed.category {
	case "root_cause":
		// Root-cause beliefs are matched by their key tokens appearing together.
		return beliefKeyInText(lowerText, seed.normalized)
	case "status":
		// Status beliefs: check if the same positive status is re-claimed.
		rawLower := strings.ToLower(seed.raw)
		prefix := rawLower
		if len(prefix) > 12 {
			prefix = prefix[:12]
		}
		return strings.Contains(lowerText, prefix)
	case "existence":
		// Existence beliefs: check if the entity name appears with existence verb.
		return beliefKeyInText(lowerText, seed.normalized)
	}
	return false
}

// beliefKeyInText checks if the normalized belief key (space-separated tokens)
// appears in the text with token-level fuzzy matching (tokens within proximity).
func beliefKeyInText(lowerText, key string) bool {
	key = strings.TrimPrefix(key, "root_cause:")
	key = strings.TrimPrefix(key, "exists:")
	tokens := strings.Fields(key)
	if len(tokens) == 0 {
		return false
	}
	// All key tokens must appear in the text.
	for _, tok := range tokens {
		if len(tok) < 3 {
			continue
		}
		if !strings.Contains(lowerText, tok) {
			return false
		}
	}
	return true
}

// normalizeBeliefKey normalizes a belief text into a token-based key.
func normalizeBeliefKey(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	// Remove articles and filler words.
	for _, w := range []string{"the ", "a ", "an ", "this ", "that ", "these ", "those "} {
		s = strings.TrimPrefix(s, w)
	}
	s = strings.Trim(s, ".,;:!?\"'()[]{}")
	// Collapse whitespace.
	s = regexp.MustCompile(`\s+`).ReplaceAllString(s, " ")
	if len(s) < 3 || len(s) > 80 {
		return ""
	}
	return s
}

func formatBeliefDefenseHint(seed beliefSeed, contra beliefContradiction, iter int) string {
	return "[Belief Defense Escalation] In iteration " + fmt.Sprintf("%d", iter) +
		" you re-stated an earlier belief (\"" + bdTruncate(seed.raw, 60) +
		"\", first stated in iteration " + fmt.Sprintf("%d", seed.iteration) +
		") despite a contradicting " + contra.signal + " signal from " + contra.toolName +
		" in iteration " + fmt.Sprintf("%d", contra.iteration) + ". " +
		"Research shows agents that settle on one reading of the evidence early and defend it " +
		"against incoming data fail silently (arXiv:2606.22936). Re-examine the contradicting evidence " +
		"and update your hypothesis before continuing."
}

func bdTruncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
