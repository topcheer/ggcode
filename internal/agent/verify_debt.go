package agent

import (
	"fmt"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

// verifyDebtState tracks accumulated source-code mutations since the last
// *successful* build/test. Unlike postEditVerify (which resets the counter
// on ANY build command including failed ones), this accumulator only clears
// on a green build -- because failed builds verify nothing.
//
// This implements the "Agent Last Mile Failure" insight (arXiv:2602.16666,
// 2026): per-step errors compound geometrically (0.95^20 = 36% task
// completion). Agents that pile up edits on an unverified foundation reach
// terminal steps with compounding latent defects. The #1 differentiator
// between high/low-performing agents is recovery skill -- specifically,
// recognizing and verifying accumulated changes before they compound into
// catastrophic last-mile failures.
//
// The debt accumulator provides a complementary signal to postEditVerify:
//   - postEditVerify: fires every 3 edits, resets on ANY build attempt
//   - verifyDebt:     accumulates continuously, only clears on green build,
//     fires ONE warning per run when debt crosses the moderate threshold
//     (7); if the first crossing already lands at 12+ the single warning
//     uses the high-risk wording (once-per-run: batch 2 guidance-noise
//     cleanup, 83be1c99). Gradual escalation beyond the first warning was
//     intentionally dropped with that cap.
//
// Zero LLM cost. Non-blocking. Thread-safe.
type verifyDebtState struct {
	mu                sync.Mutex
	editsSinceGreen   int    // total source edits since last successful build
	totalEdits        int    // total source edits this run
	totalGreenBuilds  int    // total successful verification commands this run
	lastGreenBuildCmd string // last successful verify command
	warningsIssued    int    // warnings issued this run (cap at 1)
}

// verifyDebt thresholds for escalating warnings.
const (
	verifyDebtWarn1 = 7  // first warning: moderate risk
	verifyDebtWarn2 = 12 // second warning: high risk
	verifyDebtMax   = 1  // max warnings per run
)

func newVerifyDebtState() *verifyDebtState {
	return &verifyDebtState{}
}

// recordSourceEdit increments the unverified-edit counter. Called after each
// successful source-code file edit.
func (s *verifyDebtState) recordSourceEdit() {
	s.mu.Lock()
	s.editsSinceGreen++
	s.totalEdits++
	s.mu.Unlock()
}

// recordVerifyCommand records the outcome of a build/test/verify command.
// Only successful commands clear the debt; failed commands leave it
// unchanged because they provide no verification value.
func (s *verifyDebtState) recordVerifyCommand(cmd string, failed bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !failed {
		debug.Log("agent", "verify debt cleared: green build after %d unverified edits (cmd=%q)",
			s.editsSinceGreen, cmd)
		s.editsSinceGreen = 0
		s.totalGreenBuilds++
		s.lastGreenBuildCmd = cmd
	}
	// Failed builds do NOT clear debt -- they verify nothing.
}

// maybeWarn returns a guidance string when accumulated verification debt
// crosses the warning threshold. Called at iteration start.
//
// Single warning per run (#1228: once-per-run is the design since the
// batch 2 guidance-noise cleanup; the moderate (7) and high-risk (12)
// wordings select which message the ONE warning uses - the first crossing
// picks by current debt level):
//   - 7-11 edits: moderate risk, remind to verify
//   - 12+ edits: high risk, emphasize compounding failure probability
func (s *verifyDebtState) maybeWarn(iteration int) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.warningsIssued >= verifyDebtMax {
		return ""
	}

	debt := s.editsSinceGreen
	if debt < verifyDebtWarn1 {
		return ""
	}

	s.warningsIssued++

	var msg string
	if debt >= verifyDebtWarn2 {
		// Compute compound failure probability at 95% per-step accuracy
		// to illustrate the geometric compounding risk.
		prob := compoundSuccessProb(0.95, debt)
		msg = fmt.Sprintf(
			"[Verification debt: %d source edits since the last *successful* build. "+
				"At typical per-step accuracy, the probability all %d changes are correct is ~%.0f%%. "+
				"Run a full build+test now to establish a verified baseline before making further changes.]",
			debt, debt, prob*100,
		)
	} else {
		msg = fmt.Sprintf(
			"[Verification debt: %d source edits accumulated since the last successful build. "+
				"Run a build+test to verify accumulated changes before proceeding further.]",
			debt,
		)
	}

	debug.Log("agent", "verify debt warning #%d: %d edits since green build (iter=%d)",
		s.warningsIssued, debt, iteration)

	return msg
}

// reset clears state for a new user turn.
func (s *verifyDebtState) reset() {
	s.mu.Lock()
	s.editsSinceGreen = 0
	s.totalEdits = 0
	s.totalGreenBuilds = 0
	s.lastGreenBuildCmd = ""
	s.warningsIssued = 0
	s.mu.Unlock()
}

// compoundSuccessProb computes the probability that all n steps succeed,
// given per-step success probability p. Implements the geometric compounding
// model from "Towards a Science of AI Agent Reliability" (arXiv:2602.16666).
func compoundSuccessProb(p float64, n int) float64 {
	result := 1.0
	for i := 0; i < n; i++ {
		result *= p
	}
	return result
}
