package agent

// Metacognitive State Monitor
//
// Research basis:
//   - "AI Awareness" (Li et al., Tsinghua, 2025): Meta-cognition has three components:
//     self-monitoring, self-reflection/probing, engagement in controlling cognitive processes
//   - "Fast, slow, and metacognitive thinking in AI" (Nature, 2025, SOFAI architecture):
//     Metacognitive module monitors processing in real-time, decides which solver to use
//   - "Metacognitive Uncertainty and What It Demands of Artificial Consciousness"
//     (Megan Peters, 2026): Distinguishes calibration (confidence matching accuracy) from
//     metacognitive access (higher-order representation from live dynamics)
//
// Problem: ggcode has UProp (trajectory uncertainty propagation) and confidence scoring,
// but these don't track the agent's own cognitive state stability. The system can be
// "confident" in a result while being internally unstable - changing its mind frequently,
// generating contradictory reasoning paths, or operating under uncertain interpretive frames.
//
// Gap: No higher-order monitoring of the agent's own cognitive processes:
//   - UProp: tracks error propagation through trajectory (how uncertain are the results?)
//   - Missing: tracks cognitive state stability (how certain is the agent in its own thinking?)
//
// Design:
//   - Maintains a second-order confidence metric (confidence in confidence)
//   - Tracks reasoning consistency and detect self-contradiction
//   - Monitors cognitive state transitions (plan changes, re-interpretations)
//   - Provides metacognitive guidance when the agent's own thinking is unstable
//   - Zero LLM cost - deterministic heuristics from observable patterns
//   - Complements UProp (trajectory uncertainty) by adding state uncertainty

import (
	"fmt"
	"strings"
	"sync"
)

const (
	// metaMinTurns: minimum turns before metacognitive analysis
	metaMinTurns = 3

	// metaConsistencyThreshold: when consistency drops below this, cognitive state is unstable
	metaConsistencyThreshold = 0.5

	// metaHighInstabilityThreshold: when instability exceeds this, critical guidance
	metaHighInstabilityThreshold = 0.7

	// metaPlanChangePenalty: penalty for significant plan changes
	metaPlanChangePenalty = 0.3

	// metaContradictionPenalty: penalty for detected self-contradiction
	metaContradictionPenalty = 0.5
)

// cognitiveTurnSnapshot captures key aspects of the agent's cognitive state at a turn.
type cognitiveTurnSnapshot struct {
	turnIndex        int    // which turn this is
	toolsUsed        string // concatenated tool names
	actionSummary    string // brief summary of what the agent decided to do
	interpretation   string // key interpretation or assumption made
	hasContradiction bool   // whether this turn contradicts previous
}

// metacognitiveMonitor tracks higher-order cognitive state dynamics.
type metacognitiveMonitor struct {
	mu sync.Mutex

	turns            []cognitiveTurnSnapshot // history of cognitive states
	consistencyScore float64                 // running average of cognitive consistency
	instabilityIndex float64                 // how unstable the cognitive state has been

	guidanceGiven bool // fire at most once per run
}

func newMetacognitiveMonitor() *metacognitiveMonitor {
	return &metacognitiveMonitor{
		turns:            make([]cognitiveTurnSnapshot, 0),
		consistencyScore: 1.0, // starts at perfectly consistent
	}
}

func (m *metacognitiveMonitor) reset() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.turns = m.turns[:0]
	m.consistencyScore = 1.0
	m.instabilityIndex = 0
	m.guidanceGiven = false
}

// recordTurn captures the agent's cognitive state at this turn.
//
// toolsUsed: comma-separated list of tool names called this turn
// actionSummary: brief description of what the agent decided to do
// interpretation: key interpretation or assumption that guided the action
func (m *metacognitiveMonitor) recordTurn(toolsUsed, actionSummary, interpretation string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	turn := len(m.turns)

	snapshot := cognitiveTurnSnapshot{
		turnIndex:        turn,
		toolsUsed:        toolsUsed,
		actionSummary:    actionSummary,
		interpretation:   interpretation,
		hasContradiction: false,
	}

	// Check for contradictions with previous turns
	if turn > 0 {
		if m.detectsContradiction(snapshot, m.turns[turn-1]) {
			snapshot.hasContradiction = true
			m.consistencyScore *= (1.0 - metaContradictionPenalty)
			m.instabilityIndex += metaContradictionPenalty
		}
	}

	m.turns = append(m.turns, snapshot)
}

