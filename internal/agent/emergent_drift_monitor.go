package agent

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// emergentDriftMonitor tracks patterns in multi-agent coordination to detect
// emergent behavior drift (e.g., social convention shifts, collusion formation,
// cascading failure patterns).
//
// Research basis:
//   - "Emergent Behavior in Large-Scale Multi-Agent Systems" (2026): Emergent
//     behaviors evolve over time as agents accumulate interaction history.
//   - "Emergent Coordination in Multi-Agent Language Models" (ICLR 2026):
//     Multi-agent systems require continuous monitoring for drift in coordination
//     patterns.
type emergentDriftMonitor struct {
	mu sync.Mutex

	// interactionHistory stores recent agent coordination events
	interactionHistory []agentInteraction

	// patternCounts tracks frequency of recurring coordination patterns
	patternCounts map[string]int

	// lastBaselineSnapshot captures the "normal" coordination state
	lastBaselineSnapshot *coordinationBaseline

	// driftThreshold triggers warning when pattern deviation exceeds this
	driftThreshold float64

	// maxHistory limits memory usage
	maxHistory int
}

// agentInteraction represents a single multi-agent coordination event
type agentInteraction struct {
	timestamp   time.Time
	agentID     string
	targetAgent string
	actionType  string // "message", "task_delegation", "resource_claim"
	patternKey  string // normalized key for pattern matching
	success     bool
}

// coordinationBaseline captures the statistical baseline of coordination patterns
type coordinationBaseline struct {
	capturedAt      time.Time
	patternFreq     map[string]float64
	avgResponseTime time.Duration
	successRate     float64
}

const (
	// defaultDriftThreshold is the threshold for drift detection (20% deviation)
	defaultDriftThreshold = 0.20

	// defaultMaxHistory keeps last 1000 interactions for drift analysis
	defaultMaxHistory = 1000

	// baselineRecalcInterval recalculates baseline every 200 interactions
	baselineRecalcInterval = 200
)

// newEmergentDriftMonitor creates a new drift monitor
func newEmergentDriftMonitor() *emergentDriftMonitor {
	return &emergentDriftMonitor{
		interactionHistory: make([]agentInteraction, 0),
		patternCounts:      make(map[string]int),
		driftThreshold:     defaultDriftThreshold,
		maxHistory:         defaultMaxHistory,
	}
}

// recordInteraction logs an agent coordination event for drift analysis
func (m *emergentDriftMonitor) recordInteraction(agentID, targetAgent, actionType string, success bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	patternKey := m.normalizePatternKey(agentID, targetAgent, actionType)

	evt := agentInteraction{
		timestamp:   time.Now(),
		agentID:     agentID,
		targetAgent: targetAgent,
		actionType:  actionType,
		patternKey:  patternKey,
		success:     success,
	}

	m.interactionHistory = append(m.interactionHistory, evt)
	m.patternCounts[patternKey]++

	// Prune history to prevent unbounded growth
	if len(m.interactionHistory) > m.maxHistory {
		keepStart := len(m.interactionHistory) - m.maxHistory
		m.interactionHistory = m.interactionHistory[keepStart:]

		// Recompute pattern counts from pruned history
		m.recomputePatternCounts()
	}

	// Recalculate baseline periodically
	if len(m.interactionHistory)%baselineRecalcInterval == 0 {
		m.recalculateBaseline()
	}
}

// checkDrift analyzes recent coordination patterns against baseline
func (m *emergentDriftMonitor) checkDrift(_ context.Context) (driftDetected bool, reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.lastBaselineSnapshot == nil || len(m.interactionHistory) < baselineRecalcInterval {
		return false, "insufficient data for drift detection"
	}

	// Check for pattern frequency drift
	recentPatterns := m.extractRecentPatternFreq(baselineRecalcInterval)
	driftScore := m.computeDriftScore(m.lastBaselineSnapshot.patternFreq, recentPatterns)

	if driftScore > m.driftThreshold {
		return true, formatDriftWarning(driftScore, "pattern frequency", recentPatterns)
	}

	// Check for success rate drift
	recentSuccessRate := m.computeRecentSuccessRate(baselineRecalcInterval)
	successRateDrift := absFloat64(recentSuccessRate - m.lastBaselineSnapshot.successRate)

	if successRateDrift > m.driftThreshold && m.lastBaselineSnapshot.successRate > 0.5 {
		// Only alert if baseline success rate was good (>50%)
		return true, formatDriftWarning(successRateDrift, "coordination success rate",
			map[string]float64{"recent": recentSuccessRate, "baseline": m.lastBaselineSnapshot.successRate})
	}

	return false, ""
}

