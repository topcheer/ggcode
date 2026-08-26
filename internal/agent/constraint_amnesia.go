package agent

// Constraint Amnesia Detector
//
// Research basis:
//   - Catastrophic Forgetting in Token Space (Letta/MemGPT 2025): as context
//     windows grow, early-established constraints are effectively "pushed out"
//     of the model's attention. The agent forgets rules it already acknowledged.
//   - Langchain 2025 production analysis: "agents forgetting established user
//     preferences after capability expansions" is a top-3 failure mode.
//   - Agent Drift paper (arXiv:2601.04170): semantic drift = "progressive
//     deviation from original intent" - constraints set at the start of a
//     long conversation are the first victims of drift.
//   - Continual Learning in LLM Agents (arXiv:2511.01093): context-space
//     continual learning requires explicit curation; without it, constraints
//     erode silently.
//
// Problem: When a user says "don't modify file X", "use only pattern Y",
// "no new dependencies", or "avoid touching the auth module", the agent
// acknowledges the constraint early. But after 15-20+ iterations of tool
// calls, build output, and file reads, that constraint has scrolled far
// from the top of the context window. The agent then:
//
//  1. Edits the file it was told not to touch
//  2. Adds a dependency it was told to avoid
//  3. Refactors code in the module it was told to leave alone
//  4. Uses a different pattern than what was specified
//
// This is NOT the same as scope drift (which tracks file diversity) or
// premature commitment (which checks evidence before first edit). This
// detector specifically tracks EXPLICIT user constraints and reminds the
// agent of them when context has grown large enough that they may have
// been forgotten.
//
// Design:
//   - Extracts explicit constraints from user messages via pattern matching
//     (negation constraints, exclusivity constraints, requirement constraints)
//   - Tracks when each constraint was established (iteration number)
//   - Injects a reminder when: (a) context has grown significantly
//     (>12 iterations or >50K tokens since constraint), AND (b) the agent
//     is about to make or has made an edit that plausibly relates to a
//     tracked constraint
//   - Zero LLM cost - pure deterministic pattern matching
//   - Fires at most 2 times per run (advisory, non-blocking)

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	// constraintReminderMinIterations: after this many iterations since
	// the constraint was established, the constraint is at risk of being
	// "pushed out" of effective attention.
	constraintReminderMinIterations = 12

	// constraintMaxWarnings: max reminders per run to avoid noise.
	constraintMaxWarnings = 1

	// constraintMaxTracked: max constraints to track (bounded memory).
	constraintMaxTracked = 8

	// constraintExcerptLen: max chars per constraint excerpt.
	constraintExcerptLen = 120
)

// constraintEntry represents a single extracted user constraint.
type constraintEntry struct {
	excerpt    string // the constraint text
	messageIdx int    // which user message it came from
}

// constraintAmnesiaState tracks user-established constraints across a run.
type constraintAmnesiaState struct {
	constraints []constraintEntry
	warnings    int
	currentIter int
}

func newConstraintAmnesiaState() *constraintAmnesiaState {
	return &constraintAmnesiaState{}
}

func (s *constraintAmnesiaState) reset() {
	s.constraints = nil
	s.warnings = 0
	s.currentIter = 0
}

// Constraint extraction patterns. Case-insensitive.
// These capture explicit, directive constraints from user text.
var constraintPatterns = []*regexp.Regexp{
	// Negation constraints: "don't", "do not", "never", "no new"
	regexp.MustCompile(`(?i)(?:don'?t|do not|never|no new|avoid|must not|should not)\b[^\n.]{5,80}`),
	// Exclusivity constraints: "only use", "must use", "always use"
	regexp.MustCompile(`(?i)\b(?:only|must|always|exclusively)\s+(?:use|modify|touch|edit|change|add|import|call)\b[^\n.]{3,60}`),
	// Requirement constraints: "needs to be", "has to", "it's important that"
	regexp.MustCompile(`(?i)\b(?:needs? to be|has to be|it'?s important that|make sure|ensure that)\b[^\n.]{5,70}`),
	// Scope restrictions: "stay in", "limit to", "keep within"
	regexp.MustCompile(`(?i)\b(?:stay in|limit (?:to|changes to)|keep within|don'?t touch|leave\b[^n]{0,5}alone)\b[^\n.]{3,60}`),
}

// extractConstraints scans user text for explicit constraint directives.
// Returns extracted constraint excerpts (deduplicated).
func extractConstraints(text string) []string {
	if len(text) == 0 {
		return nil
	}

	var results []string
	seen := make(map[string]bool)

	for _, pat := range constraintPatterns {
		matches := pat.FindAllString(text, -1)
		for _, m := range matches {
			excerpt := strings.TrimSpace(m)
			// Normalize and truncate. #1029: rune-safe truncation -- byte slicing
			// here could split a multi-byte CJK/emoji rune and inject invalid UTF-8
			// into the reminder text (same family as #934).
			if len(excerpt) > constraintExcerptLen {
				runes := []rune(excerpt)
				cut := constraintExcerptLen
				if cut > len(runes) {
					cut = len(runes)
				}
				excerpt = string(runes[:cut]) + "..."
			}
			// Deduplicate by first 40 chars (fuzzy).
			key := strings.ToLower(excerpt)
			if len(key) > 40 {
				key = key[:40]
			}
			if seen[key] {
				continue
			}
			seen[key] = true
			results = append(results, excerpt)
		}
	}

	return results
}

// recordConstraints extracts and stores constraints from a user message.
// messageIdx is the 1-based index of the user message in the conversation.
func (s *constraintAmnesiaState) recordConstraints(text string, messageIdx int) {
	if s == nil {
		return
	}
	extracted := extractConstraints(text)
	for _, ex := range extracted {
		if len(s.constraints) >= constraintMaxTracked {
			break
		}
		s.constraints = append(s.constraints, constraintEntry{
			excerpt:    ex,
			messageIdx: messageIdx,
		})
	}
}

// maybeWarn checks whether any tracked constraints are at risk of being
// forgotten and returns a reminder message if so. iteration is the current
// 1-based agent loop iteration.
func (s *constraintAmnesiaState) maybeWarn(iteration int) string {
	if s == nil || len(s.constraints) == 0 {
		return ""
	}
	if s.warnings >= constraintMaxWarnings {
		return ""
	}

	s.currentIter = iteration

	// Check if enough iterations have passed since the earliest constraint
	// was established. If the constraint was set at message 1 and we're now
	// at iteration 15, the constraint is deep in the context window.
	earliestMsgIdx := s.constraints[0].messageIdx
	iterationsSince := iteration - earliestMsgIdx
	if iterationsSince < constraintReminderMinIterations {
		return ""
	}

	s.warnings++

	// Build the reminder with tracked constraints.
	var lines []string
	for i, c := range s.constraints {
		lines = append(lines, fmt.Sprintf("  %d. \"%s\"", i+1, c.excerpt))
	}

	return fmt.Sprintf(
		"[constraint-reminder] You are %d iterations into this task. "+
			"Constraints established early in the conversation may have "+
			"scrolled out of your active attention. Before making your next "+
			"edit, verify compliance with these user-specified constraints:\n%s\n"+
			"If any pending action would violate a constraint, stop and reconsider.",
		iterationsSince,
		strings.Join(lines, "\n"),
	)
}
