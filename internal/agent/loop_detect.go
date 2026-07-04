package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

// loopDetector tracks consecutive identical tool calls AND consecutive error
// streaks across agent loop iterations.
//
// Two detection modes:
//  1. Exact duplicate detection: same tool + same args called N times in a row.
//  2. Error-streak detection: any N consecutive tool calls all return errors,
//     even if they are different tools with different args. This catches the
//     common "try → fail → try different approach → fail again" cycle that
//     exact-duplicate detection misses.
//
// Error-streak detection implements the core insight from the Reflexion paper
// (Shinn et al. 2023) and the Iterative Refinement Loop pattern: when the agent
// has had multiple consecutive failures, it should stop and reconsider its
// entire strategy rather than continuing to try minor variations.
type loopDetector struct {
	// History of tool call fingerprints from the current consecutive run.
	// Reset when a different tool or different arguments are seen.
	fingerprints []string

	// lastToolName tracks the tool name from the previous call for logging.
	lastToolName string

	// consecutiveErrors counts how many tool calls in a row returned errors.
	// Reset to 0 when any tool call succeeds.
	consecutiveErrors int

	// errorGuidanceGiven tracks whether we already injected error-streak
	// guidance at the current threshold, to avoid spamming.
	errorGuidanceGiven bool
}

// fingerprintToolCall creates a hash of tool name + arguments.
func fingerprintToolCall(name string, args []byte) string {
	h := sha256.Sum256(append([]byte(name+"|"), args...))
	return hex.EncodeToString(h[:8]) // 8 hex chars = 4 bytes, enough for dedup
}

// checkDuplicate returns a non-empty guidance message if the tool call is a
// duplicate of the previous consecutive calls (same tool, same arguments, 3+
// times in a row). Returns empty string otherwise.
func (ld *loopDetector) checkDuplicate(tc provider.ToolCallDelta) string {
	fp := fingerprintToolCall(tc.Name, tc.Arguments)

	// If this is a different fingerprint, reset the streak.
	if len(ld.fingerprints) == 0 || ld.fingerprints[len(ld.fingerprints)-1] != fp {
		ld.fingerprints = []string{fp}
		ld.lastToolName = tc.Name
		return ""
	}

	// Same fingerprint — increment streak.
	ld.fingerprints = append(ld.fingerprints, fp)

	// Only warn at exactly 3 consecutive duplicates to avoid spamming.
	// The message itself usually causes the LLM to change approach.
	streak := len(ld.fingerprints)
	if streak == 3 {
		debug.Log("agent", "loop detection: %s called %d times with identical args, injecting guidance", tc.Name, streak)
		ld.lastToolName = tc.Name
		return fmt.Sprintf(
			"Notice: You have called %s with the exact same arguments %d consecutive times. "+
				"This suggests you may be stuck in a loop. If the previous attempts failed, "+
				"try a different approach: read the current file content first, use different "+
				"parameters, or reconsider your strategy. Do not repeat the exact same call.",
			tc.Name, streak,
		)
	}

	// At 5+ consecutive duplicates, inject stronger guidance.
	if streak == 5 {
		debug.Log("agent", "loop detection: %s called %d times with identical args, STRONG warning", tc.Name, streak)
		return fmt.Sprintf(
			"WARNING: You have called %s with identical arguments %d times. "+
				"This is clearly not working. You MUST change your approach entirely. "+
				"Stop and think about why this is failing before trying anything else.",
			tc.Name, streak,
		)
	}

	return ""
}

// reset clears the detector state. Called when a different tool call is seen
// or when a new user turn starts.
func (ld *loopDetector) reset() {
	ld.fingerprints = nil
	ld.lastToolName = ""
	ld.consecutiveErrors = 0
	ld.errorGuidanceGiven = false
}

// recordResult updates the error streak counter. Call after each tool result
// is known. If the streak hits a threshold, returns guidance text.
func (ld *loopDetector) recordResult(isError bool, toolName string) string {
	if isError {
		ld.consecutiveErrors++
	} else {
		if ld.consecutiveErrors > 0 {
			ld.consecutiveErrors = 0
			ld.errorGuidanceGiven = false
		}
		return ""
	}

	if ld.errorGuidanceGiven {
		return ""
	}

	// At 4 consecutive errors from different tool calls, inject strategic
	// guidance. This is the key threshold: the agent has tried multiple
	// different approaches and none worked. Time to step back and think.
	if ld.consecutiveErrors >= 4 {
		ld.errorGuidanceGiven = true
		debug.Log("agent", "error-streak detection: %d consecutive tool errors, injecting strategic guidance", ld.consecutiveErrors)
		return fmt.Sprintf(
			"You have had %d consecutive tool calls return errors in a row. "+
				"This strongly suggests your current approach is not working. "+
				"STOP and reconsider your strategy from scratch:\n"+
				"1. Re-read the relevant files to understand the actual current state\n"+
				"2. Check if there are compile errors or type mismatches you missed\n"+
				"3. Consider whether the problem is in a different file than you think\n"+
				"4. Try a fundamentally different approach instead of minor variations",
			ld.consecutiveErrors,
		)
	}

	return ""
}

// loopDetectionInjection appends a guidance message to toolResults when the
// agent is stuck in a consecutive duplicate tool call loop.
func (a *Agent) loopDetectionInjection(tc provider.ToolCallDelta) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.loopDetector.checkDuplicate(tc)
}

// errorStreakCheck tracks consecutive tool errors and injects strategic
// guidance when the agent has had multiple failures in a row. This catches
// the "try different approaches, all fail" pattern that exact-duplicate
// detection misses.
func (a *Agent) errorStreakCheck(isError bool, toolName string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.loopDetector.recordResult(isError, toolName)
}

// resetLoopDetector clears consecutive tool call tracking. Called at the start
// of each new RunStreamWithContent (new user turn).
func (a *Agent) resetLoopDetector() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.loopDetector.reset()
}

// toolCallSummary returns a short human-readable summary of a tool call for
// logging purposes.
func toolCallSummary(tc provider.ToolCallDelta) string {
	argStr := string(tc.Arguments)
	if len(argStr) > 80 {
		argStr = argStr[:77] + "..."
	}
	return fmt.Sprintf("%s(%s)", tc.Name, strings.TrimSpace(argStr))
}
