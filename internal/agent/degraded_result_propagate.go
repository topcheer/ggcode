package agent

import (
	"fmt"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

// Silent Degradation Propagation Detector.
//
// Research basis: Galileo's "7 AI Agent Failure Modes" (2026) identifies error
// propagation as the #1 reliability killer: "A wrong tool result in step 2
// poisons steps 3, 4, and 5." AgentDiet (arXiv:2509.23586, PACMSE 2026) shows
// that degraded/expired information in agent trajectories is widespread and
// compounds silently.
//
// The specific pattern: when a tool returns a *degraded* result (not a hard
// error, but empty/null/no-data), the agent often treats it as valid data and
// continues building reasoning on top of it. Each subsequent step inherits the
// corrupted state. The agent never acknowledges the degradation - it simply
// advances as if everything is fine.
//
// This is distinct from:
//   - empty_search_tracker: fires after 3 CONSECUTIVE empty searches (repetition)
//   - truncated_completeness_fallacy: detects truncation-specific issues
//   - agentic_abstain: detects premature surrender on negative signals
//   - tool_output_guard: handles oversized outputs (size, not emptiness)
//
// This detector fires on FIRST occurrence when the agent's assistant text
// following a degraded result fails to acknowledge the degradation and instead
// continues reasoning as if it received valid data. It detects the propagation
// pattern, not the degradation itself.

const (
	// degradedResultMaxFires caps the number of guidance injections per run.
	degradedResultMaxFires = 2

	// degradedResultMaxHistory tracks the last N tool results to check
	// whether the agent acknowledged a prior degradation in its text.
	degradedResultMaxHistory = 12
)

// degradedResultInfo records a single degraded tool result and whether the
// agent subsequently acknowledged it in its reasoning text.
type degradedResultInfo struct {
	toolName     string
	iteration    int
	acknowledged bool
}

// degradedResultState tracks degraded tool results and detects when the agent
// silently propagates them without acknowledgment.
type degradedResultState struct {
	mu            sync.Mutex
	recentResults []degradedResultInfo // sliding window of recent results
	pendingCheck  *degradedResultInfo  // last degraded result not yet checked against assistant text
	guidanceFired int
}

func newDegradedResultState() *degradedResultState {
	return &degradedResultState{}
}

func (d *degradedResultState) reset() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.recentResults = nil
	d.pendingCheck = nil
	d.guidanceFired = 0
}

// isDegradedResult checks if a non-error tool result is effectively empty/degraded.
// This is a broader check than isEmptyResult - it also covers non-search tools
// (e.g., read_file returning nothing, run_command with empty stdout).
func isDegradedResult(content string) bool {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return true
	}

	// Very short results from data-retrieval tools are suspicious.
	// These tools are expected to return substantial content.
	if len(trimmed) < 80 {
		lower := strings.ToLower(trimmed)
		degradedPhrases := []string{
			"no results",
			"no matches",
			"no files found",
			"nothing found",
			"no output",
			"empty",
			"not found",
			"does not exist",
			"no such file",
			"no data",
			"null",
			"undefined",
			"no entries",
			"no commits",
			"no changes",
			"nothing to show",
			"no content",
			"0 matches",
			"0 results",
			"0 files",
			"0 entries",
		}
		for _, phrase := range degradedPhrases {
			if strings.Contains(lower, phrase) {
				return true
			}
		}
	}

	return false
}

// toolsExpectedToReturnData are tools that should return meaningful content.
// If they return empty/degraded results, something went wrong.
var toolsExpectedToReturnData = map[string]bool{
	"read_file":      true,
	"grep":           true,
	"glob":           true,
	"search_files":   true,
	"code_search":    true,
	"lsp_definition": true,
	"lsp_references": true,
	"lsp_symbols":    true,
	"git_log":        true,
	"git_show":       true,
	"git_blame":      true,
	"git_diff":       true,
	"list_directory": true,
	"web_search":     true,
	"web_fetch":      true,
	"run_command":    true,
}

