package agent

// Correction Spiral Detector
//
// Research basis: "Self-Correction as Feedback Control: Error Dynamics"
// (arXiv:2604.22273, 2026) and "A Survey for LLM Agent Trajectory Analysis:
// From Failure Attribution to Enhancement" (Wang et al., 2026).
//
// Key finding from the research:
//   Iterative self-correction in agentic LLM systems can create degenerative
//   spirals where each "fix" introduces a WORSE error. The feedback control
//   loop becomes unstable: error → fix → worse error → fix → even worse error.
//   This is distinct from oscillation (back-and-forth) or simple error counts.
//
// Example spiral:
//   1. Build failure (syntax error)         → severity: low
//   2. Fix attempt → compile error          → severity: medium (worse)
//   3. Fix attempt → runtime panic          → severity: high (worse)
//   4. Fix attempt → test suite failure     → severity: high (persisted)
//
// NONE of ggcode's existing detectors track this escalation pattern:
//   - error_compound.go: tracks overall error density (not sequential severity)
//   - convergence_lock.go: detects semantic back-and-forth (not error severity)
//   - edit_oscillation.go: detects file content flip-flopping (not error types)
//   - error_cascade.go: clusters by root cause (not sequential escalation)
//   - recurring_error.go: detects same error repeating (not escalation)
//
// This detector fills the gap by:
//   1. Tracking the sequence of error types after correction attempts
//   2. Classifying error severity (parse < compile < runtime < crash)
//   3. Detecting monotonic or near-monotonic severity escalation
//   4. Warning when the agent's fixes are making things progressively worse
//
// Competitor analysis:
//   - Claude Code: no correction spiral awareness
//   - Cursor: tracks errors but not escalation dynamics
//   - OpenHands: no sequential error severity tracking
//   - Devin: proprietary, no evidence of escalation detection
//
// Design constraints:
//   - Zero LLM cost (deterministic classification)
//   - Fires at most twice per run
//   - Non-blocking: guidance appended to tool result
//   - O(n) space for error sequence (bounded to last 12 entries)
//   - Resets each user turn

import (
	"fmt"
	"strings"
)

// correctionSpiralState tracks error severity across correction iterations.
type correctionSpiralState struct {
	// errorSequence records severity scores for consecutive error-bearing
	// iterations after corrections (edits). Each entry is the severity of
	// the error observed AFTER an edit, in iteration order.
	errorSequence []int

	// totalCorrections counts how many edit→error pairs we've seen.
	totalCorrections int

	// warningCount limits warnings per run.
	warningCount int

	// lastWarnedAt spaces warnings apart.
	lastWarnedAt int

	// lastEditIteration tracks when the last edit happened, so we can
	// associate subsequent errors with correction attempts.
	lastEditIteration int

	// recording tracks whether we're in a correction sub-sequence
	// (i.e., an edit was followed by a verify command).
	pendingEdit bool
}

func newCorrectionSpiralState() *correctionSpiralState {
	return &correctionSpiralState{
		errorSequence: make([]int, 0, 16),
	}
}

func (s *correctionSpiralState) reset() {
	s.errorSequence = s.errorSequence[:0]
	s.totalCorrections = 0
	s.warningCount = 0
	s.lastWarnedAt = 0
	s.lastEditIteration = 0
	s.pendingEdit = false
}

// Error severity levels (higher = worse).
const (
	sevNone    = 0 // no error
	sevLint    = 1 // lint warning / formatting issue
	sevParse   = 2 // syntax/parse error
	sevCompile = 3 // compilation/build error
	sevRuntime = 4 // runtime error (panic, nil deref, etc.)
	sevTest    = 5 // test failure (logic error confirmed)
	sevCrash   = 6 // segfault / unrecoverable crash
)

// recordEdit records that an edit tool was called, starting a potential
// correction sub-sequence.
func (s *correctionSpiralState) recordEdit(iteration int) {
	s.pendingEdit = true
	s.lastEditIteration = iteration
}

// recordVerifyResult records the outcome of a verification command after an edit.
// toolName is the verify tool, isError indicates failure, and output is the
// tool result content used for severity classification.
func (s *correctionSpiralState) recordVerifyResult(_ string, output string, isError bool, _ int) {
	if !s.pendingEdit || !isError {
		if !isError {
			// Successful verification after edit - resets the correction chain.
			// A green build breaks the spiral. #1449-B: the comment always
			// promised this but only pendingEdit was reset - a healthy
			// progression (fix sev1 -> GREEN -> fix sev3 -> GREEN -> fix
			// sev5) accumulated [1,3,5], satisfied the monotonic-length
			// pattern, and fired 'STOP incremental fixes' / 'consider
			// reverting to the last known-good state' on a trajectory where
			// every problem WAS fixed. The error sequence clears too.
			s.pendingEdit = false
			s.errorSequence = s.errorSequence[:0]
		}
		return
	}

	// Classify error severity from output
	severity := csClassifySeverity(output)

	// Only record if this error follows an edit (correction attempt)
	s.errorSequence = append(s.errorSequence, severity)
	s.totalCorrections++

	// Cap sequence length to avoid unbounded growth
	if len(s.errorSequence) > 12 {
		s.errorSequence = s.errorSequence[len(s.errorSequence)-12:]
	}

	s.pendingEdit = false
}

