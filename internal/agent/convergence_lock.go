package agent

// Convergence Lock — Post-Verification Unnecessary Edit Prevention
//
// Research basis: Agent convergence literature (SICA overseer, AutoGPT behavior
// studies) shows that autonomous agents frequently continue making changes
// AFTER their core task is complete and verified. This "post-verification drift"
// manifests as:
//   - Refactoring code that already works
//   - Adding "improvements" that weren't requested
//   - Editing files tangential to the task after verification passes
//   - Polishing comments or formatting after tests pass
//
// Competitor analysis:
//   - Claude Code: no post-verification behavior change; agent continues until
//     it decides to stop or the user interrupts
//   - Cursor: stops when the user stops; no automatic convergence detection
//   - OpenHands: separate planner LLM detects completion (costs extra tokens)
//   - Devin: explicit "done" state but requires external/user confirmation
//   - Windsurf: no post-verification convergence logic
//
// Gap: No deterministic system detects when an agent has verified its changes
// successfully and is now making unnecessary additional edits. This wastes
// tokens, burns context budget, and risks introducing regressions to
// already-working code — a common frustration in autopilot/long-running tasks.
//
// Our approach: track successful verification events (build/test/lint commands
// that succeed) and count subsequent file edits. When edits exceed thresholds,
// inject targeted guidance encouraging the agent to finalize. Zero LLM cost.
//
// Interaction with existing guards:
//   - verify_hint.go: suggests running verification after edits (BEFORE verify)
//   - recurring_error.go: detects same error after edits (WHEN verify fails)
//   - scope_drift.go: detects file diversity creep (DURING execution)
//   - fulfillment_gate.go: checks if requirements are met (AT completion)
//   - THIS: detects unnecessary edits AFTER successful verification
//
// The convergence lock resets on failed verification (the agent found issues
// and is fixing them — that's legitimate work, not drift).

import (
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	// convergenceEditThreshold: max edits allowed after successful verification
	// before the first convergence warning fires. Allows for minor fixes
	// discovered during verification review without triggering.
	convergenceEditThreshold = 3

	// convergenceEscalationThreshold: a second, stronger warning fires at this
	// many post-verification edits. At this point the agent is almost certainly
	// doing unnecessary work.
	convergenceEscalationThreshold = 6

	// convergenceCmdDisplayLen: max length of the verify command string to
	// include in guidance messages.
	convergenceCmdDisplayLen = 60
)

// convergenceLockState tracks post-verification edit drift.
type convergenceLockState struct {
	// verified is true after a build/test/verify command succeeds.
	verified bool

	// verifiedCommand is the command that last succeeded (for message context).
	verifiedCommand string

	// postVerifyEdits counts file edits made after the last successful verification.
	postVerifyEdits int

	// postVerifyErrors counts errors from post-verification edits.
	postVerifyErrors int

	// warned tracks whether the first convergence warning has fired.
	warned bool

	// escalated tracks whether the escalation warning has fired.
	escalated bool
}

func newConvergenceLockState() *convergenceLockState {
	return &convergenceLockState{}
}

// reset clears all state for a new run.
func (s *convergenceLockState) reset() {
	s.verified = false
	s.verifiedCommand = ""
	s.postVerifyEdits = 0
	s.postVerifyErrors = 0
	s.warned = false
	s.escalated = false
}

// recordVerifyResult records the outcome of a build/test/verify command.
// A successful verification sets the "verified" state. A failed verification
// clears it (the agent is fixing issues, which is legitimate work).
func (s *convergenceLockState) recordVerifyResult(command string, isError bool) {
	if isError {
		// Failed verification resets the "verified" state — the agent needs
		// to fix issues and re-verify. This is not drift.
		if s.verified {
			debug.Log("convergence_lock", "verification failed, resetting convergence state (was verified by %q)", s.verifiedCommand)
			s.verified = false
			s.postVerifyEdits = 0
			s.postVerifyErrors = 0
			s.warned = false
			s.escalated = false
		}
		return
	}
	// Successful verification
	wasVerified := s.verified
	s.verified = true
	s.verifiedCommand = command
	// Reset edit counters on fresh verification to avoid carrying over
	// edits from before the previous verification round.
	s.postVerifyEdits = 0
	s.postVerifyErrors = 0
	s.warned = false
	s.escalated = false
	if !wasVerified {
		debug.Log("convergence_lock", "verification succeeded: %q — convergence lock armed", truncateCmd(command))
	}
}

