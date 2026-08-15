package agent

// Drift Recurrence Detector - Insufficient Course Correction After Warning
//
// Research basis:
//   - "Agent drift: why long-running AI agents lose the plot" (usewire.io, 2026):
//     Behavioral baseline tracking reveals that "drift becomes visible in
//     patterns before it becomes visible in individual outputs." Agents that
//     receive a drift warning but only make a token acknowledgment -- then
//     resume the same drifted behavior -- exhibit "drift recurrence."
//   - "Maintaining and Preventing Drift in Agentic AI" (taazaa.com, 2025-2026):
//     Emphasizes that adaptive replanning requires DETECTING that a previous
//     correction was ineffective. Without recurrence detection, the agent
//     enters a "drift-warning-superficial-fix-drift" cycle.
//   - AgentDebug (arXiv:2509.25370): identifies "performative self-correction"
//     where agents appear to correct but don't change underlying strategy.
//   - KAPRO (arXiv:2606.20661, 2026): the "Knowing-Acting" quadrant shows
//     that self-awareness capability degradation manifests as agents that
//     acknowledge feedback but fail to ACT on it meaningfully.
//
// Problem: ggcode has multiple drift/warning detectors (scope_drift, plan_drift,
// tunnel_vision, analysis_paralysis, etc.). Each fires at most once or twice
// and injects guidance. But NO detector tracks whether the agent actually
// CHANGED BEHAVIOR after the warning. The critical failure pattern:
//
//   iter N:   scope_drift fires → "you're touching too many directories"
//   iter N+1: agent says "you're right, let me focus" → makes 1 focused edit
//   iter N+2: agent immediately goes back to editing 5+ new directories
//   iter N+3: same broad pattern continues
//
// The agent performed "performative compliance" -- a token acknowledgment
// followed by behavioral recurrence. The existing detectors fired once and
// moved on, unaware that their guidance was ignored.
//
// Existing ggcode systems that are RELATED but do NOT cover this:
//   - scope_drift.go: fires once when file diversity exceeds threshold.
//     Doesn't check if the agent continued the pattern after the warning.
//   - user_sentiment.go: tracks CONSECUTIVE negative user messages, but this
//     is about agent behavior recurrence, not user sentiment.
//   - convergence_lock.go: detects post-verification unnecessary edits, not
//     recurrence of a warned pattern.
//   - repetition_tracker.go: tracks repeated TOOL CALLS (same tool+file+error),
//     not recurrence of a behavioral PATTERN after feedback.
//
// Gap: No detector monitors whether the agent's behavioral trajectory ACTUALLY
// improved after a warning, or whether it merely paused before resuming the
// same pattern. This detector fills that gap with zero LLM cost.
//
// Design:
//   - Tracks unique directories touched by edits AFTER any drift warning fires
//   - If the agent continues to expand into new directories at the same rate
//     post-warning, it signals that the guidance was ignored
//   - Also tracks post-warning edit-to-verification ratio: if the agent was
//     warned about insufficient verification but continues editing without
//     verifying, that's recurrence
//   - Fires at most once per run (advisory, non-blocking)
//   - Zero LLM cost -- pure counter-based heuristics

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	// driftRecurrenceNewDirThreshold: after a drift warning, if the agent
	// touches this many NEW directories (not seen before the warning),
	// it signals that the warning was not heeded.
	driftRecurrenceNewDirThreshold = 4

	// driftRecurrenceMinEditsPostWarn: minimum edits after warning before
	// evaluating recurrence. Prevents false positives from a single edit.
	driftRecurrenceMinEditsPostWarn = 3

	// driftRecurrencePostWarnWindow: how many iterations after a warning
	// to monitor for recurrence.
	driftRecurrencePostWarnWindow = 8
)

// driftRecurrenceState tracks whether the agent's behavior improved after
// receiving a drift-related warning.
type driftRecurrenceState struct {
	// preWarnDirs is the set of unique directory signatures touched BEFORE
	// any drift warning fired. Used to identify NEW directories post-warning.
	preWarnDirs map[string]bool

	// postWarnDirs is the set of unique directory signatures touched AFTER
	// a drift warning fired.
	postWarnDirs map[string]bool

	// warned tracks whether any drift-related warning has fired this run.
	warned bool

	// warnIteration records the iteration when the warning fired (1-based).
	warnIteration int

	// postWarnEdits counts edits made after the warning.
	postWarnEdits int

	// postWarnVerifies counts verification commands after the warning.
	postWarnVerifies int

	// fired tracks whether the recurrence warning has already fired.
	fired bool

	// currentIteration tracks the current loop iteration.
	currentIteration int
}

func newDriftRecurrenceState() *driftRecurrenceState {
	return &driftRecurrenceState{
		preWarnDirs:  make(map[string]bool),
		postWarnDirs: make(map[string]bool),
	}
}

func (d *driftRecurrenceState) reset() {
	d.preWarnDirs = make(map[string]bool)
	d.postWarnDirs = make(map[string]bool)
	d.warned = false
	d.warnIteration = 0
	d.postWarnEdits = 0
	d.postWarnVerifies = 0
	d.fired = false
	d.currentIteration = 0
}

