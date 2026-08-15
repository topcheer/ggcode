package agent

// Plan Abandonment Detector
//
// Research basis:
//   - beginnersinai.org "Why AI Coding Agents Fail: The 9 Failure Modes"
//     (2026): "Half-finished work" is a top failure mode - agents declare
//     multi-step plans but abandon steps midway, claiming completion without
//     executing all declared steps. SWE-bench data shows this is an agent
//     discipline problem, not a model capability problem.
//   - METR "Frontier Risk Report" (2026): incomplete plan execution is a
//     leading cause of agent-produced broken/half-finished code.
//   - Angie Agee "Blast Radius for AI Agents" (2026): unexecuted plan steps
//     represent unfulfilled obligations that silently expand the blast radius
//     of a change (e.g., "I'll update the callers" - never done).
//
// Problem: AI coding agents frequently declare a multi-step plan:
//
//  1. "First, I'll read the auth module"
//  2. "Next, I'll fix the token refresh logic"
//  3. "Then, I'll update all callers"
//  4. "Finally, I'll run the tests to verify"
//
// But then claim "Done!" or "The task is complete" after executing only 1-2
// steps, silently abandoning the rest. Users trust the completion claim and
// ship incomplete work.
//
// Existing ggcode detectors that are RELATED but do NOT cover this:
//   - verification_debt.go: tracks missing verification (tests/build), not
//     unexecuted plan steps in general.
//   - fulfillment_gate.go: checks if the response addresses the user's
//     request, not whether the agent's own declared plan was completed.
//   - deferred_work.go: detects "I'll do this later" language, not the
//     declare-plan-then-claim-done-without-executing pattern.
//
// Gap: No detector tracks the agent's declared plan steps across iterations
// and warns when a completion claim arrives before those steps were
// addressed. This detector fills that gap.
//
// Design:
//   - Tracks the maximum number of future-tense plan steps declared in any
//     assistant response (numbered lists with action verbs).
//   - When a completion signal appears ("done", "complete", "finished") and
//     3+ plan steps were declared in a prior iteration that were never
//     superseded, inject guidance to verify all declared steps were executed.
//   - Zero LLM cost - pure deterministic text pattern matching.
//   - Fires at most 2 times per run (advisory, non-blocking).

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	// planAbandonMinSteps: minimum declared future-tense steps to trigger.
	planAbandonMinSteps = 3

	// planAbandonMaxWarnings: max warnings per run.
	planAbandonMaxWarnings = 2

	// planAbandonMaxStepsShown: max step excerpts in hint.
	planAbandonMaxStepsShown = 4
)

// planStepRe matches numbered list items with future-tense action language.
// Matches patterns like:
//
//	"1. I'll read the file"
//	"2) Next, I need to update callers"
//	"3. Then fix the tests"
var planStepRe = regexp.MustCompile(`(?im)^\s*\d+[.)]\s+(.+)`)

// planFutureVerbRe detects future-tense action language in a plan step line,
// indicating the step is declared but not yet executed.
var planFutureVerbRe = regexp.MustCompile(`(?i)(?:i(?:'ll|\s+will|\s+won't)(?:\s+need\s+to)?\s+|i\s+need\s+to\s+|i'm\s+going\s+to\s+|i\s+am\s+going\s+to\s+|next,?\s*i(?:'ll|\s+will)?\s+|then\s+i(?:'ll|\s+will)?\s+|finally,?\s*i(?:'ll|\s+will)?\s+|i\s+plan\s+to\s+|let\s+me\s+(?:also\s+)?(?:read|fix|update|check|run|create|add|remove|modify|verify|test|write|implement|refactor))`)

// planCompletionRe detects completion signals indicating the agent believes
// its work is finished.
var planCompletionRe = regexp.MustCompile(`(?i)(?:^|[\n.])\s*(?:the\s+)?(?:task|work|fix|change|update|implementation|feature|bug)\s+(?:(?:is|has\s+been)\s+)?(?:now\s+)?(?:done|complete|finished|ready|applied|implemented|resolved)|^done[.!]|all\s+done|finished\s+(?:the\s+)?(?:task|work|fix|implementation)|that\s+(?:should\s+\w*\s*it|wraps?\s+up|completes?)|this\s+completes`)

// planStepEditVerbRe detects edit-class verbs in a declared step — these
// steps are verifiable against RunStats.FilesEdited (#490).
var planStepEditVerbRe = regexp.MustCompile(`(?i)\b(?:fix(?:es|ed)?|updat(?:e|es|ed)|cre(?:ate|ates|ated)|add(?:s|ed)?|remov(?:e|es|ed)|modif(?:y|ies|ied)|writ(?:e|es|ing)|implement(?:s|ed)?|refactor(?:s|ed)?|renam(?:e|es|ed)|delet(?:e|es|ed)|patch(?:es|ed)?)\b`)

// planStepRunVerbRe detects run/verify-class verbs in a declared step —
// these are verifiable against RunStats.CommandsRun (#490).
var planStepRunVerbRe = regexp.MustCompile(`(?i)\b(?:run(?:s|ning)?|ra\b|ran\b|test(?:s|ed|ing)?|verif(?:y|ies|ied)|build(?:s|ing)?|built\b|buil[dt]|check(?:s|ed|ing)?|execut(?:e|es|ed))\b`)

// planStepExcerpt extracts a short excerpt from a plan step line.
func planStepExcerpt(line string) string {
	line = strings.TrimSpace(line)
	if len(line) > 60 {
		return line[:60] + "..."
	}
	return line
}