// recordEdit tracks a file edit after successful verification.
func (s *convergenceLockState) recordEdit() {
	if s.verified {
		s.postVerifyEdits++
	}
}

// recordEditError tracks errors from post-verification edits.
func (s *convergenceLockState) recordEditError() {
	if s.verified {
		s.postVerifyErrors++
	}
}

// check returns guidance text if the convergence lock should fire, empty otherwise.
// Each tier fires at most once per run.
func (s *convergenceLockState) check() string {
	if !s.verified {
		return ""
	}

	// Escalation at convergenceEscalationThreshold
	if s.postVerifyEdits >= convergenceEscalationThreshold && !s.escalated {
		s.escalated = true
		debug.Log("convergence_lock", "post-verification drift ESCALATION: %d edits after verify (%q), %d errors",
			s.postVerifyEdits, truncateCmd(s.verifiedCommand), s.postVerifyErrors)

		msg := fmt.Sprintf(
			"Convergence alert: %d file edits made after successful verification (\"%s\"). "+
				"Your changes were verified and working. Each additional edit risks breaking what was confirmed working. "+
				"Stop making changes unless you are fixing a specific, identified issue. "+
				"If the task is complete, provide a summary of what was done.",
			s.postVerifyEdits, truncateCmd(s.verifiedCommand),
		)
		if s.postVerifyErrors > 0 {
			msg += fmt.Sprintf(
				" Warning: %d of these post-verification edits resulted in errors — strong signal to stop and re-examine your approach.",
				s.postVerifyErrors,
			)
		}
		return msg
	}

	// First warning at convergenceEditThreshold
	if s.postVerifyEdits >= convergenceEditThreshold && !s.warned {
		s.warned = true
		debug.Log("convergence_lock", "post-verification drift detected: %d edits after verify (%q)",
			s.postVerifyEdits, truncateCmd(s.verifiedCommand))

		return fmt.Sprintf(
			"Convergence check: You have made %d edits since your changes verified successfully (\"%s\"). "+
				"Consider whether these additional changes are truly necessary for the task. "+
				"If the core work is complete, finalize and summarize instead of continuing to edit.",
			s.postVerifyEdits, truncateCmd(s.verifiedCommand),
		)
	}

	return ""
}

// truncateCmd shortens a command string for display in guidance messages.
func truncateCmd(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if len(cmd) <= convergenceCmdDisplayLen {
		return cmd
	}
	return cmd[:convergenceCmdDisplayLen-3] + "..."
}

// --- Agent integration ---

// convergenceRecordVerify records a verification command result for convergence tracking.
// Called alongside maybeResetVerifyOnCommand to track successful verification events.
func (a *Agent) convergenceRecordVerify(toolName string, args []byte, resultErr bool) {
	if a.convergenceLock == nil {
		return
	}
	if toolName != "run_command" {
		return
	}
	cmd := extractCommandFromArgs(args)
	if cmd == "" || !isVerifyCommand(cmd) {
		return
	}
	a.convergenceLock.recordVerifyResult(cmd, resultErr)
}

// convergenceRecordEdit tracks a file edit for convergence drift detection.
func (a *Agent) convergenceRecordEdit(toolName string) {
	if a.convergenceLock == nil {
		return
	}
	if !productiveEditTools[toolName] {
		return
	}
	a.convergenceLock.recordEdit()
}

// convergenceRecordEditError tracks errors from post-verification edits.
func (a *Agent) convergenceRecordEditError() {
	if a.convergenceLock == nil {
		return
	}
	a.convergenceLock.recordEditError()
}

// convergenceCheck returns convergence drift guidance if applicable.
func (a *Agent) convergenceCheck() string {
	if a.convergenceLock == nil {
		return ""
	}
	return a.convergenceLock.check()
}

// resetConvergenceLock clears convergence state for a new run.
func (a *Agent) resetConvergenceLock() {
	if a.convergenceLock != nil {
		a.convergenceLock.reset()
	}
}
