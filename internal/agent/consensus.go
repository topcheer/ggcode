package agent

import (
	"time"
)

// The cross-detector consensus alert ("[Systemic Failure Detected /
// Cross-Detector Consensus] ... STOP incremental patching") was removed as
// dead code: check() had no caller left, and it was a counter-driven steering
// message. recordFiring stays so the budget-crossing bookkeeping keeps a
// record of which detectors fired.
const consensusWindowSteps = 5

type consensusState struct {
	firings     []consensusFiring
	currentStep int
}

type consensusFiring struct {
	detector string
	step     int
	time     time.Time
}

func newConsensusState() *consensusState {
	return &consensusState{
		firings: make([]consensusFiring, 0, 32),
	}
}

func (s *consensusState) reset() {
	s.firings = s.firings[:0]
	s.currentStep = 0
}

func (s *consensusState) recordFiring(detectorName string, step int) {
	if s == nil || detectorName == "" {
		return
	}
	if step > s.currentStep {
		s.currentStep = step
	}
	s.firings = append(s.firings, consensusFiring{
		detector: detectorName,
		step:     step,
		time:     time.Now(),
	})

	if len(s.firings) > consensusWindowSteps*6 {
		cutoff := s.currentStep - consensusWindowSteps
		kept := s.firings[:0]
		for _, f := range s.firings {
			if f.step > cutoff {
				kept = append(kept, f)
			}
		}
		s.firings = kept
	}
}
