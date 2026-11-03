package agent

import (
	"fmt"
	"strings"
	"time"
)

const (
	consensusWindowSteps   = 5
	consensusFireThreshold = 3
	consensusMaxAlerts     = 2
	consensusCooldownSteps = 8
)

type consensusState struct {
	firings       []consensusFiring
	alertsIssued  int
	lastAlertStep int
	currentStep   int
}

type consensusFiring struct {
	detector string
	step     int
	time     time.Time
}

func newConsensusState() *consensusState {
	return &consensusState{
		firings:       make([]consensusFiring, 0, 32),
		lastAlertStep: -consensusCooldownSteps,
	}
}

func (s *consensusState) reset() {
	s.firings = s.firings[:0]
	s.alertsIssued = 0
	s.lastAlertStep = -consensusCooldownSteps
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

func (s *consensusState) check() string {
	if s == nil {
		return ""
	}
	if s.alertsIssued >= consensusMaxAlerts {
		return ""
	}

	if s.currentStep-s.lastAlertStep < consensusCooldownSteps {
		return ""
	}

	windowStart := s.currentStep - consensusWindowSteps
	seen := make(map[string]bool)
	var recentDetectors []string
	for _, f := range s.firings {
		if f.step > windowStart {
			if !seen[f.detector] {
				seen[f.detector] = true
				recentDetectors = append(recentDetectors, f.detector)
			}
		}
	}

	if len(seen) < consensusFireThreshold {
		return ""
	}

	s.alertsIssued++
	s.lastAlertStep = s.currentStep

	detectorList := strings.Join(recentDetectors, ", ")

	var guidance string
	firstAlert := "[Systemic Failure Detected / Cross-Detector Consensus] " +
		fmt.Sprintf("%d independent behavioral detectors fired within the last %d tool calls (%s). ",
			len(seen), consensusWindowSteps, detectorList) +
		"Research on metacognitive monitoring (Nelson-Narens 1990; MAPE-K Loop) shows that " +
		"simultaneous activation of multiple independent monitors is the strongest signal of " +
		"systemic failure -- far more diagnostic than any single detector. " +
		"You are not experiencing isolated issues; you are in a compounding breakdown. " +
		"STOP incremental patching. Step back and reassess: (1) Are you solving the RIGHT problem? " +
		"(2) Is your mental model of the codebase correct? (3) Should you try a fundamentally " +
		"different approach instead of iterating on the current one?"
	repeatAlert := "[Systemic Failure / Cross-Detector Consensus -- RECURRING] " +
		fmt.Sprintf("%d detectors still firing in concert (%s). ", len(seen), detectorList) +
		"The systemic breakdown persists despite prior warnings. " +
		"This strongly suggests your current approach is fundamentally wrong. " +
		"Consider: abandoning the current approach entirely, re-reading the original task " +
		"requirements, or asking for clarification rather than continuing to patch."
	if s.alertsIssued == 1 {
		guidance = firstAlert
	} else {
		guidance = repeatAlert
	}

	return guidance
}

func (s *consensusState) checkOnly() string {
	if s == nil {
		return ""
	}
	return s.check()
}
