package agent

// Compounded Trajectory Uncertainty Accumulator
//
// Research basis:
//   - Zhang et al., "Agentic Uncertainty Quantification" (arXiv:2601.15703,
//     January 2026, Salesforce Research). Introduces the "Spiral of
//     Hallucination" concept: early epistemic errors, undetected by passive
//     monitoring, propagate irreversibly through reasoning chains. Each
//     subsequent step builds on a flawed premise.
//   - "Position: Uncertainty Quantification Needs Reassessment for Large
//     Language Model Agents" (ICML 2025, arXiv:2505.22655). Identifies
//     "Compounded Trajectory Uncertainty" as a distinct category: in
//     sequential decision-making, individual step uncertainties combine.
//     A 90%-confident agent making 20 sequential uncertain decisions faces
//     trajectory reliability of only 0.9^20 = 12%. This is qualitatively
//     different from single-step uncertainty.
//   - "Uncertainty Quantification in LLM Agents: Foundations, Emerging
//     Challenges, and Opportunities" (arXiv:2602.05073). Formalizes that
//     agentic uncertainty is multiplicative, not additive - a distinction
//     missing from current agent monitoring tools.
//   - Zylos.ai (April 2026): "Confidence-gated escalation is the dominant
//     production pattern: when an agent's accumulated uncertainty exceeds
//     a threshold, it should pause and reconsider rather than continuing
//     to build on increasingly uncertain foundations."
//
// Problem: ggcode has many individual detectors for epistemic risk signals
// (action hedging, assumption tracking, unverified success claims, premature
// surrender, false premises, etc.). But these fire independently and
// ephemerally. No mechanism tracks how epistemic risk ACCUMULATES across a
// full trajectory:
//
//  1. Turn 1: "I assume this is a PostgreSQL database" -> assumption detected
//     (1 unverified assumption)
//  2. Turn 3: "hopefully this fixes it" -> hedging detected (1 hedging signal)
//  3. Turn 5: "I've fixed the issue" -> premature success claim (no verification)
//  4. Turn 7: "Let's try a different approach" -> hedging again
//
//  Each signal individually is advisory. But their accumulation means the
//  entire trajectory's conclusions rest on increasingly uncertain foundations.
//  The probability that ALL of these are simultaneously correct drops
//  multiplicatively. By turn 7, the agent may have <50% trajectory confidence
//  even though no individual detector's threshold for immediate concern was
//  crossed.
//
//  Existing detectors that are RELATED but do NOT cover this:
//   - trajectory_health.go: synthesizes OPERATIONAL signals (retries,
//     failures, verbosity, latency). This detector synthesizes EPISTEMIC
//     signals (hedging, assumptions, unverified claims) - fundamentally
//     different input dimensions.
//   - action_hedging.go: single-turn hedging detection. This detector
//     accumulates hedging across the ENTIRE trajectory.
//   - assumption_track.go: single-turn assumption detection. This detector
//     accumulates assumptions across the ENTIRE trajectory.
//
// Gap: No mechanism accumulates epistemic risk signals across a trajectory
// and warns when their multiplicative compounding makes the overall
// trajectory increasingly unreliable. This detector addresses that gap by
// counting distinct epistemic uncertainty events and triggering when
// their compounded probability falls below a reliability threshold.
//
// Design:
//   - Tracks a running count of epistemic uncertainty events across the run
//   - Categories: hedging signals, assumptions, unverified success claims,
//     false premises
//   - Models each event as ~0.85 per-step reliability (85% chance of being
//     correct given it was flagged)
//   - Computes compounded trajectory reliability = 0.85^(uncertainty count)
//   - Triggers when compounded reliability drops below 0.40 (i.e., <40%
//     chance the trajectory's conclusions are fully sound)
//   - Zero LLM cost - pure deterministic counting and arithmetic
//   - Fires at most once per run (advisory, non-blocking)
//   - Resets on new user turn

import (
	"fmt"
	"math"
)

const (
	// perStepReliability models the probability that a flagged epistemic
	// uncertainty event is actually correct. This is conservative - research
	// suggests hedging language predicts failure at 2-3x normal rates.
	perStepReliability = 0.85

	// uncertaintyAlertThreshold: at this accumulated weight, trigger.
	// With perStepReliability=0.85, a weight of 5.5 gives:
	//   0.85^5.5 = 0.40, the reliability threshold.
	uncertaintyAlertThreshold = 5.5

	// uncertaintyWeight per category - how many "units" of uncertainty each
	// event type contributes. Hedging and assumptions are 1 unit each;
	// unverified success claims are 1.5 (higher risk - the agent claims
	// success without verifying, which is worse than just being uncertain);
	// false premises are 2 (highest risk - the entire approach may be wrong).
	weightHedging        = 1.0
	weightAssumption     = 1.0
	weightUnverifiedSucc = 1.5
	weightFalsePremise   = 2.0
)

// compoundedUncertaintyState tracks accumulated epistemic uncertainty.
type compoundedUncertaintyState struct {
	totalWeight float64
	fired       bool
}

func newCompoundedUncertaintyState() *compoundedUncertaintyState {
	return &compoundedUncertaintyState{}
}

func (s *compoundedUncertaintyState) reset() {
	s.totalWeight = 0
	s.fired = false
}

// recordUncertainty adds an epistemic uncertainty event to the trajectory.
// Called by other detectors (or directly) when an epistemic risk signal fires.
func (a *Agent) recordUncertainty(_ string, weight float64) {
	if a.compoundedUncert == nil {
		return
	}
	a.compoundedUncert.totalWeight += weight
}

// maybeWarnCompoundedUncertainty checks if the accumulated epistemic
// uncertainty across the trajectory has crossed the reliability threshold.
// Returns a guidance message if so, empty string otherwise.
func (a *Agent) maybeWarnCompoundedUncertainty() string {
	if a.compoundedUncert == nil || a.compoundedUncert.fired {
		return ""
	}
	if a.compoundedUncert.totalWeight < uncertaintyAlertThreshold {
		return ""
	}

	// Compute compounded reliability for the guidance message.
	compounded := math.Pow(perStepReliability, a.compoundedUncert.totalWeight)

	a.compoundedUncert.fired = true

	pct := int(compounded * 100)
	return fmt.Sprintf(
		"[compounded-trajectory-uncertainty] Accumulated epistemic risk across "+
			"this trajectory has crossed the reliability threshold. Your run has "+
			"accumulated %.1f units of uncertainty (hedging language, unverified "+
			"assumptions, speculative success claims, or false premises).\n"+
			"Compounded trajectory reliability: ~%d%% (each individual signal may "+
			"seem minor, but uncertainties compound multiplicatively - a 0.85^N "+
			"decay).\n"+
			"This means there is roughly a %d%% chance that the conclusions of this "+
			"entire run are fully sound. Before proceeding with further edits:\n"+
			"1. Re-examine the foundational assumptions made early in the run\n"+
			"2. Verify that the root-cause hypothesis still holds\n"+
			"3. If possible, validate the current approach against a test or "+
			"diagnostic before continuing to build on it\n"+
			"Research basis: agentic uncertainty compounds multiplicatively across "+
			"trajectories (arXiv:2505.22655, arXiv:2601.15703). Continuing to "+
			"build on uncertain foundations risks the 'Spiral of Hallucination' "+
			"where each step amplifies prior epistemic errors.",
		a.compoundedUncert.totalWeight, pct, pct,
	)
}
