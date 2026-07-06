package agent

// Trajectory Confidence Scorer — HTC-inspired (Holistic Trajectory Calibration)
//
// Research: Zhang et al., "Agentic Confidence Calibration" (arXiv:2601.15778, Jan 2026)
// Key insight: agents exhibit "overconfidence in failure" — they continue executing
// confidently even when early decisions have doomed the trajectory. HTC extracts
// "process-level features ranging from macro dynamics to micro stability" to detect
// this pattern early.
//
// This implementation uses deterministic heuristics (no ML model) to compute a
// holistic trajectory confidence score from multiple signals:
//
//   MACRO DYNAMICS (trajectory-level):
//   - Tool diversity: using many different tools suggests exploration, not stuck
//   - File diversity: touching many files suggests progress, not spinning on one
//   - Trajectory length: diminishing returns after many iterations
//
//   MICRO STABILITY (per-step):
//   - Overall success rate: fraction of tool calls that succeed
//   - Edit success rate: fraction of file edits that succeed
//   - Recent momentum: last-5-call success rate vs overall
//   - Error concentration: clustered errors are worse than spread-out ones
//
// The score ranges 0-100. When it drops below a threshold early in the trajectory
// (before error-streak's 4-error threshold fires), we inject a "reconsider" message
// to prevent compounding errors — the core problem HTC identifies.

import (
	"fmt"
	"strings"
)

const (
	// confidenceWarnThreshold: below this score, inject early warning.
	// Set to 30 — meaningfully low to avoid false positives.
	confidenceWarnThreshold = 30

	// confidenceMinCalls: need at least this many tool calls before scoring
	// to avoid noise from the first few calls.
	confidenceMinCalls = 5

	// momentumWindowSize: number of recent calls for momentum calculation.
	momentumWindowSize = 5
)

// confidenceState tracks holistic trajectory quality signals.
type confidenceState struct {
	totalCalls   int
	successCount int
	failureCount int

	editAttempts int
	editSuccess  int

	uniqueTools map[string]bool
	uniqueFiles map[string]bool

	// Sliding window for momentum (recent success rate)
	recentResults []bool // true = success, false = failure

	// Error clustering: track if errors are consecutive
	consecutiveErrors int
	maxErrorCluster   int
	lastWasError      bool

	// Track if we've already fired (avoid duplicate interventions)
	guidanceGiven bool
}

func newConfidenceState() *confidenceState {
	return &confidenceState{
		uniqueTools: make(map[string]bool),
		uniqueFiles: make(map[string]bool),
	}
}

func (c *confidenceState) reset() {
	c.totalCalls = 0
	c.successCount = 0
	c.failureCount = 0
	c.editAttempts = 0
	c.editSuccess = 0
	c.uniqueTools = make(map[string]bool)
	c.uniqueFiles = make(map[string]bool)
	c.recentResults = c.recentResults[:0]
	c.consecutiveErrors = 0
	c.maxErrorCluster = 0
	c.lastWasError = false
	c.guidanceGiven = false
}

// recordResult feeds a tool call outcome into the confidence tracker.
// toolName: name of the tool called.
// isError: whether the tool returned an error.
// fileHint: file path if the tool operated on a file (empty otherwise).
func (c *confidenceState) recordResult(toolName string, isError bool, fileHint string) {
	c.totalCalls++

	if isError {
		c.failureCount++
		c.consecutiveErrors++
		if c.consecutiveErrors > c.maxErrorCluster {
			c.maxErrorCluster = c.consecutiveErrors
		}
		c.lastWasError = true
	} else {
		c.successCount++
		c.consecutiveErrors = 0
		c.lastWasError = false
	}

	c.uniqueTools[toolName] = true

	if fileHint != "" {
		c.uniqueFiles[fileHint] = true
	}

	// Track edit-specific success rate
	if isEditTool(toolName) {
		c.editAttempts++
		if !isError {
			c.editSuccess++
		}
	}

	// Maintain sliding window for momentum
	c.recentResults = append(c.recentResults, !isError)
	if len(c.recentResults) > momentumWindowSize {
		c.recentResults = c.recentResults[1:]
	}
}

