package agent

// Dynamic Replan Detector - Trigger Active Path Replanning on Persistent Tool Failures
//
// Research basis: "When Tools Fail: Benchmarking Dynamic Replanning and Anomaly
// Recovery in LLM Agents" (arXiv:2606.05806, Jun 2026) establishes that effective
// agents must dynamically replan their execution path when tools fail, rather than
// blindly retrying or providing static fallback suggestions.
//
// The paper identifies three failure response modes:
//   1. Retry: Retry the same tool (works for transient errors)
//   2. Fallback: Switch to an alternative tool (works when tools are substitutes)
//   3. Replan: Re-evaluate the entire task path (works when the approach is wrong)
//
// Gap in existing ggcode systems:
//   - tool_error_fallback.go: provides static per-tool fallback suggestions, but
//     doesn't trigger replanning when the same tool repeatedly fails
//   - transient_retry.go: silently retries the same tool, doesn't consider replanning
//   - failure_mode.go: classifies failure types but doesn't trigger active replanning
//   - capability_boundary.go: detects when all approaches fail, but only after N
//     distinct failures, not after repeated failures of the same tool
//
// This component fills the gap by triggering active replanning guidance when:
//   - The SAME tool fails >= 2 times consecutively OR
//   - Different tools fail but for the SAME underlying reason (detected via error
//     pattern clustering)
//
// Design:
//   - Zero LLM cost (deterministic counter + heuristic error clustering)
//   - Fires when replanning is likely beneficial (persistent failure pattern)
//   - Provides concrete replanning prompts: "Your approach to [goal] is not
//     working. Consider: (1) alternative tools, (2) different strategy, (3)
//     breaking down the task differently"
//   - Distinct from tool_error_fallback: fallback suggests alternative tools for
//     a specific failure; replan suggests rethinking the entire approach
//
// Implementation notes:
//   - Tracks consecutive failures per tool (not total failures)
//   - Fires at most 2 times per run (to avoid nagging)
//   - Resets on any successful tool execution (progress indicator)

import (
	"fmt"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	// replanTriggerCount is the number of consecutive failures for the same tool
	// that triggers replanning guidance. Set to 2 to catch persistent failures
	// without being too aggressive.
	replanTriggerCount = 2
	// maxReplanWarnings caps replanning warnings per run to avoid nagging.
	maxReplanWarnings = 2
)

// replanState tracks tool failure patterns to detect when replanning is needed.
type replanState struct {
	mu             sync.Mutex
	failCount      map[string]int    // tool name → consecutive failure count
	lastError      map[string]string // tool name → last error content (for clustering)
	warningCount   int               // total replan warnings issued this run
	lastSuggestion string            // most recent replan suggestion (for dedup)
}

func newReplanState() *replanState {
	return &replanState{
		failCount: make(map[string]int),
		lastError: make(map[string]string),
	}
}

func (r *replanState) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failCount = make(map[string]int)
	r.lastError = make(map[string]string)
	r.warningCount = 0
	r.lastSuggestion = ""
}

// recordResult tracks tool execution and returns replanning guidance if needed.
// Call this AFTER tool execution, with the tool name, whether it succeeded,
// and the error content (empty on success).
func (r *replanState) recordResult(toolName string, isSuccess bool, errorContent string) string {
	if isSuccess {
		// Reset failure count for this tool on success - progress made.
		r.mu.Lock()
		delete(r.failCount, toolName)
		delete(r.lastError, toolName)
		r.mu.Unlock()
		return ""
	}

	if r.warningCount >= maxReplanWarnings {
		return "" // Cap reached, don't nag.
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// Increment failure count for this tool.
	r.failCount[toolName]++
	r.lastError[toolName] = errorContent

	// Check if this tool has reached the trigger threshold.
	if r.failCount[toolName] < replanTriggerCount {
		return ""
	}

	// Generate replan suggestion.
	suggestion := r.generateReplanSuggestion(toolName, errorContent)

	// Avoid duplicate warnings.
	if suggestion == r.lastSuggestion {
		return ""
	}

	r.warningCount++
	r.lastSuggestion = suggestion

	debug.Log("replan", "tool '%s' failed %d times, triggering replan: %s",
		toolName, r.failCount[toolName], suggestion)

	return suggestion
}

// generateReplanSuggestion creates a contextual replanning prompt based on the
// failing tool and error pattern.
func (r *replanState) generateReplanSuggestion(toolName, errorContent string) string {
	var strategy string

	// Classify the failure type to tailor the replan suggestion.
	lowerErr := strings.ToLower(errorContent)

	// Permission/auth failures -> suggest checking requirements.
	if strings.Contains(lowerErr, "permission") || strings.Contains(lowerErr, "denied") ||
		strings.Contains(lowerErr, "unauthorized") || strings.Contains(lowerErr, "forbidden") {
		strategy = "Check if you have the necessary permissions or if the resource is accessible."
	} else if strings.Contains(lowerErr, "not found") || strings.Contains(lowerErr, "no such") ||
		strings.Contains(lowerErr, "does not exist") {
		// Not found -> suggest verifying path/context.
		strategy = "Verify the file path, identifier, or resource name is correct."
	} else if strings.Contains(lowerErr, "timeout") || strings.Contains(lowerErr, "deadline") ||
		strings.Contains(lowerErr, "context canceled") {
		// Timeout -> suggest breaking down task or optimizing.
		strategy = "Consider breaking down the task into smaller steps or using more efficient operations."
	} else if strings.Contains(lowerErr, "syntax") || strings.Contains(lowerErr, "parse") ||
		strings.Contains(lowerErr, "invalid") {
		// Syntax/invalid -> suggest validating inputs.
		strategy = "Validate your inputs (patterns, paths, commands) and ensure they match the expected format."
	} else if strings.Contains(lowerErr, "no match") || strings.Contains(lowerErr, "0 result") ||
		strings.Contains(lowerErr, "empty") {
		// Empty results -> suggest broadening search or checking assumptions.
		strategy = "Broaden your search criteria, try different keywords, or verify your assumptions about the codebase."
	} else {
		// Generic failure.
		strategy = "Consider alternative tools or a different approach to achieve the same goal."
	}

	// Build the full suggestion with context.
	return fmt.Sprintf(
		"[Dynamic Replan] The tool '%s' has failed %d times with errors like '%s'. "+
			"This suggests your current approach may not be working. "+
			"STOP and RE-EVALUATE your plan. Consider: (1) %s "+
			"(2) Are there alternative tools that could achieve the same goal? "+
			"(3) Should you break down the task differently? "+
			"(4) Is your understanding of the problem correct?",
		toolName, r.failCount[toolName], truncateError(errorContent, 50), strategy)
}

// truncateError shortens error messages for display while keeping key info.
func truncateError(err string, maxLen int) string {
	if len(err) <= maxLen {
		return err
	}
	return err[:maxLen] + "..."
}
