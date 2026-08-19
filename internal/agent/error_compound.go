package agent

// Error Compounding Risk Detector
//
// Research basis: "Self-Verifying AI: Why 2026 Is the Year AI Checks Its Own Work"
// (Heimdall Engineering, 2026) and "AI Agent Systems: Architectures, Applications,
// and Evaluation" (arXiv 2601.01743, 2026) formalize the Error Accumulation Problem:
// errors across MANY steps of an agent trajectory compound, so the trajectory-level
// reliability is far worse than any single step's success rate suggests.
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
// This detector fills that gap.
//
// MATH MODEL (sliding-window density) — issue #336 fix:
//
// The previous model computed P(success) = (1 - density)^totalSteps where
// density = errorSteps/totalSteps. That formula is mathematically invalid:
// density is ALREADY an aggregated ratio over the run; raising (1-density)
// to the power of totalSteps re-multiplies the run length into an aggregate,
// double-counting it. Consequence: any run with >=8 steps and >=1 error
// produced P < 70% and fired a "moderate" warning (e.g. 8 steps with 1 error:
// density=12.5%, P = 0.875^8 = 34% — a healthy transient error flagged as risk).
//
// The correct question is not "what is the probability the whole run is clean"
// but "is the agent erroring at a high rate RIGHT NOW". We therefore use a
// plain empirical error density over a SLIDING WINDOW of the most recent
// ecWindow steps:
//
//	windowDensity = windowErrors / windowSteps
//
//   - moderate: windowDensity > 0.30 (with >=8 window steps)
//   - critical: moderate condition AND new errors occurred since the last
//     warning (a pure recovery period — zero new errors — must never escalate)
//   - short-circuit: with only >=3 total steps, windowDensity >= 0.75 still
//     fires, so catastrophic starts (3/3 errors) are caught despite the small n
//
// Because the window slides, old errors naturally age out: after 12 clean
// steps the density drops to 0 and warnings subside without any special
// decay logic. This directly fixes the second bug in #336, where the
// whole-run counter kept density high during recovery and escalated severity
// ("Strongly recommend stopping incremental edits") while the agent was
// actually succeeding.
//
// Design constraints:
//   - Zero LLM cost (deterministic tracking + formula)
//   - Fires at most twice per run (moderate, then critical)
//   - Non-blocking: guidance injected, execution proceeds normally
//   - O(window) space: a fixed-size ring, not unbounded per-step history
//   - Distinguishes error TYPES to avoid double-counting the same failure

import (
	"fmt"
	"strings"
	"sync"
)

// ecWindow is the number of most recent steps used for the density estimate.
const ecWindow = 12

// ecWarnDensity is the window error density above which we warn (moderate).
const ecWarnDensity = 0.30

// ecCatastrophicDensity, with ecCatastrophicMinSteps, short-circuits the
// minimum-sample gate so that a near-total failure rate still fires early.
const (
	ecCatastrophicDensity  = 0.75
	ecCatastrophicMinSteps = 3
)

// ecStep is one recorded tool-call iteration in the sliding window.
type ecStep struct {
	hasError    bool
	verifyFails int
	editFails   int
	toolErrors  int
}

// errorCompoundState tracks error accumulation across the agent trajectory
// using a sliding window of recent steps.
type errorCompoundState struct {
	mu sync.Mutex

	window       []ecStep // ring of the most recent <= ecWindow steps
	totalSteps   int      // total tool-call iterations recorded (lifetime)
	verifyFails  int      // lifetime explicit verify command failures
	editFails    int      // lifetime edit tool failures
	toolErrors   int      // lifetime other tool execution errors
	warningCount int      // how many warnings have been emitted this run
	lastWarnedAt int      // iteration of last warning (to space them out)

	// pendingVerify/pendingEdit/pendingTool hold error classifications from
	// recordResult that will be attached to the next recordStep call.
	pendingVerify int
	pendingEdit   int
	pendingTool   int

	// newErrSinceWarn counts error steps recorded after the last warning.
	// Critical escalation requires this to be > 0: a recovery period with
	// zero new errors must never escalate severity (#336).
	newErrSinceWarn int

	// #681 revert snapshot: maybeWarn consumes the per-run quota ("at most
	// 2 per run") when it RETURNS a message, but since #677 the message
	// then goes through the per-turn guidance budget, which may suppress
	// it. "Returned != delivered": a suppressed fire must not burn the
	// quota (that is how the detector could go permanently dark with zero
	// guidance ever delivered). markUndelivered restores this snapshot.
	canRevertWarn    bool
	prevLastWarnedAt int
	prevNewErrSince  int
}

func newErrorCompoundState() *errorCompoundState {
	return &errorCompoundState{}
}

func (s *errorCompoundState) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.window = nil
	s.totalSteps = 0
	s.verifyFails = 0
	s.editFails = 0
	s.toolErrors = 0
	s.warningCount = 0
	s.lastWarnedAt = 0
	s.pendingVerify = 0
	s.pendingEdit = 0
	s.pendingTool = 0
	s.newErrSinceWarn = 0
}

// recordStep records one tool-call iteration. multipleErrors indicates the
// same iteration had more than one error (counts as one error-step).
func (s *errorCompoundState) recordStep(hasError bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	step := ecStep{
		hasError:    hasError,
		verifyFails: s.pendingVerify,
		editFails:   s.pendingEdit,
		toolErrors:  s.pendingTool,
	}
	s.pendingVerify, s.pendingEdit, s.pendingTool = 0, 0, 0

	s.totalSteps++
	if hasError {
		s.newErrSinceWarn++
	}
	s.window = append(s.window, step)
	if len(s.window) > ecWindow {
		s.window = s.window[len(s.window)-ecWindow:]
	}
}

