package agent

// Self-Correction Stability Gate — EIR/ECR-based correction loop gating
//
// Research basis: "Self-Correction as Feedback Control: Error Dynamics,
// Stability Thresholds, and Prompt Interventions in LLMs" (arXiv:2604.22273)
//
// The paper recasts iterative self-correction as a closed-loop feedback control
// problem. Two state parameters determine stability:
//
//   - EIR (Error Introduction Rate): probability of introducing a NEW error
//     during a correction round.
//   - ECR (Error Correction Rate): probability of fixing an existing error
//     during a correction round.
//
// Stability threshold: ECR/EIR > Acc/(1-Acc) must hold for self-correction to
// be beneficial. When it doesn't, each correction round introduces more errors
// than it fixes — the loop is net-negative and should be stopped.
//
// Empirical findings (7 frontier models, GSM8K, 4 iterations):
//   - Models with EIR ≤ 0.5% benefit from self-correction (+0.2 to +3.4 pp)
//   - Models with EIR ~2% degrade significantly (-6.2 pp)
//   - A "verify-first" prompt intervention reduces EIR from 2% to ~0%, converting
//     harmful loops into stable ones.
//
// Existing ggcode systems that are CLOSE but don't cover this:
//
//   - verify_regression.go: classifies each verification cycle's errors as
//     NEW / PERSISTENT / RESOLVED. This is exactly the raw data needed for
//     EIR/ECR computation, but it doesn't aggregate across rounds or compute
//     stability.
//   - recurring_error.go: detects when the SAME error persists across edits
//     (root-cause detection). Complementary — it catches "no progress", while
//     this gate catches "net-negative progress" (fixing A but introducing B).
//   - loop_detect.go: catches consecutive identical tool calls or generic error
//     streaks. Doesn't distinguish "introduced new error" from "same error".
//
// The gap: when self-correction is net-negative (EIR > ECR scaled by accuracy),
// no existing system tells the agent to STOP correcting and step back. The agent
// keeps fixing newly introduced errors in an endless whack-a-mole cycle. This
// gate fills that gap by computing ECR/EIR from verify_regression data and
// injecting a stability warning when the ratio falls below threshold.
//
// Implementation:
//   - Consumes per-round NEW/PERSISTENT/RESOLVED counts from verifyRegression
//   - Maintains running EIR (new errors introduced) and ECR (errors resolved)
//   - Fires at most once per run when ECR/EIR drops below stability threshold
//   - Requires a minimum sample size (3 correction rounds) before evaluating

import (
	"fmt"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	// scGateMinRounds: minimum correction rounds before evaluating stability.
	// Too few rounds and the ratio is noisy. The paper uses 4 iterations;
	// we require 3 to start evaluating since ggcode's auto-repair loop is
	// shorter per verify cycle.
	scGateMinRounds = 3

	// scGateStabilityRatio: the minimum ECR/EIR ratio for stable self-correction.
	// Derived from the paper's threshold ECR/EIR > Acc/(1-Acc) assuming a
	// conservative base accuracy Acc ≈ 0.6, which gives Acc/(1-Acc) = 1.5.
	// We use a slightly more lenient threshold (1.2) to avoid false positives
	// on agents that are making slow but net-positive progress.
	scGateStabilityRatio = 1.2
)

// selfCorrectionGateState tracks EIR/ECR across verification correction rounds
// to detect when self-correction has become net-negative (introducing more
// errors than it fixes).
type selfCorrectionGateState struct {
	// totalRounds counts the number of correction rounds observed (each verify
	// cycle with errors = one round).
	totalRounds int

	// totalNewErrors accumulates NEW error counts across all rounds (EIR proxy).
	totalNewErrors int

	// totalResolvedErrors accumulates RESOLVED error counts across all rounds
	// (ECR proxy).
	totalResolvedErrors int

	// totalPersistent accumulates PERSISTENT error counts (informational).
	totalPersistent int

	// fired tracks whether the stability warning has been injected this run.
	fired bool
}