// score computes the holistic trajectory confidence (0-100).
// Returns -1 if insufficient data (< confidenceMinCalls).
func (c *confidenceState) score() int {
	if c.totalCalls < confidenceMinCalls {
		return -1 // insufficient data
	}

	// Base: overall success rate (0-100)
	overallRate := float64(c.successCount) / float64(c.totalCalls) * 100

	// Momentum: recent success rate
	recentRate := overallRate // default to overall if window not full
	if len(c.recentResults) > 0 {
		recentSuccess := 0
		for _, ok := range c.recentResults {
			if ok {
				recentSuccess++
			}
		}
		recentRate = float64(recentSuccess) / float64(len(c.recentResults)) * 100
	}

	// Tool diversity bonus: using many different tools suggests healthy exploration.
	// Normalize: 1-3 tools = no bonus, 4-6 = +5, 7+ = +10
	toolDiv := len(c.uniqueTools)
	divBonus := 0
	switch {
	case toolDiv >= 7:
		divBonus = 10
	case toolDiv >= 4:
		divBonus = 5
	}

	// Error clustering penalty: consecutive errors are worse than spread ones.
	// Each error in the max cluster beyond 2 subtracts 3 points.
	clusterPenalty := 0
	if c.maxErrorCluster > 2 {
		clusterPenalty = (c.maxErrorCluster - 2) * 3
	}

	// Edit success rate: if edits are failing, trajectory is likely bad.
	editPenalty := 0
	if c.editAttempts >= 3 {
		editFailRate := 1.0 - float64(c.editSuccess)/float64(c.editAttempts)
		if editFailRate > 0.5 {
			// More than half of edits failing — significant penalty
			editPenalty = int(editFailRate * 20)
		}
	}

	// Momentum divergence: if recent is much worse than overall, trajectory
	// is deteriorating. If recent is much better, trajectory is recovering.
	momentumDiv := recentRate - overallRate
	momentumAdj := int(momentumDiv * 0.3) // dampened

	// Compute final score
	score := int(overallRate) + divBonus - clusterPenalty - editPenalty + momentumAdj

	// Clamp to 0-100
	if score < 0 {
		score = 0
	}
	if score > 100 {
		score = 100
	}

	return score
}

// maybeIntervene returns guidance text if trajectory confidence is critically low.
// Fires at most once per run (guidanceGiven flag).
// Only fires after confidenceMinCalls but before error-streak (4 errors) would fire,
// to provide early warning of a bad trajectory.
func (c *confidenceState) maybeIntervene() string {
	if c.guidanceGiven {
		return ""
	}
	if c.totalCalls < confidenceMinCalls {
		return ""
	}

	s := c.score()
	if s >= confidenceWarnThreshold {
		return ""
	}

	// Don't fire if we're already deep in the run (let error-streak/overseer handle it)
	if c.maxErrorCluster >= 4 {
		return "" // error-streak will handle this
	}

	c.guidanceGiven = true

	var reasons []string

	// Diagnose what's wrong
	if c.editAttempts >= 3 {
		editFailRate := 1.0 - float64(c.editSuccess)/float64(c.editAttempts)
		if editFailRate > 0.5 {
			reasons = append(reasons, fmt.Sprintf("%.0f%% of file edits are failing", editFailRate*100))
		}
	}

	if c.maxErrorCluster >= 3 {
		reasons = append(reasons, fmt.Sprintf("%d consecutive errors detected", c.maxErrorCluster))
	}

	if len(c.uniqueTools) <= 2 && c.totalCalls >= 5 {
		reasons = append(reasons, "very low tool diversity — consider a different approach")
	}

	successRate := float64(c.successCount) / float64(c.totalCalls) * 100
	if successRate < 40 {
		reasons = append(reasons, fmt.Sprintf("overall success rate is only %.0f%%", successRate))
	}

	reasonStr := "multiple indicators suggest this approach may not be working"
	if len(reasons) > 0 {
		reasonStr = strings.Join(reasons, "; ")
	}

	return fmt.Sprintf(
		"[trajectory confidence: %d/100] %s. Consider stepping back: re-read the task requirements, "+
			"verify your assumptions about the codebase, and try a fundamentally different approach "+
			"before continuing. Early course correction prevents compounding errors.",
		s, reasonStr,
	)
}

// isEditTool returns true for tools that modify files.
func isEditTool(toolName string) bool {
	switch toolName {
	case "edit_file", "write_file", "multi_edit_file", "multi_file_edit", "notebook_edit":
		return true
	default:
		return false
	}
}
