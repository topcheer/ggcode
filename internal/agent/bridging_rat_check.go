package agent

// Bridging Rationalization Detector
//
// Research basis: RECAP (KriraAI, Jul 2026) and the stale-context benchmark
// (Meetless, Jul 2026) identify a distinct agent failure mode called "stale
// premise propagation." When an agent's belief is contradicted by a new
// observation, the agent frequently produces a BRIDGING RATIONALIZATION --
// a plausible-sounding reconciliation that preserves both the stale belief
// and the contradicting observation by attributing the discrepancy to an
// external cause. RECAP calls this "confident monotonicity": the model
// produces "a better reconciliation but worse revision." The Meetless
// benchmark found agents write the fully stale version with ZERO files
// read in 100% of floor trials, because the agent rationalizes rather
// than investigates.
//
// Example failure pattern:
//   1. Agent reads file, observes "func foo() returns int"
//   2. Agent edits file, changing the function signature
//   3. Agent's test fails (the actual return type changed)
//   4. Agent says: "this is expected, the environment may have changed"
//      or "this discrepancy is likely due to a cached build artifact"
//      -- instead of re-reading the file to see the new state
//
// The bridging rationalization itself is the failure. The agent does NOT
// re-verify; it explains away the contradiction and continues acting on
// the stale premise.
//
// Distinction from existing detectors:
//   - belief_defense.go: requires a belief to be RE-STATED after contradiction
//     across iterations. This detector fires on the RATIONALIZATION RESPONSE
//     in a single iteration, even if the original belief is never re-stated.
//   - narrative_evidence.go: text contradicts the IMMEDIATELY preceding tool
//     output. This detector targets rationalization patterns that EXPLAIN AWAY
//     a contradiction rather than directly contradicting it.
//   - premature_surrender.go: agent giving up too early. This is the opposite:
//     the agent PERSISTS by rationalizing instead of re-verifying.
//   - diagnostic_fixation.go: re-stating the same root-cause hypothesis.
//     This detector targets the broader "environment must have changed" class
//     of rationalizations regardless of any specific hypothesis.
//
// Detection approach:
//   1. Track tool outputs that contain contradiction signals (errors, failures,
//      not-found, mismatches).
//   2. In the NEXT assistant text after a contradiction, look for bridging
//      rationalization patterns: "the environment may have changed", "this is
//      expected", "likely a stale cache", "probably cached", "might be out of
//      sync", "perhaps a timing issue", "this discrepancy is due to ...".
//   3. If found AND the agent does NOT subsequently re-read or re-verify the
//      relevant file/state, flag the rationalization.
//
// Design:
//   - Zero LLM cost -- pure heuristic pattern matching.
//   - Non-blocking advisory hint, capped at 2 injections per run.
//   - Fires only when a rationalization appears AFTER a contradiction (temporal
//     ordering matters) and the agent doesn't re-verify.
//   - Resets each run.

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	bridgingMaxWarnings = 2
)

// bridgingRatState tracks contradictions and bridging rationalizations.
type bridgingRatState struct {
	warnings       int
	contradictions []bridgingContradiction
}

type bridgingContradiction struct {
	iteration int
	toolName  string
	signal    string // "error", "failure", "not_found", "mismatch"
}

func newBridgingRatState() *bridgingRatState {
	return &bridgingRatState{}
}

func (s *bridgingRatState) reset() {
	s.warnings = 0
	s.contradictions = nil
}

// --- Contradiction signals in tool outputs ---

var bridgingErrorRe = regexp.MustCompile(`(?i)(---\s*FAIL|FAIL(?:ED|URES?)?|panic:|undefined:\s*\w+|cannot find|not found|no such (?:file|directory)|BUILD FAILURE|compilation failed|syntax error|error[: ]|\.go:\d+:.*error)`)
var bridgingNotFoundRe = regexp.MustCompile(`(?i)(no (?:results?|matches?|files? found)|0 (?:results?|matches?|files?)|nothing (?:found|matching)|empty (?:result|set))`)
var bridgingMismatchRe = regexp.MustCompile(`(?i)(mismatch|did not match|expected.*got|assertion failed|unexpected (?:result|output|value)|exit code [1-9]|returncode[: ]*[1-9])`)

// --- Bridging rationalization patterns in assistant text ---
//
// These are phrases that reconcile a contradiction by attributing it to an
// external/transient cause rather than investigating the actual state.

