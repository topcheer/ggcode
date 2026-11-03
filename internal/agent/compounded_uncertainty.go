package agent

import (
	"fmt"
	"math"
)

const (
	perStepReliability = 0.85

	uncertaintyAlertThreshold = 5.5

	weightHedging        = 1.0
	weightUnverifiedSucc = 1.5
	weightFalsePremise   = 2.0
)

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

func (a *Agent) recordUncertainty(_ string, weight float64) {
	if a.compoundedUncert == nil {
		return
	}
	a.compoundedUncert.totalWeight += weight
}

func (a *Agent) maybeWarnCompoundedUncertainty() string {
	if a.compoundedUncert == nil || a.compoundedUncert.fired {
		return ""
	}
	if a.compoundedUncert.totalWeight < uncertaintyAlertThreshold {
		return ""
	}

	compounded := math.Pow(perStepReliability, a.compoundedUncert.totalWeight)

	a.compoundedUncert.fired = true

	pct := int(compounded * 100)
	return fmt.Sprintf(
		"[compounded-trajectory-uncertainty] Accumulated epistemic risk across "+
			"this trajectory has crossed the reliability threshold. Your run has "+
			"accumulated %.1f units of uncertainty (hedging language, speculative "+
			"success claims, or false premises).\n"+
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
