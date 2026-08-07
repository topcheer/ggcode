package agent

// Error Compounding Risk Detector
//
// Research basis: "Self-Verifying AI: Why 2026 Is the Year AI Checks Its Own Work"
// (Heimdall Engineering, 2026) and "AI Agent Systems: Architectures, Applications,
// and Evaluation" (arXiv 2601.01743, 2026) formalize the Error Accumulation Problem:
//
//   In a 10-step workflow, even a 95% accuracy rate per step sounds good in
//   isolation. But 0.95^10 = 0.60. That means 40% of the time, your 10-step
//   AI process arrives at a wrong or suboptimal result -- with no mechanism
//   to know it happened.
//
// This is why most enterprise AI pilots succeed in demos and fail in production.
// The demo shows a clean path; production shows the messy reality of compounding
// errors across steps.
//
// EXISTING GGCODE DETECTORS that address INDIVIDUAL error patterns but NOT the
// systemic compounding risk:
//
//   - fix_cascade.go: tracks edit→verify→fail CYCLES (specific pattern)
//   - recurring_error.go: detects the SAME error repeating (exact match)
//   - error_regression.go: detects NEW errors after edits (diff-based)
//   - convergence_lock.go: detects post-verify unnecessary edits
//   - verification_debt.go: tracks unverified edit accumulation
//
// NONE of these compute the SYSTEMIC risk that accumulated small failures
// (across DIFFERENT error types) have made the overall trajectory unreliable.
// An agent can have 3 tool errors, 2 edit failures, 1 build failure -- each
// caught by a different detector -- without any single detector recognizing
// that the COMBINED error density means the trajectory is now high-risk.
//
// This detector fills that gap by:
//   1. Tracking ALL error signals across the run (any IsError result, any
//      edit failure, any failed verify command)
//   2. Computing error density = errorSteps / totalSteps
//   3. Estimating compounding success probability: P(success) ≈ (1-density)^steps
//   4. Warning when P drops below threshold, recommending holistic verification
//
// Competitor analysis:
//   - Claude Code: no systemic error compounding awareness
//   - Cursor: no trajectory-level error accumulation tracking
//   - OpenHands: tracks per-action success but not compounding probability
//   - Devin: has "confidence scoring" but proprietary, not open
//   - Aider: no cross-step error aggregation
//
// Design constraints:
//   - Zero LLM cost (deterministic tracking + formula)
//   - Fires at most twice per run (at moderate risk, then at critical risk)
//   - Non-blocking: guidance injected, execution proceeds normally
//   - O(1) space: tracks counts only, not per-step details
//   - Distinguishes error TYPES to avoid double-counting the same failure

import (
	"fmt"
	"strings"
	"sync"
)

// errorCompoundState tracks error accumulation across the agent trajectory.
type errorCompoundState struct {
	mu sync.Mutex

	totalSteps   int // total tool-call iterations recorded
	errorSteps   int // iterations with at least one error signal
	verifyFails  int // explicit verify command failures (build/test/lint)
	editFails    int // edit_file/multi_edit_file/write_file failures
	toolErrors   int // other tool execution errors (IsError=true)
	warningCount int // how many warnings have been emitted this run
	lastWarnedAt int // iteration of last warning (to space them out)
}

func newErrorCompoundState() *errorCompoundState {
	return &errorCompoundState{}
}

func (s *errorCompoundState) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.totalSteps = 0
	s.errorSteps = 0
	s.verifyFails = 0
	s.editFails = 0
	s.toolErrors = 0
	s.warningCount = 0
	s.lastWarnedAt = 0
}

// recordStep records one tool-call iteration. multipleErrors indicates the
// same iteration had more than one error (counts as one error-step).
func (s *errorCompoundState) recordStep(hasError bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.totalSteps++
	if hasError {
		s.errorSteps++
	}
}