// detectsContradiction checks if the current turn contradicts the previous turn.
func (m *metacognitiveMonitor) detectsContradiction(current, previous cognitiveTurnSnapshot) bool {
	// Check for opposite actions on same target
	if isMetaOppositeAction(current.actionSummary, previous.actionSummary) {
		currentTarget := metaExtractTarget(current.actionSummary)
		previousTarget := metaExtractTarget(previous.actionSummary)
		if currentTarget != "" && currentTarget == previousTarget {
			return true
		}
	}

	// Check for opposite interpretations of similar patterns
	if hasMetaOppositeInterpretation(current.interpretation, previous.interpretation) {
		return true
	}

	return false
}

// evaluateConsistency analyzes the cognitive state history.
func (m *metacognitiveMonitor) evaluateConsistency() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.turns) < 2 {
		return
	}

	actionCoherence := m.calculateActionCoherence()
	interpretationStability := m.calculateInterpretationStability()

	m.consistencyScore = 0.6*actionCoherence + 0.4*interpretationStability
	m.instabilityIndex = 1.0 - m.consistencyScore
}

// calculateActionCoherence measures how coherent the agent's actions have been.
func (m *metacognitiveMonitor) calculateActionCoherence() float64 {
	if len(m.turns) < 2 {
		return 1.0
	}

	planChanges := 0
	for i := 1; i < len(m.turns); i++ {
		if isMetaPlanChange(m.turns[i-1].actionSummary, m.turns[i].actionSummary) {
			planChanges++
		}
	}

	penalty := float64(planChanges) * metaPlanChangePenalty
	if penalty > 1.0 {
		penalty = 1.0
	}

	return 1.0 - penalty
}

// calculateInterpretationStability measures how stable the agent's interpretations have been.
func (m *metacognitiveMonitor) calculateInterpretationStability() float64 {
	if len(m.turns) < 2 {
		return 1.0
	}

	patternGroups := make(map[string]int)
	for _, t := range m.turns {
		if t.interpretation == "" {
			continue
		}
		pattern := metaExtractInterpretationPattern(t.interpretation)
		patternGroups[pattern]++
	}

	if len(patternGroups) == 0 {
		return 1.0
	}

	maxGroupSize := 0
	for _, count := range patternGroups {
		if count > maxGroupSize {
			maxGroupSize = count
		}
	}

	return float64(maxGroupSize) / float64(len(m.turns))
}

// maybeIntervene provides metacognitive guidance when cognitive state is unstable.
func (m *metacognitiveMonitor) maybeIntervene() string {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.guidanceGiven {
		return ""
	}
	if len(m.turns) < metaMinTurns {
		return ""
	}

	m.evaluateConsistency()

	if m.consistencyScore >= metaConsistencyThreshold {
		return ""
	}

	var guidance strings.Builder
	var warnings []string
	var criticalIssue bool

	if m.consistencyScore < (1.0 - metaHighInstabilityThreshold) {
		criticalIssue = true
		warnings = append(warnings,
			fmt.Sprintf("cognitive stability is critically low (%.0f%%)",
				m.consistencyScore*100))
	} else {
		warnings = append(warnings,
			fmt.Sprintf("cognitive stability is degraded (%.0f%%)",
				m.consistencyScore*100))
	}

	contradictionCount := 0
	for _, t := range m.turns {
		if t.hasContradiction {
			contradictionCount++
		}
	}
	if contradictionCount > 0 {
		warnings = append(warnings,
			fmt.Sprintf("detected %d self-contradiction(s) in reasoning",
				contradictionCount))
	}

	m.guidanceGiven = true

	guidance.WriteString("[metacognitive monitor] ")

	if criticalIssue {
		guidance.WriteString("CRITICAL: ")
	}

	guidance.WriteString(strings.Join(warnings, ". "))
	guidance.WriteString(".\n\n")

	if contradictionCount > 0 {
		guidance.WriteString("Analysis: Your reasoning shows internal contradictions. " +
			"This suggests uncertainty in your own interpretive framework. " +
			"You may be operating under conflicting assumptions about the codebase or task.\n\n")
		guidance.WriteString("Action: Step back and explicitly state your current understanding. " +
			"Before proceeding, verify the contradictory interpretations by re-reading " +
			"the relevant code or documentation. Choose one interpretation and commit to it, " +
			"or acknowledge the ambiguity and ask for clarification.\n")
	} else if m.instabilityIndex > metaHighInstabilityThreshold {
		guidance.WriteString("Analysis: Your cognitive state is highly unstable - you're changing " +
			"your approach frequently. This often indicates you're reacting to surface-level " +
			"symptoms rather than addressing the root cause.\n\n")
		guidance.WriteString("Action: Pause and reflect on the underlying problem. " +
			"Define a clear hypothesis about what's wrong, then design a targeted test " +
			"to verify it. Avoid reactive toggling between different approaches.\n")
	} else {
		guidance.WriteString("Analysis: Your reasoning shows some inconsistency. " +
			"This may indicate uncertainty about the right direction.\n\n")
		guidance.WriteString("Action: Make your current assumptions explicit. " +
			"State clearly what you believe to be true and why. " +
			"This makes it easier to detect errors later if your assumptions prove wrong.\n")
	}

	if len(m.turns) >= 2 {
		last := m.turns[len(m.turns)-1]
		guidance.WriteString(fmt.Sprintf("\nRecent cognitive state: %s (%s)",
			last.actionSummary, last.interpretation))
	}

	return guidance.String()
}

