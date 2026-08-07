package agent

// consensus.go -- Cross-Detector Consensus (Systemic Failure Detection)
//
// Research basis:
//   - Zilos AI "MetaCognition Patterns for AI Agent Self-Monitoring and Adaptive
//     Control" (2026-03-14): Section 5.1 "Cross-stream anomalies (most diagnostic)":
//     "anomalies that appear in both streams simultaneously are high-confidence
//     systemic failures; anomalies in only one stream narrow the locus of the problem."
//   - Nelson & Narens (1990) metacognitive framework: the meta-level must synthesize
//     signals from multiple object-level monitors.
//   - MAPE-K Loop (Kephart & Chess, 2003): the Analyze phase must correlate symptoms
//     across monitored resources before issuing control recommendations.
//
// The gap this addresses: ggcode has 30+ independent behavioral detectors (error_rush,
// correction_spiral, strategy_fixation, bare_edit_streak, etc.), each firing its own
// guidance independently. When multiple detectors fire within a narrow temporal window,
// that simultaneous activation is the STRONGEST signal of systemic failure -- far more
// diagnostic than any single detector. But no existing component synthesizes this
// cross-detector signal.
//
// The pattern: if N detectors fire within W tool-call steps, the agent is not experiencing
// isolated incidents — it is in a systemic breakdown. The consensus detector injects a
// high-priority "step back" message that is stronger and more actionable than individual
// detector warnings.
//
// Distinct from:
//   - error_compound: tracks error probability across steps (single signal type)
//   - trajectory_health: synthesizes post-hoc quality metrics (not real-time consensus)
//   - failure_mode: classifies a single failure's category (no cross-detector correlation)
//   - THIS detector: correlates TEMPORAL CO-OCCURRENCE of multiple independent detectors

import (
	"fmt"
	"strings"
	"time"
)

const (
	// consensusWindowSteps is how many recent tool-call steps to consider for
	// cross-detector consensus (short-term temporal correlation).
	consensusWindowSteps = 5
	// consensusFireThreshold is how many distinct detectors must fire within the
	// window to trigger a systemic alert.
	consensusFireThreshold = 3
	// consensusMaxAlerts caps total consensus alerts per run to avoid nagging.
	consensusMaxAlerts = 2
	// consensusCooldownSteps prevents re-firing within this many steps of an alert.
	consensusCooldownSteps = 8
)

// consensusState tracks temporal co-occurrence of detector firings.
type consensusState struct {
	// firings is a ring buffer of recent detector firing events.
	firings []consensusFiring
	// alertsIssued counts total consensus alerts in this run.
	alertsIssued int
	// lastAlertStep is the tool-call step index when we last alerted (for cooldown).
	lastAlertStep int
	// currentStep is incremented each time recordFiring is called.
	currentStep int
}

// consensusFiring records a single detector firing event.
type consensusFiring struct {
	detector string
	step     int
	time     time.Time
}

func newConsensusState() *consensusState {
	return &consensusState{
		firings:       make([]consensusFiring, 0, 32),
		lastAlertStep: -consensusCooldownSteps, // allow first alert
	}
}

func (s *consensusState) reset() {
	s.firings = s.firings[:0]
	s.alertsIssued = 0
	s.lastAlertStep = -consensusCooldownSteps
	s.currentStep = 0
}

// recordFiring logs that a named detector produced guidance.
// This should be called whenever ANY detector's check() returns non-empty.
func (s *consensusState) recordFiring(detectorName string) {
	if s == nil || detectorName == "" {
		return
	}
	s.currentStep++
	s.firings = append(s.firings, consensusFiring{
		detector: detectorName,
		step:     s.currentStep,
		time:     time.Now(),
	})
	// Trim old entries beyond 2x window to keep memory bounded
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

// check returns a consensus alert if multiple distinct detectors fired within
// the recent window, indicating systemic failure. Returns empty string otherwise.
func (s *consensusState) check() string {
	if s == nil {
		return ""
	}
	if s.alertsIssued >= consensusMaxAlerts {
		return ""
	}

	// Cooldown: don't fire too soon after last alert
	if s.currentStep-s.lastAlertStep < consensusCooldownSteps {
		return ""
	}

	// Count distinct detectors within the window
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

// scanAndCheck scans tool result content for detector tag signatures (e.g.
// "[Error Rush", "[Strategy Fixation"), records each detected firing, then
// checks for cross-detector consensus. Returns consensus guidance if triggered.
// This avoids modifying 30+ individual .check() call sites in the agent loop.
func (s *consensusState) scanAndCheck(content string) string {
	if s == nil {
		return ""
	}
	// Known detector tag prefixes that appear in bracketed guidance headers.
	detectorTags := []string{
		"[Error Rush",
		"[Strategy Fixation",
		"[Correction Spiral",
		"[Error Strategy Loop",
		"[Hardcoded Output",
		"[Bare Edit Streak",
		"[Fix Cascade",
		"[Recurring Error",
		"[Stalled Convergence",
		"[Convergence Lock",
		"[Tunnel Vision",
		"[Analysis Paralysis",
		"[Tool Call Economy",
		"[Context Footprint",
		"[Empty Search Spiral",
		"[Error Regression",
		"[Circular Reasoning",
		"[Contradiction Detected",
		"[Ungrounded Reflection",
		"[Action Hedging",
		"[Scope Creep",
		"[Premature Abstraction",
		"[Plan Abandonment",
		"[Tool Target Mismatch",
		"[Premature Commitment",
		"[Diagnostic Disconnect",
		"[Failure Mode",
		"[Tool Fallback",
		"[Error Cascade",
		"[File Freshness",
		"[Tool Thermal",
		"[Cache Efficiency",
		"[Pressure Forecaster",
		"[Prompt Ops",
		"[Change Reconcile",
		"[Error Compound",
		"[Temporal Blindness",
		"[Deferred Work",
		"[Expired Read",
		"[Action Annihilate",
		"[Assumption Track",
		"[Futile Cycle",
	}
	for _, tag := range detectorTags {
		if strings.Contains(content, tag) {
			s.recordFiring(strings.TrimPrefix(tag, "["))
		}
	}
	return s.check()
}