// recordIteration updates the current iteration counter.
func (d *driftRecurrenceState) recordIteration(iteration int) {
	d.currentIteration = iteration
}

// recordEdit tracks a file modification relative to any drift warning.
func (d *driftRecurrenceState) recordEdit(filePath string) {
	if filePath == "" {
		return
	}
	fp := filepath.Clean(filePath)
	dir := filepath.Dir(fp)
	sig := dirSignature(dir)
	if sig == "" {
		return
	}

	if d.warned {
		d.postWarnDirs[sig] = true
		d.postWarnEdits++
	} else {
		d.preWarnDirs[sig] = true
	}
}

// recordVerification tracks a verification action (build/test/lint/diagnostics).
func (d *driftRecurrenceState) recordVerification() {
	if d.warned {
		d.postWarnVerifies++
	}
}

// markWarning records that a drift-related warning fired at the given iteration.
// Called by scope_drift, plan_drift, or similar detectors when they inject guidance.
func (d *driftRecurrenceState) markWarning(iteration int) {
	if !d.warned {
		d.warned = true
		d.warnIteration = iteration
	}
}

// check evaluates whether the agent has recurred into the warned pattern.
// Returns a guidance message if recurrence is detected, empty otherwise.
func (d *driftRecurrenceState) check() string {
	if d.fired || !d.warned {
		return ""
	}

	// Only check within the post-warning window.
	if d.currentIteration-d.warnIteration > driftRecurrencePostWarnWindow {
		return ""
	}

	// Need minimum edits post-warning to evaluate.
	if d.postWarnEdits < driftRecurrenceMinEditsPostWarn {
		return ""
	}

	// Count directories that are NEW (not in pre-warn set).
	newDirs := 0
	for pd := range d.postWarnDirs {
		if !d.preWarnDirs[pd] {
			newDirs++
		}
	}

	// Also check verification ratio: if the agent made many edits but
	// zero verifications post-warning, that signals continued drift.
	verifyRatio := 0.0
	if d.postWarnEdits > 0 {
		verifyRatio = float64(d.postWarnVerifies) / float64(d.postWarnEdits)
	}

	// #473: the verify-ratio branch only fires when there IS directory
	// expansion — a compliant agent (0 new dirs after the scope warning,
	// mid-refactor before the build step) must not be accused of
	// "performative compliance". newDirs==0 is compliance, not recurrence.
	if newDirs < driftRecurrenceNewDirThreshold {
		if newDirs == 0 {
			return ""
		}
		// Under threshold but nonzero new dirs: require zero verifies
		// across the minimum edit count to still call it drift.
		if !(d.postWarnVerifies == 0) {
			return ""
		}
	}

	d.fired = true

	debug.Log("drift_recurrence",
		"drift recurrence: %d new dirs post-warning, %d edits, %d verifies (ratio=%.2f)",
		newDirs, d.postWarnEdits, d.postWarnVerifies, verifyRatio)

	var sb strings.Builder
	header := fmt.Sprintf(
		"[Drift Recurrence] After receiving a drift warning at iteration %d, "+
			"you have continued the same pattern: %d new directories touched, "+
			"%d edits with only %d verification actions.",
		d.warnIteration, newDirs, d.postWarnEdits, d.postWarnVerifies)
	sb.WriteString(header)

	if newDirs >= driftRecurrenceNewDirThreshold {
		sb.WriteString(" Your file scope is still expanding despite the warning.")
	}
	if verifyRatio == 0 && d.postWarnEdits >= driftRecurrenceMinEditsPostWarn {
		sb.WriteString(" You have not run ANY verification (build/test/lint) since the warning.")
	}

	sb.WriteString(
		"\n\nThis suggests the earlier warning was acknowledged but not acted upon. " +
			"Performative compliance -- saying 'you're right' then continuing the same behavior -- " +
			"wastes iterations and compounds errors. STOP and genuinely reassess: " +
			"(1) re-read the original task requirements, " +
			"(2) identify which changes are actually necessary vs. optional, " +
			"(3) run verification (build/test) before making any more edits, " +
			"(4) if the task genuinely requires this scope, explain WHY before continuing.")

	return sb.String()
}

// --- Agent integration ---

// driftRecurrenceRecord tracks an edit for drift recurrence analysis.
func (a *Agent) driftRecurrenceRecord(toolName string, fileHint string) {
	if a.driftRecurrence == nil {
		return
	}
	if productiveEditTools[toolName] {
		a.driftRecurrence.recordEdit(fileHint)
	}
	if verificationDebtTools[toolName] == debtVerifying {
		a.driftRecurrence.recordVerification()
	}
}

// driftRecurrenceMarkWarn records that a drift warning fired.
func (a *Agent) driftRecurrenceMarkWarn(iteration int) {
	if a.driftRecurrence != nil {
		a.driftRecurrence.markWarning(iteration)
	}
}

// driftRecurrenceCheck returns guidance if drift recurrence is detected.
func (a *Agent) driftRecurrenceCheck() string {
	if a.driftRecurrence == nil {
		return ""
	}
	return a.driftRecurrence.check()
}

// resetDriftRecurrence clears state for a new run.
func (a *Agent) resetDriftRecurrence() {
	if a.driftRecurrence != nil {
		a.driftRecurrence.reset()
	}
}
