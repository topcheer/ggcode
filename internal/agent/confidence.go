package agent

import (
	"fmt"
	"strings"
)

const (
	confidenceWarnThreshold = 30

	confidenceMinCalls = 5

	momentumWindowSize = 5
)

type confidenceState struct {
	totalCalls   int
	successCount int
	failureCount int

	editAttempts int
	editSuccess  int

	uniqueTools map[string]bool
	uniqueFiles map[string]bool

	recentResults []bool

	consecutiveErrors int
	maxErrorCluster   int
	lastWasError      bool

	// lastLogprob: language-model turn confidence (avg token logprob) fed
	// in from the stream done event; previously a debug-only orphan
	// (#uncertainty-loop P1). nil = provider did not report.
	lastLogprob *float64

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
	c.lastLogprob = nil
	c.guidanceGiven = false
}

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

	if isEditTool(toolName) {
		c.editAttempts++
		if !isError {
			c.editSuccess++
		}
	}

	c.recentResults = append(c.recentResults, !isError)
	if len(c.recentResults) > momentumWindowSize {
		c.recentResults = c.recentResults[1:]
	}
}

func (c *confidenceState) score() int {
	if c.totalCalls < confidenceMinCalls {
		return -1
	}

	overallRate := float64(c.successCount) / float64(c.totalCalls) * 100

	recentRate := overallRate
	if len(c.recentResults) > 0 {
		recentSuccess := 0
		for _, ok := range c.recentResults {
			if ok {
				recentSuccess++
			}
		}
		recentRate = float64(recentSuccess) / float64(len(c.recentResults)) * 100
	}

	toolDiv := len(c.uniqueTools)
	divBonus := 0
	switch {
	case toolDiv >= 7:
		divBonus = 10
	case toolDiv >= 4:
		divBonus = 5
	}

	clusterPenalty := 0
	if c.maxErrorCluster > 2 {
		clusterPenalty = (c.maxErrorCluster - 2) * 3
	}

	editPenalty := 0
	if c.editAttempts >= 3 {
		editFailRate := 1.0 - float64(c.editSuccess)/float64(c.editAttempts)
		if editFailRate > 0.5 {

			editPenalty = int(editFailRate * 20)
		}
	}

	momentumDiv := recentRate - overallRate
	momentumAdj := int(momentumDiv * 0.3)

	score := int(overallRate) + divBonus - clusterPenalty - editPenalty + momentumAdj

	if score < 0 {
		score = 0
	}
	if score > 100 {
		score = 100
	}

	return score
}

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

	if c.maxErrorCluster >= 4 {
		return ""
	}

	c.guidanceGiven = true

	var reasons []string

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
		reasons = append(reasons, "very low tool diversity - consider a different approach")
	}

	// Dual evidence: low LM confidence on top of a weak trajectory score.
	// A polished answer with a low avg logprob is exactly the wrapped-
	// authority failure mode (arXiv:2608.27167).
	if c.lastLogprob != nil && *c.lastLogprob <= -2.5 {
		reasons = append(reasons, fmt.Sprintf("recent turns had low language-model confidence (avg token logprob %.2f)", *c.lastLogprob))
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

func isEditTool(toolName string) bool {
	return sourceMutatingTools[toolName]
}