func newSelfCorrectionGateState() *selfCorrectionGateState {
	return &selfCorrectionGateState{}
}

func (s *selfCorrectionGateState) reset() {
	if s == nil {
		return
	}
	s.totalRounds = 0
	s.totalNewErrors = 0
	s.totalResolvedErrors = 0
	s.totalPersistent = 0
	s.fired = false
}

// recordRound feeds per-round regression counts into the stability gate and
// returns a non-empty guidance message if the self-correction loop has become
// net-negative (EIR-dominated). Returns "" if no warning is warranted.
//
// Parameters:
//   - newErrors: errors introduced by the agent's recent edits (regressions)
//   - persistentErrors: errors unchanged from the previous round
//   - resolvedErrors: errors fixed since the previous round
func (s *selfCorrectionGateState) recordRound(newErrors, persistentErrors, resolvedErrors int) string {
	if s == nil || s.fired {
		// Still accumulate data even after firing (for diagnostics).
		if s != nil {
			s.totalRounds++
			s.totalNewErrors += newErrors
			s.totalResolvedErrors += resolvedErrors
			s.totalPersistent += persistentErrors
		}
		return ""
	}

	s.totalRounds++
	s.totalNewErrors += newErrors
	s.totalResolvedErrors += resolvedErrors
	s.totalPersistent += persistentErrors

	// Need enough rounds to trust the ratio.
	if s.totalRounds < scGateMinRounds {
		return ""
	}

	// If no new errors have been introduced at all, the loop is stable or
	// positive — no warning needed.
	if s.totalNewErrors == 0 {
		return ""
	}

	// Compute ECR/EIR ratio. EIR proxy = new errors introduced per round.
	// ECR proxy = errors resolved per round.
	eir := float64(s.totalNewErrors) / float64(s.totalRounds)
	ecr := float64(s.totalResolvedErrors) / float64(s.totalRounds)

	if eir <= 0 {
		return ""
	}

	ratio := ecr / eir

	debug.Log("self-correction-gate", "rounds=%d new=%d resolved=%d persistent=%d EIR=%.1f ECR=%.1f ratio=%.2f threshold=%.2f",
		s.totalRounds, s.totalNewErrors, s.totalResolvedErrors, s.totalPersistent, eir, ecr, ratio, scGateStabilityRatio)

	// If ratio is below stability threshold, self-correction is net-negative.
	if ratio < scGateStabilityRatio {
		s.fired = true
		return selfCorrectionUnstableGuidance(s.totalRounds, s.totalNewErrors, s.totalResolvedErrors, ratio)
	}

	return ""
}

// selfCorrectionUnstableGuidance generates the stability warning message.
// The message uses a "step back" framing (the paper's "verify-first" prompt
// intervention that reduces EIR from 2% to ~0%).
func selfCorrectionUnstableGuidance(rounds, newErrors, resolvedErrors int, ratio float64) string {
	return fmt.Sprintf(
		"[self-correction-unstable] %d cycles: %d new errors vs %d resolved (ratio %.1f < %.1f). Fixes introduce more errors than they resolve.",
		rounds, newErrors, resolvedErrors, ratio, scGateStabilityRatio,
	)
}

// --- Agent integration methods ---

// selfCorrectionGateRecordRound feeds regression data from a verification cycle
// into the stability gate. Called after verifyRegression.classifyErrors has
// produced its diff. The new/persistent/resolved counts are extracted from the
// same verification result.
//
// Returns non-empty guidance if the gate detects net-negative self-correction.
func (a *Agent) selfCorrectionGateRecordRound(newErrors, persistentErrors, resolvedErrors int) string {
	if a.selfCorrectionGate == nil {
		return ""
	}
	return a.selfCorrectionGate.recordRound(newErrors, persistentErrors, resolvedErrors)
}

// resetSelfCorrectionGate clears the stability gate for a new run.
func (a *Agent) resetSelfCorrectionGate() {
	if a.selfCorrectionGate != nil {
		a.selfCorrectionGate.reset()
	}
}