// isMetaOppositeAction checks if two action summaries represent opposite operations.
func isMetaOppositeAction(a, b string) bool {
	opposites := map[string]string{
		"add":     "delete",
		"delete":  "add",
		"create":  "remove",
		"remove":  "create",
		"enable":  "disable",
		"disable": "enable",
		"open":    "close",
		"close":   "open",
		"start":   "stop",
		"stop":    "start",
	}

	for k, v := range opposites {
		if strings.Contains(a, k) && strings.Contains(b, v) {
			return true
		}
		if strings.Contains(b, k) && strings.Contains(a, v) {
			return true
		}
	}

	return false
}

// hasMetaOppositeInterpretation checks if two interpretations represent opposite views.
func hasMetaOppositeInterpretation(a, b string) bool {
	if a == "" || b == "" {
		return false
	}

	opposites := map[string]string{
		"correct":     "incorrect",
		"incorrect":   "correct",
		"works":       "doesn't work",
		"broken":      "working",
		"necessary":   "unnecessary",
		"unnecessary": "necessary",
		"safe":        "unsafe",
		"unsafe":      "safe",
		"error":       "no error",
		"problem":     "no problem",
	}

	for k, v := range opposites {
		if strings.Contains(a, k) && strings.Contains(b, v) {
			return true
		}
		if strings.Contains(b, k) && strings.Contains(a, v) {
			return true
		}
	}

	return false
}

// isMetaPlanChange checks if two action summaries represent different plans.
func isMetaPlanChange(a, b string) bool {
	aType := metaExtractActionType(a)
	bType := metaExtractActionType(b)

	if aType != bType {
		return true
	}

	if aType != "" {
		aTarget := metaExtractTarget(a)
		bTarget := metaExtractTarget(b)
		if aTarget != "" && bTarget != "" && aTarget != bTarget {
			return !metaAreRelatedTargets(aTarget, bTarget)
		}
	}

	return false
}

// metaExtractActionType extracts the high-level action type from a summary.
func metaExtractActionType(s string) string {
	types := []string{"read", "write", "edit", "search", "grep", "test", "build", "run",
		"list", "delete", "create", "remove", "add", "git"}

	for _, t := range types {
		if strings.HasPrefix(strings.ToLower(s), t) {
			return t
		}
	}

	return ""
}

// metaExtractTarget extracts the target from a summary.
func metaExtractTarget(s string) string {
	parts := strings.Fields(s)
	for i, p := range parts {
		if p == "file" || p == "pattern" || p == "directory" || p == "package" {
			if i+1 < len(parts) {
				return parts[i+1]
			}
		}
	}
	return ""
}

// metaAreRelatedTargets checks if two targets are likely related.
func metaAreRelatedTargets(a, b string) bool {
	if strings.HasPrefix(a, b) || strings.HasPrefix(b, a) {
		return true
	}

	aPkg := metaExtractPackageFromTarget(a)
	bPkg := metaExtractPackageFromTarget(b)
	return aPkg != "" && aPkg == bPkg
}

// metaExtractPackageFromTarget extracts package name from a file path.
func metaExtractPackageFromTarget(s string) string {
	if idx := strings.Index(s, "internal/"); idx >= 0 {
		rest := s[idx+9:]
		if slash := strings.Index(rest, "/"); slash > 0 {
			return rest[:slash]
		}
		return rest
	}
	return ""
}

// metaExtractInterpretationPattern extracts a key pattern from an interpretation.
func metaExtractInterpretationPattern(s string) string {
	s = strings.ToLower(s)

	patterns := []string{
		"permission", "auth", "error", "bug", "typo", "logic",
		"missing", "wrong", "incorrect", "correct", "works", "broken",
		"refactor", "optimization", "performance", "security",
	}

	for _, p := range patterns {
		if strings.Contains(s, p) {
			return p
		}
	}

	words := strings.Fields(s)
	if len(words) > 2 {
		return strings.Join(words[:2], " ")
	}
	if len(words) > 0 {
		return words[0]
	}

	return ""
}
