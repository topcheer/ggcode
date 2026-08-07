package agent

// Verification Outcome Disconnect Detector
//
// Research basis:
//   - Tian et al., "Overconfidence in LLM-as-a-Judge" (arXiv:2508.06225, 2025):
//     Models systematically overstate correctness - predicted confidence
//     significantly overstates actual correctness. In coding agents this manifests
//     as proceeding after verification failure without acknowledging the gap.
//   - "A Self-Improving Coding Agent" (arXiv:2504.15228, 2025): agents that
//     fail to close the loop on verification results have degraded trajectory
//     quality - they "advance past failures" instead of resolving them.
//   - Zhu et al., "Scaling Test-time Compute for LLM Agents" (arXiv:2506.12928):
//     "Knowing when to reflect is important for agents" - but agents frequently
//     skip reflection precisely when verification surfaces a failure.
//
// Problem: After running a build/test/lint command that FAILS, the agent
// sometimes:
//   1. Ignores the failure and proceeds to claim success ("The fix is complete")
//   2. Moves on to an unrelated task without addressing the failure
//   3. Makes edits to different files than what the failure indicated
//   4. Claims the failure is "expected" or "pre-existing" without evidence
//
// This is the BEHAVIORAL manifestation of the overconfidence phenomenon: the
// agent's actions (advancing) dont match the evidence (failure). Unlike
// unverified_confidence.go which checks confidence-without-verification, this
// detector checks verification-FAILURE-without-acknowledgment.
//
// Design:
//   - Monitors tool results from verification tools for failure indicators
//   - Tracks whether subsequent tool calls address the failure
//   - Fires when the agent advances to unrelated work or claims success
//     after receiving a verification failure it hasn't addressed
//   - Zero LLM cost — pure deterministic pattern matching
//   - Fires at most 2 times per run (advisory, non-blocking)
//   - Cleared when agent re-runs the failing verification or edits files
//     mentioned in the error output

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	verifyDisconnectMaxWarnings   = 2
	verifyDisconnectMaxIterations = 5 // max iterations to wait for resolution
	verifyDisconnectMaxErrors     = 5 // max error snippets to track
)

// verifyFailureRe matches common failure indicators in tool output.
var verifyFailureRe = regexp.MustCompile(`(?i)(FAIL|FAILED|failure|error|panic|cannot|undefined|syntax error|compilation failed|BUILD FAILED|test failed|0 tests? pass|0%.*pass)`)

// verifySuccessRe matches common success indicators that override failures.
var verifySuccessRe = regexp.MustCompile(`(?i)(PASS|ok\b|BUILD SUCCESS|all tests pass|0 failing|0 failed|coverage:.*100%|no errors|compilation successful|ran successfully)`)

// verifyDisconnectClaimRe matches success/claim language in assistant text.
var verifyDisconnectClaimRe = regexp.MustCompile(`(?i)\b(the fix|changes?|solution|implementation).{0,20}(work|correct|complete|done|ready|resolved|success)|\b(tests?|build|lint).{0,15}(pass|succeed|ok)|\beverything.{0,10}(work|pass|fine)|\bno (more |longer )?(issues?|errors?|problems?|failures?)\b`)

// verifyFailureInfo captures a detected verification failure.
type verifyFailureInfo struct {
	toolName  string // tool that produced the failure
	toolInput string // truncated input (e.g., command text)
	iteration int    // iteration when detected
	snippet   string // short failure snippet
	addressed bool   // whether subsequent actions addressed it
}

// verifyDisconnectState tracks verification failures and whether they were resolved.
type verifyDisconnectState struct {
	warnings       int
	pendingFailure *verifyFailureInfo // current unresolved failure
}

func newVerifyDisconnectState() *verifyDisconnectState {
	return &verifyDisconnectState{}
}

func (s *verifyDisconnectState) reset() {
	s.warnings = 0
	s.pendingFailure = nil
}

// isVerificationResult checks whether a tool result looks like build/test/lint output.
func isVerificationResult(toolName, result string) bool {
	if len(result) < 10 || len(result) > 50000 {
		return false
	}
	switch toolName {
	case "run_command", "start_command":
		return true
	case "lsp_diagnostics":
		return true
	default:
		return false
	}
}

