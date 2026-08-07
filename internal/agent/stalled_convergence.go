package agent

// Stalled Convergence Detector
//
// Research basis:
//   - SICA (arXiv 2504.15228): self-improving coding agents exhibit
//     "convergence stagnation" -- error counts decrease slowly but plateau,
//     indicating the agent is patching symptoms rather than root causes.
//   - ODCV-Bench (arXiv 2512.20798): outcome-driven constraint violations
//     where agents optimize a partial metric (error count) without actually
//     achieving the goal (compilation/test success).
//   - Anthropic Context Engineering (Sep 2025): agents get stuck in
//     "diminishing returns" loops where each iteration yields smaller
//     improvements until progress effectively halts.
//
// Problem: AI coding agents fixing build/test errors often show a pattern:
//   Iteration 1: 20 errors -> edit -> 15 errors (good progress)
//   Iteration 2: 15 errors -> edit -> 13 errors (decelerating)
//   Iteration 3: 13 errors -> edit -> 12 errors (plateauing)
//   Iteration 4: 12 errors -> edit -> 12 errors (stalled)
//   Iteration 5: 12 errors -> edit -> 11 errors (trivial)
// The agent is stuck in a local optimum -- each fix only addresses a
// surface symptom while the root cause persists. No existing detector
// catches this "slow bleed" pattern.
//
// Detection: Track error counts across verification runs. When 3+
// consecutive verifications show diminishing improvements (delta < 30%
// of the initial delta), inject guidance to step back and reconsider
// the approach rather than continuing to patch individual errors.
//
// What this does NOT duplicate:
//   - error_regression.go: detects error count INCREASES (opposite direction)
//   - fix_cascade.go: detects edit->fail CYCLES (same errors returning)
//   - recurring_error.go: detects same error FINGERPRINT recurring
//   - compounding_failure.go: tracks aggregate failure RATE, not trend slope
//   - convergence_lock.go: fires after SUCCESS (post-verification drift)
//
// Design: zero LLM cost, deterministic. Fires at most twice per run.

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	// stalledMinSamples: minimum number of verification data points before
	// assessing trend (we need at least 3: initial, improved, stalled).
	stalledMinSamples = 2

	// maxStalledWarnings caps how many stagnation warnings fire per run.
	maxStalledWarnings = 2

	// stalledDecelRatio: if the latest delta is less than this fraction of
	// the peak delta, the convergence has effectively stalled.
	stalledDecelRatio = 0.3
)

// stalledConvergenceCheckCommand is the Agent-level wrapper that checks
// whether a verification command result shows a stalled convergence pattern.
// Returns guidance if stagnation is detected.
func (a *Agent) stalledConvergenceCheckCommand(toolName string, args []byte, content string, isError bool) string {
	if a.stalledConvergence == nil {
		return ""
	}
	if toolName != "run_command" {
		return ""
	}
	cmd := extractCommandFromArgs(args)
	if cmd == "" || !isVerifyCommand(cmd) {
		return ""
	}
	return a.stalledConvergence.recordVerify(content, isError)
}

// stalledConvergenceState tracks error count trends across verifications
// to detect stalled convergence patterns.
type stalledConvergenceState struct {
	// errorHistory records error counts from successive verifications.
	errorHistory []int

	// hadEdits tracks whether edits occurred since last verification.
	hadEdits bool

	// warningCount tracks how many stagnation warnings fired this run.
	warningCount int
}

func newStalledConvergenceState() *stalledConvergenceState {
	return &stalledConvergenceState{}
}

func (s *stalledConvergenceState) reset() {
	s.errorHistory = nil
	s.hadEdits = false
	s.warningCount = 0
}

// recordEdit notes that a file edit occurred.
func (s *stalledConvergenceState) recordEdit() {
	s.hadEdits = true
}

