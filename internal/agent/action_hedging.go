package agent

// Action Hedging Detector - Verbalized Uncertainty During Mutations
//
// Research basis:
//   - "Agentic Confidence Calibration" (arXiv 2601.15778, Jan 2026): introduces
//     Holistic Trajectory Calibration and identifies "verbalized uncertainty"
//     as a key process-level feature. Agents that express hedging while
//     performing actions are poorly calibrated - they know the action may
//     not work but proceed anyway.
//   - MetaCogAgent (arXiv 2605.17292): metacognitive self-assessment before
//     execution. Agents should assess task-capability alignment BEFORE acting,
//     not hedge while already mutating code.
//   - Agent Loop Termination Pattern (agentnative.dev, 2026): overconfidence in
//     failure remains a fundamental barrier. Hedging language is the inverse
//     signal - the agent knows it's uncertain but doesn't convert that
//     awareness into verification.
//
// Problem: AI coding agents sometimes express low confidence in their approach
// in the SAME message where they perform a code edit:
//
//  1. "This should hopefully fix the issue" → proceeds with edit_file
//  2. "Let's try this approach and see if it works" → writes new code
//  3. "I'm not entirely sure but this might be the right fix" → edits anyway
//  4. "This is a best guess at the fix" → commits unverified change
//  5. "If this doesn't work, we can try something else" → doesn't verify first
//
// This is a metacognitive failure: the agent has enough awareness to express
// uncertainty but lacks the discipline to convert it into pre-action
// verification. Research shows these edits fail at significantly higher rates
// than confident, evidence-backed edits.
//
// Existing ggcode detectors that are RELATED but do NOT cover this:
//   - assumption_track.go: detects unverified beliefs about state/requirements
//     ("I assume the database is PostgreSQL"). This detector targets
//     uncertainty about the ACTION being taken in the same turn.
//   - unverified_claim_verify.go: detects post-action success claims without
//     verification ("I've fixed the issue"). This detector targets
//     pre-action hedging before/during the edit.
//   - premature_commitment.go: checks if first edit happened too quickly
//     (insufficient exploration). This targets language accompanying edits.
//   - trajectory_confidence.go: trajectory-level tool success rates, not
//     per-action text analysis.
//
// Gap: No detector captures the specific anti-pattern of hedging language
// accompanying a mutation action. This detector addresses that gap by
// scanning assistant text in iterations that contain editing tools and
// flagging uncertainty expressions that indicate the agent should verify
// its approach before proceeding.
//
// Design:
//   - Scans assistant text in iterations containing mutation tools
//   - Detects hedging language: "hopefully", "might fix", "let's try",
//     "best guess", "not sure but", "if this doesn't work", etc.
//   - Only fires when hedging accompanies an actual edit action
//   - Threshold: 2+ hedging signals → inject guidance to verify first
//   - Zero LLM cost - pure deterministic text pattern matching
//   - Fires at most 2 times per run (advisory, non-blocking)

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	// hedgingWarnThreshold: at this count, inject guidance.
	hedgingWarnThreshold = 2

	// hedgingMaxWarnings: max warnings per run.
	hedgingMaxWarnings = 2

	// hedgingMaxExamples: max hedging examples to include in hint.
	hedgingMaxExamples = 3
)

// hedgingPattern represents a detected action-hedging language pattern.
type hedgingPattern struct {
	level   string // "HIGH" or "MEDIUM"
	pattern *regexp.Regexp
}

// Precompiled patterns for performance. Case-insensitive.
// These capture verbalized uncertainty expressed alongside actions.
var actionHedgingPatterns = []hedgingPattern{
	// HIGH confidence - explicit uncertainty about the action's correctness
	{"HIGH", regexp.MustCompile(`(?i)\bthis should hopefully\b`)},
	{"HIGH", regexp.MustCompile(`(?i)\bhopefully this (?:fix|work|resolv|address)`)},
	{"HIGH", regexp.MustCompile(`(?i)\bthis might (?:fix|work|resolv|be|not)\b`)},
	{"HIGH", regexp.MustCompile(`(?i)\bthis may (?:fix|work|not|or)\b`)},
	{"HIGH", regexp.MustCompile(`(?i)\bI'm not (?:entirely |completely |100% )?sure\b.*\b(but|yet|still)\b`)},
	{"HIGH", regexp.MustCompile(`(?i)\bnot sure if (?:this|that)\b`)},
	{"HIGH", regexp.MustCompile(`(?i)\bbest guess\b`)},
	{"HIGH", regexp.MustCompile(`(?i)\beducated guess\b`)},
	// #1436-C: the literal 'if this doesn't work' pattern was removed -
	// it is a strict SUBSET of the generalized regex below; both matching
	// the same sentence double-counted it, and the dedup key
	// (level+":"+excerpt) differs by match end, so one hedging sentence
	// hit the threshold (2) alone.
	{"HIGH", regexp.MustCompile(`(?i)\bif (?:this|that) (?:doesn't|does not|fails?)\b`)},
	// #359: distance-capped and first-person scoped — describing a code path
	// ("uses the exponential fallback if all brokers are down") is technical
	// narration, not a pre-action hedge.
	{"HIGH", regexp.MustCompile(`(?i)\b(?:we|i)'?(?:ll| will)? ?(?:use|fall back to|switch to)? ?a? ?fallback\b[^.]{0,40}\bif\b`)},
	// #359: "let's try X" is confident iteration, not high-uncertainty hedging
	// (compare MEDIUM "see if this works"); narrowed + downgraded.
	{"MEDIUM", regexp.MustCompile(`(?i)\blet'?s try (?:this|that|it)\b`)},
	{"HIGH", regexp.MustCompile(`(?i)\btrial and error\b`)},
	{"HIGH", regexp.MustCompile(`(?i)\bthis is (?:a )?shot in the dark\b`)},

	// MEDIUM confidence - tentative/provisional language about the approach
	{"MEDIUM", regexp.MustCompile(`(?i)\bI think this (?:should|will|might|could|may)\b`)},
	{"MEDIUM", regexp.MustCompile(`(?i)\bthis ought to\b`)},
	{"MEDIUM", regexp.MustCompile(`(?i)\bthis is worth a try\b`)},
	{"MEDIUM", regexp.MustCompile(`(?i)\bsee if (?:this|that) works\b`)},
	{"MEDIUM", regexp.MustCompile(`(?i)\bfingers crossed\b`)},
	{"MEDIUM", regexp.MustCompile(`(?i)\bno guarantees?\b`)},
	{"MEDIUM", regexp.MustCompile(`(?i)\bcould be wrong\b`)},
	{"MEDIUM", regexp.MustCompile(`(?i)\bmight be (?:wrong|mistaken|incorrect)\b`)},
	{"MEDIUM", regexp.MustCompile(`(?i)\bI could be off\b`)},
}