// recordResult classifies a tool result and stages error signals for the
// next recordStep. Returns true if this result contributed an error signal.
func (s *errorCompoundState) recordResult(toolName string, isError bool, iteration int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	hadError := false

	if isError {
		hadError = true
		if ecIsVerifyCmd(toolName) {
			s.verifyFails++
			s.pendingVerify++
		} else if ecIsEditTool(toolName) {
			s.editFails++
			s.pendingEdit++
		} else {
			s.toolErrors++
			s.pendingTool++
		}
	}

	return hadError
}

// windowStats summarizes the sliding window.
func (s *errorCompoundState) windowStats() (steps, errs, verifyFails, editFails, toolErrors int) {
	for _, st := range s.window {
		steps++
		if st.hasError {
			errs++
		}
		verifyFails += st.verifyFails
		editFails += st.editFails
		toolErrors += st.toolErrors
	}
	return
}

// maybeWarn computes the sliding-window error density and returns guidance
// if thresholds are crossed. See the module comment for the math rationale.
func (s *errorCompoundState) maybeWarn(iteration int) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Short-circuit for catastrophic starts: 3+ steps with >=75% error
	// density is alarming regardless of the small sample size.
	// Otherwise require a reasonably filled window.
	catastrophic := s.totalSteps >= ecCatastrophicMinSteps &&
		float64(s.errorStepsInWindowLocked()) >= ecCatastrophicDensity*float64(len(s.window))
	if !catastrophic && s.totalSteps < 8 {
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

	wSteps, wErrs, wVerify, wEdit, wTool := s.windowStats()

	density := 0.0
	if wSteps > 0 {
		density = float64(wErrs) / float64(wSteps)
	}

	// No meaningful error rate in the window -- no risk. Errors that have
	// slid out of the window no longer trigger warnings (recovery subside).
	if density <= ecWarnDensity {
		return ""
	}

	// Escalation guard (#336): the second (critical) warning requires NEW
	// errors since the first warning. A pure recovery period (window has
	// only stale errors, zero new ones) must not escalate severity.
	if s.warningCount > 0 && s.newErrSinceWarn == 0 {
		return ""
	}

	s.canRevertWarn = true
	s.prevLastWarnedAt = s.lastWarnedAt
	s.prevNewErrSince = s.newErrSinceWarn
	s.warningCount++
	s.lastWarnedAt = iteration
	s.newErrSinceWarn = 0

	return s.formatWarning(wSteps, wErrs, density, wVerify, wEdit, wTool)
}

// markUndelivered reverts the quota consumption of the most recent
// maybeWarn fire whose guidance was suppressed by the per-turn guidance
// budget (#681). The detector's own thresholds are unchanged; only the
// per-run warning quota, the spacing marker, and the escalation counter
// are rolled back so the warning can be re-fired on a later, less
// saturated iteration. No-op if there is no revertible fire.
func (s *errorCompoundState) markUndelivered() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.canRevertWarn || s.warningCount == 0 {
		return
	}
	s.canRevertWarn = false
	s.warningCount--
	s.lastWarnedAt = s.prevLastWarnedAt
	s.newErrSinceWarn = s.prevNewErrSince
}

// formatWarning builds the guidance message for a firing warning. Severity
// escalates from moderate (first warning) to critical (second).
func (s *errorCompoundState) formatWarning(wSteps, wErrs int, density float64, wVerify, wEdit, wTool int) string {
	var severity, action string
	if s.warningCount == 1 {
		severity = "moderate"
		action = "Consider pausing to run a holistic verification pass (build + test + lint) before continuing."
	} else {
		severity = "critical"
		action = "Strongly recommend stopping incremental edits and running full verification. The trajectory may be building on compounding errors."
	}

	return fmt.Sprintf(`[error-compounding] %s risk detected: %d errors in last %d steps `+
		`(window density=%.0f%%). Error breakdown (window): %s. `+
		`Per-step errors compound -- a sustained high error rate erodes overall `+
		`trajectory reliability. %s`,
		severity, wErrs, wSteps, density*100,
		ecWindowBreakdown(wVerify, wEdit, wTool), action,
	)
}

// ecWindowBreakdown renders the window-scoped error-type breakdown. Reports
// WINDOW counts (not lifetime totals) so the report reflects recent reality
// rather than ancient history (#336).
func ecWindowBreakdown(wVerify, wEdit, wTool int) string {
	var parts []string
	if wVerify > 0 {
		parts = append(parts, fmt.Sprintf("%d verify failures", wVerify))
	}
	if wEdit > 0 {
		parts = append(parts, fmt.Sprintf("%d edit failures", wEdit))
	}
	if wTool > 0 {
		parts = append(parts, fmt.Sprintf("%d tool errors", wTool))
	}
	if s := strings.Join(parts, ", "); s != "" {
		return s
	}
	return "none classified (raw step errors only)"
}

// errorStepsInWindowLocked returns the number of error steps in the window.
// Caller must hold s.mu.
func (s *errorCompoundState) errorStepsInWindowLocked() int {
	n := 0
	for _, st := range s.window {
		if st.hasError {
			n++
		}
	}
	return n
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
// Derived from the canonical sourceMutatingTools superset (#738).
func ecIsEditTool(toolName string) bool {
	return sourceMutatingTools[toolName]
}