// recordVerify processes a verification result and returns guidance if
// stalled convergence is detected.
func (s *stalledConvergenceState) recordVerify(content string, failed bool) string {
	currentErrors := countVerifyErrors(content)

	// Only track if there are errors and edits were made.
	if !failed || !s.hadEdits {
		// Still record for baseline, but don't assess trend without edits.
		s.errorHistory = append(s.errorHistory, currentErrors)
		s.hadEdits = false
		return ""
	}

	s.errorHistory = append(s.errorHistory, currentErrors)
	s.hadEdits = false

	// Need at least stalledMinSamples+1 data points to assess a trend.
	if len(s.errorHistory) < stalledMinSamples+1 {
		return ""
	}

	// Cap history to avoid unbounded growth.
	if len(s.errorHistory) > 10 {
		s.errorHistory = s.errorHistory[len(s.errorHistory)-10:]
	}

	if s.warningCount >= maxStalledWarnings {
		return ""
	}

	if isStalledConvergence(s.errorHistory) {
		s.warningCount++
		debug.Log("stalled-convergence",
			"error history %v shows stalled convergence -- agent may be patching symptoms",
			s.errorHistory)
		return stalledGuidance(s.errorHistory)
	}

	return ""
}

// isStalledConvergence analyzes the error count history to detect a
// decelerating convergence pattern.
//
// Pattern: errors are decreasing (overall trend is down) but the rate
// of improvement has decelerated to near-zero -- the last delta is less
// than stalledDecelRatio of the peak delta.
func isStalledConvergence(history []int) bool {
	n := len(history)
	if n < stalledMinSamples+1 {
		return false
	}

	// Compute deltas between consecutive verifications.
	deltas := make([]int, 0, n-1)
	for i := 1; i < n; i++ {
		deltas = append(deltas, history[i]-history[i-1])
	}

	// The errors should be non-increasing (overall downward or flat).
	for _, d := range deltas {
		if d > 0 {
			return false // error count increased -- not convergence
		}
	}

	// Find the peak improvement (most negative delta = biggest improvement).
	peakImprovement := 0
	for _, d := range deltas {
		if d < peakImprovement {
			peakImprovement = d
		}
	}

	// If there was never a meaningful improvement, this isn't "stalled
	// convergence" -- it's just a flat error count (other detectors handle that).
	if peakImprovement > -1 {
		return false
	}

	// Check the last delta: if it's near-zero relative to peak,
	// convergence has stalled. With 3+ samples, we need at least the
	// last delta to be small. With 4+ samples, we also check the
	// second-to-last to confirm the stall is persistent.
	lastDelta := deltas[len(deltas)-1]
	peakAbs := -peakImprovement // make positive
	if peakAbs == 0 {
		return false
	}

	// Last delta deceleration ratio: |lastDelta| / peakAbs
	lastRatio := float64(absInt(lastDelta)) / float64(peakAbs)

	// Stalled: last delta is less than stalledDecelRatio of peak.
	if lastRatio >= stalledDecelRatio {
		return false
	}

	// With 4+ samples (3+ deltas), also require the second-to-last delta
	// to be decelerated, confirming a persistent stall.
	if len(deltas) >= 3 {
		prevDelta := deltas[len(deltas)-2]
		prevRatio := float64(absInt(prevDelta)) / float64(peakAbs)
		if prevRatio >= stalledDecelRatio {
			return false
		}
	}

	return true
}

// absInt returns the absolute value of an integer.
func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// stalledGuidance generates the guidance message for stalled convergence.
func stalledGuidance(history []int) string {
	last := history[len(history)-1]
	histStr := make([]string, len(history))
	for i, v := range history {
		histStr[i] = strconv.Itoa(v)
	}
	return "[STALLED CONVERGENCE] " +
		"Error count trend: " + strings.Join(histStr, " -> ") +
		" -- improvements are decelerating. You are likely patching surface " +
		"symptoms rather than fixing the root cause. " +
		fmt.Sprintf("%d errors remain after multiple fix attempts with diminishing returns.\n\n", last) +
		"Recommended actions:\n" +
		"1. STOP making incremental fixes to individual errors.\n" +
		"2. Re-read the FULL error output and look for a COMMON root cause.\n" +
		"   (e.g., a missing import, wrong type signature, API change)\n" +
		"3. Fix the root cause -- multiple errors may disappear at once.\n" +
		"4. If errors are genuinely independent, consider reverting recent\n" +
		"   changes and taking a different approach entirely."
}