// extractFuturePlanSteps finds numbered plan steps with future-tense language
// in assistant text. Returns the step excerpts.
func extractFuturePlanSteps(text string) []string {
	if len(text) == 0 {
		return nil
	}

	matches := planStepRe.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return nil
	}

	var steps []string
	seen := make(map[string]bool)
	for _, m := range matches {
		line := strings.TrimSpace(m[1])
		if len(line) == 0 {
			continue
		}
		if !planFutureVerbRe.MatchString(line) {
			continue
		}
		excerpt := planStepExcerpt(line)
		if seen[excerpt] {
			continue
		}
		seen[excerpt] = true
		steps = append(steps, excerpt)
	}

	return steps
}

// hasCompletionSignal checks if the text contains a task-completion claim.
func hasCompletionSignal(text string) bool {
	if len(text) == 0 {
		return false
	}
	return planCompletionRe.MatchString(text)
}

// planAbandonState tracks declared plan steps and completion signals.
type planAbandonState struct {
	// steps from the most recent plan declaration (future-tense steps only)
	declaredSteps []string
	// iteration index when the plan was last declared (-1 = no active plan)
	planIter int
	warnings int
	curIter  int
}

func newPlanAbandonState() *planAbandonState {
	return &planAbandonState{planIter: -1}
}

func (s *planAbandonState) reset() {
	s.declaredSteps = nil
	s.planIter = -1
	s.warnings = 0
	s.curIter = 0
}

// planHasExecutionGap reports whether the declared steps show a MISSING
// execution-evidence channel (#490). The completion claim is only suspicious
// when a declared step CATEGORY has zero matching evidence:
//   - an edit-class step was declared but no files were ever edited, or
//   - a run-class step was declared but no commands were ever run.
//
// Steps whose category has evidence, and steps with no evidence channel at
// all (pure read steps), do not contribute a gap — the detector stays
// conservative in the false-positive direction. Its documented contract is
// to distinguish abandonment from FAITHFUL completion, which was previously
// indistinguishable (every multi-step task's completion shape was in the
// trigger surface).
//
// A nil runStats (no evidence available) is treated as a gap: the declare →
// zero-activity → claim-done shape must still warn.
func planHasExecutionGap(steps []string, rs *RunStats) bool {
	if rs == nil {
		return true
	}
	hasEditStep := false
	hasRunStep := false
	for _, stp := range steps {
		if planStepEditVerbRe.MatchString(stp) {
			hasEditStep = true
		}
		if planStepRunVerbRe.MatchString(stp) {
			hasRunStep = true
		}
	}
	if !hasEditStep && !hasRunStep {
		// Pure read/inspect steps: no evidence channel exists for them.
		return false
	}
	gap := false
	if hasEditStep && len(rs.FilesEdited) == 0 {
		gap = true
	}
	if hasRunStep && len(rs.CommandsRun) == 0 {
		gap = true
	}
	return gap
}

// maybeWarnPlanAbandon checks assistant text for the plan-abandonment pattern:
// plan steps were declared in a prior iteration, and a completion signal
// appears without evidence that all steps were executed.
//
// Returns a guidance message if the pattern is detected, empty string otherwise.
func (a *Agent) maybeWarnPlanAbandon(assistantText string, runStats *RunStats) string {
	if a.planAbandon == nil {
		return ""
	}
	if a.planAbandon.warnings >= planAbandonMaxWarnings {
		return ""
	}

	s := a.planAbandon
	s.curIter++

	// Check for a new plan declaration with future-tense steps.
	newSteps := extractFuturePlanSteps(assistantText)
	if len(newSteps) >= planAbandonMinSteps {
		s.declaredSteps = newSteps
		s.planIter = s.curIter
	}

	// No active plan - nothing to check.
	if len(s.declaredSteps) < planAbandonMinSteps || s.planIter < 0 {
		return ""
	}

	// Plan was declared in the same iteration as the completion signal -
	// the agent may be summarizing what it WILL do then claiming done.
	// Only warn if completion comes in a LATER iteration.
	if s.curIter <= s.planIter {
		return ""
	}

	// Check for completion signal.
	if !hasCompletionSignal(assistantText) {
		return ""
	}

	// Execution-evidence gate (#490): a completion claim after a declared
	// plan is only suspicious when a declared step category (edit / run)
	// shows zero matching execution evidence. A faithful multi-step run
	// that edited files and ran commands must NOT trigger — previously the
	// trigger surface was byte-identical for faithful completion and true
	// abandonment (≈100% false-positive rate on multi-step tasks).
	if !planHasExecutionGap(s.declaredSteps, runStats) {
		return ""
	}

	// Pattern detected: plan declared, then completion claimed later.
	stepCount := len(s.declaredSteps)
	s.warnings++

	var shown []string
	for si, stp := range s.declaredSteps {
		if si >= planAbandonMaxStepsShown {
			break
		}
		shown = append(shown, fmt.Sprintf("  %d. %s", si+1, stp))
	}
	if stepCount > planAbandonMaxStepsShown {
		shown = append(shown, fmt.Sprintf("  ... and %d more", stepCount-planAbandonMaxStepsShown))
	}

	// Reset the active plan so we don't re-warn on the same plan.
	s.declaredSteps = nil
	s.planIter = -1

	hint := "[plan-abandon] You declared " + fmt.Sprintf("%d", stepCount) +
		" plan step(s) in a prior turn and are now " +
		"claiming completion. Verify that ALL declared steps were actually executed " +
		"- agents frequently abandon plan steps (e.g., 'update all callers', " +
		"'run the tests', 'add error handling') while claiming the task is done. " +
		"If any step was skipped, complete it now or explicitly tell the user " +
		"what remains.\n" +
		"Declared steps:\n" + strings.Join(shown, "\n")

	return hint
}