// hedgingHit records a single detected hedging signal.
type hedgingHit struct {
	level   string
	excerpt string
}

// actionHedgingState tracks hedging detections across a run.
type actionHedgingState struct {
	warnings int
}

func newActionHedgingState() *actionHedgingState {
	return &actionHedgingState{}
}

func (s *actionHedgingState) reset() {
	s.warnings = 0
}

// isMutationTool returns true if the tool name is a code-editing/mutation tool.
// Derived from the canonical sourceMutatingTools superset plus git side-effect
// tools (#738) so the file-editing members can never drift.
func isMutationTool(toolName string) bool {
	return sourceMutatingTools[toolName] ||
		toolName == "git_commit" || toolName == "git_add" || toolName == "git_reset" ||
		toolName == "git_revert" || toolName == "git_checkout" || toolName == "git_stash"
}

// scanActionHedging analyzes assistant text for action-hedging language.
// Returns the list of detected hedging hits (sorted HIGH first).
func scanActionHedging(text string) []hedgingHit {
	if len(text) == 0 {
		return nil
	}

	var hits []hedgingHit
	seen := make(map[string]bool) // deduplicate by excerpt

	for _, hp := range actionHedgingPatterns {
		locs := hp.pattern.FindAllStringIndex(text, -1)
		for _, loc := range locs {
			start := loc[0]
			// Extract a short excerpt around the match for context.
			excerptStart := start - 20
			if excerptStart < 0 {
				excerptStart = 0
			}
			excerptEnd := loc[1] + 40
			if excerptEnd > len(text) {
				excerptEnd = len(text)
			}
			excerpt := strings.TrimSpace(text[excerptStart:excerptEnd])
			// Truncate long excerpts.
			if len(excerpt) > 80 {
				excerpt = excerpt[:80] + "..."
			}

			// Deduplicate by excerpt content.
			key := hp.level + ":" + excerpt
			if seen[key] {
				continue
			}
			seen[key] = true

			hits = append(hits, hedgingHit{
				level:   hp.level,
				excerpt: excerpt,
			})
		}
	}

	return hits
}

// maybeWarnActionHedging checks assistant text for hedging language
// when the iteration contains mutation tools. Returns a guidance message
// if the threshold is exceeded. Returns empty string if no warning needed.
func (a *Agent) maybeWarnActionHedging(text string, hasMutation bool) string {
	if a.actionHedging == nil {
		return ""
	}
	if a.actionHedging.warnings >= hedgingMaxWarnings {
		return ""
	}
	// Only fire when hedging accompanies an actual edit action.
	if !hasMutation {
		return ""
	}

	hits := scanActionHedging(text)
	if len(hits) < hedgingWarnThreshold {
		return ""
	}

	// Count by level.
	highCount := 0
	for _, h := range hits {
		if h.level == "HIGH" {
			highCount++
		}
	}

	a.actionHedging.warnings++

	// Build examples list (prioritize HIGH).
	var examples []string
	for _, h := range hits {
		if len(examples) >= hedgingMaxExamples {
			break
		}
		examples = append(examples, fmt.Sprintf("  [%s] ...%s...", h.level, h.excerpt))
	}

	severity := "INFO"
	if highCount >= 1 {
		severity = "WARNING"
	}

	return fmt.Sprintf(
		"[%s-action-hedging] Detected %d verbalized uncertainty signal(s) "+
			"(%d HIGH, %d MEDIUM) in a turn that includes a code mutation. "+
			"Your hedging language indicates you are not confident in the fix "+
			"you are applying. This is a metacognitive signal: you have enough "+
			"awareness to express doubt but are proceeding without verification. "+
			"Before making further edits: (1) re-read the relevant code to confirm "+
			"root cause, (2) check if there is existing test coverage, "+
			"(3) consider asking the user for clarification on ambiguous requirements. "+
			"Do not apply speculative edits when you can verify first.\n"+
			"Detected hedging signals:\n%s",
		severity, len(hits), highCount, len(hits)-highCount,
		strings.Join(examples, "\n"),
	)
}
