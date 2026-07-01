package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

// loopDetector tracks consecutive identical tool calls across agent loop
// iterations. When the same tool is called with the same arguments N times in
// a row, it injects a system message to break the loop.
//
// This addresses a common failure mode where the LLM gets stuck repeating the
// same failed operation (e.g., editing a file with the wrong old_text, then
// trying the exact same edit again) and wastes many iterations/tokens without
// making progress.
type loopDetector struct {
	// History of tool call fingerprints from the current consecutive run.
	// Reset when a different tool or different arguments are seen.
	fingerprints []string

	// lastToolName tracks the tool name from the previous call for logging.
	lastToolName string
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
}

// loopDetectionInjection appends a guidance message to toolResults when the
// agent is stuck in a consecutive duplicate tool call loop.
func (a *Agent) loopDetectionInjection(tc provider.ToolCallDelta) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.loopDetector.checkDuplicate(tc)
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