// maybeWarn checks for escalation spiral and returns guidance if detected.
func (s *correctionSpiralState) maybeWarn(iteration int) string {
	// Need at least 3 correction→error pairs to detect a pattern
	if len(s.errorSequence) < 3 {
		return ""
	}

	// Cap warnings
	if s.warningCount >= 2 {
		return ""
	}

	// Space warnings
	if s.warningCount > 0 && iteration-s.lastWarnedAt < 5 {
		return ""
	}

	// Analyze the recent error sequence for escalation
	seq := s.errorSequence

	// Check for monotonic escalation (each error >= previous)
	// Allow at most one non-escalating step (plateau is OK, decrease is not)
	escalations := 0
	plateaus := 0
	regressions := 0
	for i := 1; i < len(seq); i++ {
		switch {
		case seq[i] > seq[i-1]:
			escalations++
		case seq[i] == seq[i-1]:
			plateaus++
		default:
			regressions++
		}
	}

	// Spiral pattern: escalations >= 2 and regressions <= 1
	// This means the majority of correction attempts made things worse
	if escalations < 2 || regressions > 1 {
		return ""
	}

	// Check that the last error is at least as severe as the first
	// (true spiral ends at or above where it started)
	if seq[len(seq)-1] < seq[0] {
		return ""
	}

	s.warningCount++
	s.lastWarnedAt = iteration

	// Build severity trajectory description
	labels := make([]string, len(seq))
	for i, v := range seq {
		labels[i] = csSeverityLabel(v)
	}
	trajectory := strings.Join(labels, " → ")

	var action string
	if s.warningCount == 1 {
		action = "Your corrections are making the problem progressively worse. " +
			"STOP incremental fixes. Step back, read the full error context, " +
			"and address the root cause rather than symptoms."
	} else {
		action = "CRITICAL: correction spiral persists. Your fix attempts keep " +
			"introducing higher-severity errors. Strongly recommend reverting to " +
			"the last known-good state (git checkout / undo) and re-approaching " +
			"the problem from scratch with a different strategy."
	}

	return fmt.Sprintf(
		"[correction-spiral] Error severity escalating across %d correction attempts "+
			"(%d escalations, %d regressions). Severity trajectory: %s. "+
			"Feedback control theory shows that unstable correction loops degrade "+
			"monotonically without intervention. %s",
		len(seq), escalations, regressions, trajectory, action,
	)
}

// csClassifySeverity determines error severity from tool output text.
func csClassifySeverity(output string) int {
	low := strings.ToLower(output)

	// Test failure FIRST (#491 C): test-failure output frequently contains
	// words that overlap crash markers ("--- FAIL: TestSignalHandler"
	// contains "signal") — the specific fail+test/assert/expect shape is
	// the stronger signal and must win over the crash heuristic below.
	if strings.Contains(low, "fail") && (strings.Contains(low, "test") ||
		strings.Contains(low, "assert") || strings.Contains(low, "expect")) {
		return sevTest
	}

	// Crash / segfault (highest). "signal" is matched only in contextual
	// forms (#491 C): a bare substring turned any output merely MENTIONING
	// the word (test names, grep matches) into a crash, feeding bogus
	// sevCrash entries into the escalation sequence.
	if strings.Contains(low, "segfault") ||
		strings.Contains(low, "received signal") || strings.Contains(low, "signal:") ||
		strings.Contains(low, "core dumped") || strings.Contains(low, "fatal error") ||
		strings.Contains(low, "goroutine") && strings.Contains(low, "panic") {
		return sevCrash
	}

	// Runtime error (panic, nil pointer, index out of range)
	if strings.Contains(low, "panic:") || strings.Contains(low, "nil pointer") ||
		strings.Contains(low, "index out of range") || strings.Contains(low, "runtime error") ||
		strings.Contains(low, "deadlock") || strings.Contains(low, "timeout") {
		return sevRuntime
	}

	// Parse / syntax error (check before compile - "syntax error" can match both)
	if strings.Contains(low, "syntax error") || strings.Contains(low, "parse error") ||
		strings.Contains(low, "unexpected token") || strings.Contains(low, "expected") {
		return sevParse
	}

	// Compilation error
	if strings.Contains(low, "compile error") || strings.Contains(low, "cannot find symbol") ||
		strings.Contains(low, "undefined:") || strings.Contains(low, "type error") ||
		strings.Contains(low, "build failed") {
		return sevCompile
	}

	// Lint / formatting
	if strings.Contains(low, "lint") || strings.Contains(low, "warning:") ||
		strings.Contains(low, "deprecated") || strings.Contains(low, "format") {
		return sevLint
	}

	// Default: treat as compile-level if it's a build failure we can't classify
	return sevCompile
}

// csSeverityLabel converts a severity level to a human-readable label.
func csSeverityLabel(level int) string {
	switch level {
	case sevCrash:
		return "crash"
	case sevTest:
		return "test-fail"
	case sevRuntime:
		return "runtime-err"
	case sevCompile:
		return "compile-err"
	case sevParse:
		return "syntax-err"
	case sevLint:
		return "lint"
	default:
		return "unknown"
	}
}

// csIsEditTool checks if a tool modifies files (same set as other detectors).
// Derived from the canonical sourceMutatingTools superset (#738).
func csIsEditTool(toolName string) bool {
	return sourceMutatingTools[toolName]
}

// csIsVerifyTool was removed (#491): tool-NAME-level verify classification
// was the exact #350/#483/#485/#488 bug family — run_command carries
// observational (cat/ls/git diff) and verify (go test) commands alike.
// The production wiring now gates on command CONTENT via
// psIsVerifyCommand(sfCommandArg(psArgs)), and start_command is excluded
// entirely: its result reflects job startup, not the verification outcome.
