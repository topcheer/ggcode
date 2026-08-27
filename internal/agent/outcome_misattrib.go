package agent

// Outcome Misattribution Detector
//
// Research basis:
//   - "Why AI Coding Agents Fail" (SWE-bench analysis, 2025-2026): a top-3
//     failure mode is "phantom success" - the agent receives a tool result
//     containing error/failure indicators (build failure, test failure,
//     "not found", empty results) but then claims success or completion
//     in its narrative, proceeding as if the operation succeeded.
//   - CogCal-1 (2025): cognitive calibration gaps where the agent's
//     self-assessment diverges from objective evidence.
//   - Agent-R self-training (arXiv:2503.07657): "outcome delusion" where
//     agents misattribute negative results as positive, compounding into
//     cascading failures.
//
// Problem: After a tool returns content containing failure indicators
// (compile errors, test failures, "no results found", "file not found"),
// the agent sometimes describes the outcome as successful ("done", "fixed",
// "works correctly", "all set") and moves on. This silently ships broken
// work and is invisible to the user without careful inspection.
//
// Existing ggcode detectors that are RELATED but do NOT cover this:
//   - verify_disconnect.go: tracks whether verification was *run* after
//     edits, and whether the agent *advanced past* a verification failure.
//     It does NOT check whether the agent *narrated success* despite the
//     failure - the agent could acknowledge the failure and still be
//     flagged. This detector catches the opposite: the agent NARRATES
//     success despite objective failure in tool output.
//   - verify_scope_narrow.go: detects narrowing of verification scope
//     (e.g., testing fewer cases). Does not compare narrative to results.
//   - unverified_confidence / evidence_overconfidence: track whether
//     verification was done, not whether it contradicted the narrative.
//   - trajectory_confidence_scorer.go: scores overall trajectory quality,
//     does not detect per-result misattribution.
//
// Gap: No detector compares the agent's POST-RESULT NARRATIVE ("done",
// "fixed", "works") against FAILURE INDICATORS in the tool result that
// preceded it. This detector fills that gap.
//
// Design:
//   - Phase 1 (recordResult): called during tool execution. Examines the
//     result content for failure indicators (error patterns, test
//     failures, "not found", empty results). Records the failure for
//     the current iteration.
//   - Phase 2 (checkMisattribution): called after assistantText is
//     captured in the next iteration. Scans the text for success claims
//     ("done", "fixed", "works", "all set", "complete"). If a success
//     claim appears in the iteration immediately following a recorded
//     failure - and no corrective tool call (edit, fix, retry) was made
//     between the failure and the claim - inject guidance to re-verify.
//   - Zero LLM cost - pure deterministic pattern matching.
//   - Fires at most 2 times per run (advisory, non-blocking).

import (
	"regexp"
	"strings"
)

const (
	outcomeMisattribMaxWarnings = 2
)

// Failure indicator patterns in tool results.
var outcomeFailureRe = regexp.MustCompile(
	`(?i)(?:error[s]?\s|fail(?:ed|ure|ures)?\b|panic:|fatal:|cannot |could not |not found\b|no results?\b|no matches?\b|no such file|undefined:|compilation aborted|BUILD FAILURE|exit code [1-9]|0 tests? pass|0 matches|traceback|exception|segfault|core dumped)`,
)

// Success claim patterns in assistant narrative.
var outcomeSuccessClaimRe = regexp.MustCompile(
	`(?i)\b(?:done|fixed|resolved|solved|works?\s+(?:correctly|as\s+expected|now)|all\s+(?:set|good|passing|tests?\s+(?:pass|are\s+passing))|verified\s+(?:that\s+)?(?:it|this|everything)\s+works|successfully\s+(?:completed|implemented|fixed|updated)|everything\s+(?:looks?|is)\s+(?:good|correct|fine)|the\s+(?:fix|change|test|build)\s+(?:works?|passes?|is\s+correct)|no\s+(?:issues?|problems?|errors?))\b`,
)

// Tools whose results carry verifiable pass/fail semantics of the agent's
// own actions (build/test/lint command output, explicit git operation
// results, compiler diagnostics).
// #1139: Content-returning read-class tools (read_file, grep, glob,
// search_files, lsp_hover, lsp_definition, lsp_references) were removed
// from this set. Their output is EXTERNAL CONTENT, not an outcome of the
// agent's own work, so scanning it for bare words like "error" or "fail"
// misfires whenever the agent merely reads ordinary Go source that contains
// error-handling code (e.g. "return fmt.Errorf(...)") and then says 'done'.
var outcomeVerifiableTools = map[string]bool{
	"run_command":         true,
	"start_command":       true,
	"read_command_output": true,
	"git_commit":          true,
	"lsp_diagnostics":     true,
}

