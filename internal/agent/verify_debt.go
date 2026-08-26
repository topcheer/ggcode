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
//     fires escalating warnings at higher thresholds (7, 12)
//
// Zero LLM cost. Non-blocking. Thread-safe.
type verifyDebtState struct {
	mu                sync.Mutex
	editsSinceGreen   int    // total source edits since last successful build
	totalEdits        int    // total source edits this run
	totalGreenBuilds  int    // total successful verification commands this run
	lastGreenBuildCmd string // last successful verify command
	warningsIssued    int    // warnings issued this run (cap at 3)
	lastWarnDebt      int    // debt level at last warning (to avoid repeated moderate warnings)
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
// crosses warning thresholds. Called at iteration start.
//
// Escalation:
//   - 7+ edits: moderate risk, remind to verify
//   - 12+ edits: high risk, emphasize compounding failure probability
//   - Capped at verifyDebtMax warnings per run
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

	// Only increment warning counter when debt crosses a new threshold,
	// not on every call with the same debt level. This prevents moderate
	// warnings at debt=7 from consuming the entire cap before the
	// high-risk warning at debt=12 can fire.
	if debt >= verifyDebtWarn2 {
		// High-risk threshold: only fire if we haven't already warned at this level
		if s.lastWarnDebt >= verifyDebtWarn2 {
			return ""
		}
	} else {
		// Moderate threshold: only fire if we haven't already warned at moderate
		if s.lastWarnDebt >= verifyDebtWarn1 {
			return ""
		}
	}

	s.warningsIssued++
	s.lastWarnDebt = debt

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