var bridgingRatPatterns = []*regexp.Regexp{
	// Environment/external attribution
	regexp.MustCompile(`(?i)(?:the |this )?(?:environment|filesystem|system|workspace|repo)\s+(?:may |might |probably |likely |must have )?(?:has |have |has been )?changed`),
	// Caching/transient state attribution
	regexp.MustCompile(`(?i)(?:stale|cached|outdated|old)\s+(?:cache|build|artifact|binary|compilation|state|index)`),
	regexp.MustCompile(`(?i)(?:probably|likely|might be|may be|could be)\s+(?:a |an )?(?:stale|cached|outdated|old)`),
	// "This is expected" dismissal
	regexp.MustCompile(`(?i)this (?:is|was|seems) (?:expected|normal|fine|ok(?:ay)?|by design|understandable)`),
	// Timing/race attribution
	regexp.MustCompile(`(?i)(?:probably|likely|might be|may be|could be)\s+(?:a )?(?:timing|race|transient|temporary|intermittent)\s+(?:issue|condition|problem|error|failure)`),
	// Sync/version divergence attribution
	regexp.MustCompile(`(?i)(?:out of sync|desynchronized|version (?:mismatch|difference)|path (?:mismatch|difference))`),
	// "Due to" external cause
	regexp.MustCompile(`(?i)(?:this |the )?discrepancy\s+(?:is|was|might be|may be|could be|is likely)\s+(?:due to|caused by|from)\s`),
	// External modification attribution
	regexp.MustCompile(`(?i)(?:may have|might have|probably|likely)\s+(?:been )?(?:modified|changed|updated|overwritten|altered)\s+(?:externally|by (?:another|a different)|by someone|outside)`),
}

// recordToolResult checks for contradicting signals in tool outputs.
func (s *bridgingRatState) recordToolResult(toolName, content string, iteration int, isError bool) {
	if isError {
		s.contradictions = append(s.contradictions, bridgingContradiction{
			iteration: iteration,
			toolName:  toolName,
			signal:    "error",
		})
		return
	}
	if content == "" {
		return
	}
	switch {
	case bridgingErrorRe.MatchString(content):
		s.contradictions = append(s.contradictions, bridgingContradiction{
			iteration: iteration, toolName: toolName, signal: "failure",
		})
	case bridgingMismatchRe.MatchString(content):
		s.contradictions = append(s.contradictions, bridgingContradiction{
			iteration: iteration, toolName: toolName, signal: "mismatch",
		})
	case bridgingNotFoundRe.MatchString(content):
		s.contradictions = append(s.contradictions, bridgingContradiction{
			iteration: iteration, toolName: toolName, signal: "not_found",
		})
	}
}

// hasRecentContradiction checks if a contradiction occurred in a recent prior
// iteration (within the last few iterations, since compaction may remove
// older ones).
func (s *bridgingRatState) hasRecentContradiction(currentIter int) *bridgingContradiction {
	for i := len(s.contradictions) - 1; i >= 0; i-- {
		c := &s.contradictions[i]
		gap := currentIter - c.iteration
		if gap >= 1 && gap <= 3 {
			return c
		}
	}
	return nil
}

// checkRationalization scans assistant text for bridging rationalization
// patterns that appeared after a recent contradiction. Returns a hint if found.
func (s *bridgingRatState) checkRationalization(text string, iteration int) string {
	if s.warnings >= bridgingMaxWarnings || len(text) < 15 {
		return ""
	}

	// Must have a recent contradiction.
	contra := s.hasRecentContradiction(iteration)
	if contra == nil {
		return ""
	}

	// Check for bridging rationalization patterns.
	matched := false
	var sample string
	for _, pat := range bridgingRatPatterns {
		loc := pat.FindStringIndex(text)
		if loc != nil {
			matched = true
			end := loc[1]
			if end-loc[0] > 60 {
				end = loc[0] + 60
			}
			sample = strings.TrimSpace(text[loc[0]:end])
			break
		}
	}
	if !matched {
		return ""
	}

	s.warnings++

	return fmt.Sprintf(
		"[Bridging Rationalization] In iteration %d, after a %s signal from %s (iteration %d), "+
			"you wrote \"%s...\". Research (RECAP 2026, stale-context benchmark) shows agents frequently "+
			"reconcile contradictions by attributing them to external/transient causes instead of "+
			"re-verifying the actual state. This is the dominant silent failure mode in long-horizon "+
			"agents: the premise stays stale and the agent continues acting on outdated information. "+
			"Re-read the relevant file or re-run the check to confirm the current state before proceeding.",
		iteration, contra.signal, contra.toolName, contra.iteration, sample,
	)
}