// detectVerificationFailure returns a failure snippet if the tool output indicates
// a verification failure, or "" if it indicates success or is ambiguous.
func detectVerificationFailure(toolName, result string) string {
	if !isVerificationResult(toolName, result) {
		return ""
	}

	hasSuccess := verifySuccessRe.MatchString(result)
	hasFailure := verifyFailureRe.MatchString(result)

	// Success overrides failure in combined output (e.g., "1 test failed, 9 passed" has both)
	if hasSuccess && !hasFailure {
		return ""
	}
	if !hasFailure {
		return ""
	}

	// Check for exit code indicators
	if regexp.MustCompile(`(?i)(exit code|exit status|returned)\s*[1-9]`).MatchString(result) {
		// definitely a failure
	}

	// Extract a short snippet around the first failure indicator
	loc := verifyFailureRe.FindStringIndex(result)
	if loc == nil {
		return ""
	}

	start := loc[0] - 80
	if start < 0 {
		start = 0
	}
	end := loc[1] + 80
	if end > len(result) {
		end = len(result)
	}
	snippet := strings.TrimSpace(result[start:end])
	if len(snippet) > 200 {
		snippet = snippet[:197] + "..."
	}
	return snippet
}

// recordVerificationResult processes a tool result, tracking failures.
func (s *verifyDisconnectState) recordVerificationResult(toolName, toolInput, result string, iteration int) {
	snippet := detectVerificationFailure(toolName, result)
	if snippet != "" {
		s.pendingFailure = &verifyFailureInfo{
			toolName:  toolName,
			toolInput: truncateVD(toolInput, 80),
			iteration: iteration,
			snippet:   snippet,
		}
		return
	}

	// If we had a pending failure and this is a verification result that doesn't
	// show failure, the issue may have been resolved
	if s.pendingFailure != nil && isVerificationResult(toolName, result) {
		// Clear only if this looks like the same kind of verification
		s.pendingFailure = nil
	}
}

// recordToolCallForVD tracks tool calls to see if the agent is addressing a failure.
func (s *verifyDisconnectState) recordToolCallForVD(toolName string) {
	if s.pendingFailure == nil {
		return
	}

	// Code edits to files suggest the agent is addressing the failure
	if isCodeEditTool(toolName) {
		s.pendingFailure.addressed = true
		return
	}

	// Re-running verification suggests addressing
	if isVerificationTool(toolName) {
		s.pendingFailure.addressed = true
		return
	}
}

func isVerificationTool(toolName string) bool {
	return verificationToolRe.MatchString(toolName)
}

// maybeWarnVerifyDisconnect checks whether the agent has an unresolved verification
// failure and is either claiming success or has moved on to unrelated work.
func (a *Agent) maybeWarnVerifyDisconnect(assistantText string, iteration int) string {
	if a.verifyDisconnect == nil {
		return ""
	}
	s := a.verifyDisconnect

	if s.warnings >= verifyDisconnectMaxWarnings {
		return ""
	}

	if s.pendingFailure == nil {
		return ""
	}

	pf := s.pendingFailure

	// Has the agent been sitting on this failure for too many iterations?
	stale := iteration-pf.iteration >= verifyDisconnectMaxIterations

	// Is the agent claiming success despite the failure?
	claimsSuccess := verifyDisconnectClaimRe.MatchString(assistantText)

	// Has the agent addressed the failure?
	addressed := pf.addressed

	if addressed {
		// Agent addressed it — clear and don't warn
		s.pendingFailure = nil
		return ""
	}

	if claimsSuccess {
		s.warnings++
		s.pendingFailure = nil // clear after warning
		return fmt.Sprintf(`[Verification Gap] A verification command failed but you are now claiming success:

  Failed: %s (%s)
  Error: %s

You advanced to a success claim without evidence that the failure was resolved. This is the behavioral overconfidence gap (arXiv:2508.06225): agents proceed past failures without closing the loop. Re-run the failing command and confirm it passes before claiming the fix is complete.`,
			pf.toolName, pf.toolInput, pf.snippet)
	}

	if stale {
		s.warnings++
		s.pendingFailure = nil
		return fmt.Sprintf(`[Verification Gap] A verification command failed %d iterations ago but hasn't been re-run or addressed:

  Failed: %s (%s)
  Error: %s

The failure has gone unresolved while work continued. Either fix the underlying issue and re-run, or explicitly acknowledge why this failure is acceptable (e.g., pre-existing, unrelated).`,
			iteration-pf.iteration, pf.toolName, pf.toolInput, pf.snippet)
	}

	return ""
}

// truncateVD truncates a string for display, prefixed to avoid collision.
func truncateVD(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) > max {
		return s[:max-3] + "..."
	}
	if s == "" {
		return "(no input)"
	}
	return s
}