// Corrective tools that invalidate a misattribution check (if used
// between the failure and the success claim, the agent is addressing it).
// Aliased to the canonical sourceMutatingTools superset (#738).
var outcomeCorrectiveTools = sourceMutatingTools

// outcomeMisattribState tracks failures and corrective actions per run.
type outcomeMisattribState struct {
	warnings int
	// pendingFailure records the iteration where the last failure was seen.
	pendingFailureIter int
	// pendingFailureType briefly categorizes the failure for the message.
	pendingFailureType string
	// correctiveActionSeen tracks whether a corrective tool was used
	// after the failure was recorded.
	correctiveActionSeen bool
}

func newOutcomeMisattribState() *outcomeMisattribState {
	return &outcomeMisattribState{
		pendingFailureIter: -1, // -1 = no pending failure
	}
}

// containsFailureIndicator checks if a tool result content has failure signals.
func containsFailureIndicator(content string) (bool, string) {
	if len(content) < 5 {
		return false, ""
	}
	loc := outcomeFailureRe.FindString(content)
	if loc == "" {
		return false, ""
	}
	// Classify the failure type for a more useful message.
	lower := strings.ToLower(loc)
	switch {
	case strings.Contains(lower, "test") || strings.Contains(lower, "fail"):
		return true, "test/build failure"
	case strings.Contains(lower, "error") || strings.Contains(lower, "panic") || strings.Contains(lower, "undefined"):
		return true, "error in output"
	case strings.Contains(lower, "not found") || strings.Contains(lower, "no result") || strings.Contains(lower, "no match") || strings.Contains(lower, "no such"):
		return true, "empty/not-found result"
	default:
		return true, "failure indicator"
	}
}

// recordResult examines a tool result for failure indicators.
// Called during tool execution for each tool result.
func (s *outcomeMisattribState) recordResult(toolName string, resultContent string, isError bool, iter int) {
	// If the result is an explicit error result, always record it.
	if isError {
		s.pendingFailureIter = iter
		s.pendingFailureType = "tool error"
		s.correctiveActionSeen = false
		return
	}

	// Only check verifiable tools for embedded failure indicators.
	if !outcomeVerifiableTools[toolName] {
		return
	}

	hasFail, failType := containsFailureIndicator(resultContent)
	if hasFail {
		s.pendingFailureIter = iter
		s.pendingFailureType = failType
		s.correctiveActionSeen = false
	}
}

// recordToolCallForOM tracks corrective actions taken after a failure.
func (s *outcomeMisattribState) recordToolCallForOM(toolName string) {
	if s.pendingFailureIter < 0 {
		return
	}
	if outcomeCorrectiveTools[toolName] {
		s.correctiveActionSeen = true
	}
}

// checkMisattribution scans assistant text for success claims that follow
// a recorded failure without corrective action. Returns a guidance string.
func (s *outcomeMisattribState) checkMisattribution(assistantText string, iter int) string {
	if s.pendingFailureIter < 0 {
		return ""
	}
	if s.warnings >= outcomeMisattribMaxWarnings {
		return ""
	}
	// Only check the iteration immediately following the failure.
	if iter != s.pendingFailureIter+1 {
		// If more than one iteration has passed, the failure is stale.
		if iter > s.pendingFailureIter+1 {
			s.pendingFailureIter = -1
		}
		return ""
	}

	if len(assistantText) < 5 {
		return ""
	}

	// If corrective action was taken, the agent may have legitimately fixed
	// the issue - don't flag unless we can see it didn't help.
	if s.correctiveActionSeen {
		// Still check: if the agent claims success but the fix happened in
		// the same iteration (not yet verified), it's premature. But if the
		// corrective action was in a prior iteration and then verification
		// ran in the current one and passed, that's legitimate.
		// For simplicity, if corrective action was taken, reset and don't warn.
		s.pendingFailureIter = -1
		return ""
	}

	claims := outcomeSuccessClaimRe.FindAllString(assistantText, -1)
	if len(claims) == 0 {
		return ""
	}

	s.warnings++
	s.pendingFailureIter = -1 // consume the pending failure

	// Build a concise excerpt of the first claim.
	claimExcerpt := claims[0]
	if len(claimExcerpt) > 50 {
		claimExcerpt = claimExcerpt[:50]
	}

	msg := "[outcome misattribution] Your text claims success (\"" + claimExcerpt + "\") but the preceding tool result contained " + s.pendingFailureType + ". Re-verify the actual outcome before proceeding - claiming success despite a failure result is a common source of silently broken work."
	return msg
}