// recordResult classifies a tool result and records error signals.
// Returns true if this step contributed an error signal.
func (s *errorCompoundState) recordResult(toolName string, isError bool, iteration int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	hadError := false

	if isError {
		hadError = true
		if ecIsVerifyCmd(toolName) {
			s.verifyFails++
		} else if ecIsEditTool(toolName) {
			s.editFails++
		} else {
			s.toolErrors++
		}
	}

	return hadError
}

// maybeWarn computes the compounding risk and returns guidance if thresholds
// are crossed. Uses the geometric compounding model from the research:
//
//	P(trajectory success) ≈ (1 - errorDensity)^totalSteps
//
// where errorDensity = errorSteps / totalSteps. This is conservative:
// it assumes each error-step independently degrades reliability.
func (s *errorCompoundState) maybeWarn(iteration int) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Need enough data to be meaningful
	if s.totalSteps < 8 {
		return ""
	}

	// Already warned twice -- cap at 2 warnings per run
	if s.warningCount >= 2 {
		return ""
	}

	// Space warnings: don't fire again too quickly
	if s.warningCount > 0 && iteration-s.lastWarnedAt < 5 {
		return ""
	}

	errorDensity := float64(s.errorSteps) / float64(s.totalSteps)

	// No errors at all -- no risk
	if errorDensity < 0.01 {
		return ""
	}

	// Geometric compounding probability
	// Using (1 - density) as per-step success probability
	perStepSuccess := 1.0 - errorDensity
	successProb := 1.0
	for i := 0; i < s.totalSteps; i++ {
		successProb *= perStepSuccess
	}

	// Convert to percentage
	successPct := successProb * 100

	// Thresholds:
	//   First warning: P < 70% (moderate risk) -- nudge holistic verification
	//   Second warning: P < 40% (critical risk) -- strongly recommend checkpoint
	var threshold float64
	if s.warningCount == 0 {
		threshold = 70.0
	} else {
		threshold = 40.0
	}

	if successPct >= threshold {
		return ""
	}

	s.warningCount++
	s.lastWarnedAt = iteration

	// Build guidance message
	var severity string
	var action string
	if s.warningCount == 1 {
		severity = "moderate"
		action = "Consider pausing to run a holistic verification pass (build + test + lint) before continuing."
	} else {
		severity = "critical"
		action = "Strongly recommend stopping incremental edits and running full verification. The trajectory may be building on compounding errors."
	}

	// Build error breakdown
	var breakdown []string
	if s.verifyFails > 0 {
		breakdown = append(breakdown, fmt.Sprintf("%d verify failures", s.verifyFails))
	}
	if s.editFails > 0 {
		breakdown = append(breakdown, fmt.Sprintf("%d edit failures", s.editFails))
	}
	if s.toolErrors > 0 {
		breakdown = append(breakdown, fmt.Sprintf("%d tool errors", s.toolErrors))
	}
	breakdownStr := strings.Join(breakdown, ", ")

	return fmt.Sprintf(
		"[error-compounding] %s risk detected: accumulated errors across %d steps "+
			"(density=%.0f%%, est. trajectory success ~%.0f%%). Error breakdown: %s. "+
			"Per-step errors compound geometrically -- even small per-step failure rates "+
			"erode overall reliability over many steps. %s",
		severity, s.totalSteps, errorDensity*100, successPct,
		breakdownStr, action,
	)
}

// ecIsVerifyCmd checks if a tool is a verification command (build/test/lint).
func ecIsVerifyCmd(toolName string) bool {
	switch toolName {
	case "run_command", "start_command":
		return true // these often run builds/tests
	default:
		return false
	}
}

// ecIsEditTool checks if a tool modifies files.
func ecIsEditTool(toolName string) bool {
	switch toolName {
	case "edit_file", "multi_edit_file", "write_file", "multi_file_write",
		"multi_file_edit", "file_ops", "notebook_edit":
		return true
	default:
		return false
	}
}