// recordDegradedResult is called after each tool execution. It records degraded
// results and marks them as pending acknowledgment check.
func (d *degradedResultState) recordDegradedResult(toolName, content string, isError bool, iteration int) {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Maintain sliding window
	if len(d.recentResults) >= degradedResultMaxHistory {
		d.recentResults = d.recentResults[1:]
	}

	if isError {
		// Hard errors are handled by the error system. Record as non-degraded.
		d.recentResults = append(d.recentResults, degradedResultInfo{
			toolName:     toolName,
			iteration:    iteration,
			acknowledged: true, // errors are inherently "acknowledged" since the agent sees them as errors
		})
		d.pendingCheck = nil
		return
	}

	if toolsExpectedToReturnData[toolName] && isDegradedResult(content) {
		d.recentResults = append(d.recentResults, degradedResultInfo{
			toolName:     toolName,
			iteration:    iteration,
			acknowledged: false,
		})
		d.pendingCheck = &d.recentResults[len(d.recentResults)-1]
		debug.Log("degraded-result", "degraded result from %s at iteration %d (pending acknowledgment check)",
			toolName, iteration)
	} else {
		// Normal result
		d.recentResults = append(d.recentResults, degradedResultInfo{
			toolName:     toolName,
			iteration:    iteration,
			acknowledged: true,
		})
		d.pendingCheck = nil
	}
}

// checkAcknowledgment examines the assistant's text response for evidence that
// it acknowledged the degraded result. Returns guidance text if the agent
// silently propagated the degradation without acknowledgment.
//
// acknowledgmentPatterns are phrases that indicate the agent recognized the
// degraded/empty result and is adjusting its approach.
var degradedAckPatterns = []string{
	"no result",
	"no results",
	"didn't find",
	"did not find",
	"didn't return",
	"did not return",
	"no matches",
	"no files",
	"nothing found",
	"empty result",
	"no output",
	"not found",
	"doesn't exist",
	"does not exist",
	"no such file",
	"no data",
	"returned nothing",
	"returned empty",
	"came back empty",
	"no entries",
	"no commits",
	"no changes",
	"no content",
	"0 matches",
	"0 results",
	"0 files",
	"the search returned",
	"the query returned",
	"the tool returned",
	"was not found",
	"weren't found",
	"was empty",
	"were empty",
	"is empty",
	"are empty",
	"unable to find",
	"failed to find",
	"couldn't find",
	"could not find",
	"no luck",
	"came up empty",
	"struck out",
	"dead end",
	"nothing came back",
	"returned no",
}

func (d *degradedResultState) checkAcknowledgment(assistantText string) string {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.pendingCheck == nil {
		return ""
	}

	// Check if the assistant text acknowledges the degradation
	lowerText := strings.ToLower(assistantText)
	acknowledged := false
	for _, ackPhrase := range degradedAckPatterns {
		if strings.Contains(lowerText, ackPhrase) {
			acknowledged = true
			break
		}
	}

	// Mark the pending result as checked
	d.pendingCheck.acknowledged = acknowledged
	pending := d.pendingCheck
	d.pendingCheck = nil

	if acknowledged {
		// Agent recognized the degradation - no propagation risk
		debug.Log("degraded-result", "agent acknowledged degraded result from %s (no propagation)",
			pending.toolName)
		return ""
	}

	// Agent silently propagated - inject guidance if within fire limit
	if d.guidanceFired >= degradedResultMaxFires {
		return ""
	}

	d.guidanceFired++
	debug.Log("degraded-result", "SILENT PROPAGATION: agent did not acknowledge degraded %s result (fire #%d)",
		pending.toolName, d.guidanceFired)

	return d.buildGuidance(pending.toolName)
}

func (d *degradedResultState) buildGuidance(toolName string) string {
	tips := []string{
		fmt.Sprintf("[degraded-result] %s returned empty/no-data result but you continued as if valid.", toolName),
		"This is a leading cause of error cascades in agent systems (Galileo 2026).",
		"Before continuing:",
		"1. Explicitly acknowledge the degraded result in your reasoning",
		"2. Determine WHY the result was empty (wrong path, wrong pattern, resource doesn't exist)",
		"3. Verify your assumptions - do not build further conclusions on the empty result",
		"4. Adjust your approach based on the actual (missing) data, not what you expected to find",
	}
	return strings.Join(tips, "\n")
}