// normalizePatternKey creates a normalized key for pattern matching
func (m *emergentDriftMonitor) normalizePatternKey(agentID, targetAgent, actionType string) string {
	// Normalize to direction-agnostic pattern for detecting emergent conventions
	// e.g., "A->B:message" and "B->A:message" both become "<->:message"
	if actionType == "message" || actionType == "task_delegation" {
		// Sort agent IDs to create symmetric key
		agents := []string{agentID, targetAgent}
		if agents[0] > agents[1] {
			agents[0], agents[1] = agents[1], agents[0]
		}
		return agents[0] + "<->" + agents[1] + ":" + actionType
	}
	return agentID + "->" + targetAgent + ":" + actionType
}

// recomputePatternCounts rebuilds pattern counts from current history
func (m *emergentDriftMonitor) recomputePatternCounts() {
	newCounts := make(map[string]int)
	for _, evt := range m.interactionHistory {
		newCounts[evt.patternKey]++
	}
	m.patternCounts = newCounts
}

// recalculateBaseline captures the current coordination state as baseline
func (m *emergentDriftMonitor) recalculateBaseline() {
	sampleSize := intMin(len(m.interactionHistory), baselineRecalcInterval)

	patternFreq := m.extractRecentPatternFreq(sampleSize)
	successRate := m.computeRecentSuccessRate(sampleSize)

	m.lastBaselineSnapshot = &coordinationBaseline{
		capturedAt:  time.Now(),
		patternFreq: patternFreq,
		successRate: successRate,
	}
}

// extractRecentPatternFreq computes pattern frequencies from recent interactions
func (m *emergentDriftMonitor) extractRecentPatternFreq(sampleSize int) map[string]float64 {
	sampleStart := len(m.interactionHistory) - sampleSize
	sample := m.interactionHistory[sampleStart:]

	freq := make(map[string]float64)
	for _, evt := range sample {
		freq[evt.patternKey]++
	}

	// Normalize to frequencies
	for key := range freq {
		freq[key] = freq[key] / float64(sampleSize)
	}

	return freq
}

// computeDriftScore calculates the total variation distance between pattern distributions
func (m *emergentDriftMonitor) computeDriftScore(baseline, recent map[string]float64) float64 {
	// Total variation distance: 0.5 * sum(|p_i - q_i|)
	totalVariation := 0.0

	// All unique keys
	allKeys := make(map[string]bool)
	for key := range baseline {
		allKeys[key] = true
	}
	for key := range recent {
		allKeys[key] = true
	}

	for key := range allKeys {
		p := baseline[key]
		q := recent[key]
		totalVariation += absFloat64(p - q)
	}

	return 0.5 * totalVariation
}

// computeRecentSuccessRate calculates success rate from recent interactions
func (m *emergentDriftMonitor) computeRecentSuccessRate(sampleSize int) float64 {
	sampleStart := len(m.interactionHistory) - sampleSize
	sample := m.interactionHistory[sampleStart:]

	successCount := 0
	for _, evt := range sample {
		if evt.success {
			successCount++
		}
	}

	return float64(successCount) / float64(sampleSize)
}

// formatDriftWarning creates a human-readable drift warning
func formatDriftWarning(driftScore float64, metric string, details map[string]float64) string {
	detailStr := ""
	for k, v := range details {
		detailStr += fmt.Sprintf(", %s=%.2f", k, v)
	}
	return fmt.Sprintf("Emergent behavior drift detected: %s drift=%.2f (threshold=%.2f)%s",
		metric, driftScore, defaultDriftThreshold, detailStr)
}

func absFloat64(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

// intMin returns the smaller of two integers (renamed to avoid conflicts)
func intMin(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// Global monitor instance (singleton pattern for simplicity)
var globalEmergentDriftMonitor = newEmergentDriftMonitor()

// RecordAgentInteraction logs an agent coordination event for drift analysis.
// It tracks multi-agent interactions like messaging, task delegation, and resource
// claims to detect emergent behavior patterns over time.
func RecordAgentInteraction(agentID, targetAgent, actionType string, success bool) {
	globalEmergentDriftMonitor.recordInteraction(agentID, targetAgent, actionType, success)
}

// CheckEmergentDrift analyzes recent coordination patterns against baseline
// and returns true if significant drift is detected (e.g., social convention shifts,
// collusion formation, cascading failure patterns).
func CheckEmergentDrift(ctx context.Context) (bool, string) {
	return globalEmergentDriftMonitor.checkDrift(ctx)
}
