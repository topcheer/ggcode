package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

type loopDetector struct {
	fingerprints []string

	lastToolName string

	consecutiveErrors int

	errorGuidanceLevel int
}

func fingerprintToolCall(name string, args []byte) string {
	h := sha256.Sum256(append([]byte(name+"|"), args...))
	return hex.EncodeToString(h[:8])
}

func (ld *loopDetector) checkDuplicate(tc provider.ToolCallDelta) string {
	fp := fingerprintToolCall(tc.Name, tc.Arguments)

	if len(ld.fingerprints) == 0 || ld.fingerprints[len(ld.fingerprints)-1] != fp {
		ld.fingerprints = []string{fp}
		ld.lastToolName = tc.Name
		return ""
	}

	ld.fingerprints = append(ld.fingerprints, fp)

	streak := len(ld.fingerprints)
	if streak == 3 {
		debug.Log("agent", "loop detection: %s called %d times with identical args, injecting guidance", tc.Name, streak)
		ld.lastToolName = tc.Name
		return fmt.Sprintf(
			"Notice: You have called %s with the exact same arguments %d consecutive times. "+
				"This suggests you may be stuck in a loop - BUT if the result was marked "+
				"'[cached — identical content]' after a context compaction, one repeat is "+
				"expected and fine (the memoization layer answers it at zero cost); only "+
				"repeatedly NOT acting on the result is a loop. "+
				"If the previous attempts failed, try a different approach: read the current "+
				"file content first, use different parameters, or reconsider your strategy.",
			tc.Name, streak,
		)
	}

	if streak == 5 {
		debug.Log("agent", "loop detection: %s called %d times with identical args, STRONG warning", tc.Name, streak)
		return fmt.Sprintf(
			"WARNING: You have called %s with identical arguments %d times. "+
				"This is clearly not working. You MUST change your approach entirely. "+
				"Stop and think about why this is failing before trying anything else.",
			tc.Name, streak,
		)
	}

	if streak > 5 && (streak-5)%5 == 0 {
		debug.Log("agent", "loop detection: %s identical-arg streak at %d, periodic re-warn", tc.Name, streak)
		return fmt.Sprintf(
			"WARNING: the identical-argument streak for %s has reached %d calls. "+
				"Whatever you are waiting for is not going to change on its own - change "+
				"the approach or stop.",
			tc.Name, streak,
		)
	}

	return ""
}

func (ld *loopDetector) reset() {
	ld.fingerprints = nil
	ld.lastToolName = ""
	ld.consecutiveErrors = 0
	ld.errorGuidanceLevel = 0
}

func (ld *loopDetector) recordResult(isError bool, toolName string) string {
	if isError {
		ld.consecutiveErrors++
	} else {
		if ld.consecutiveErrors > 0 {
			ld.consecutiveErrors = 0
			ld.errorGuidanceLevel = 0
		}
		return ""
	}

	if ld.consecutiveErrors >= 10 && ld.errorGuidanceLevel < 3 {
		ld.errorGuidanceLevel = 3
		debug.Log("agent", "error-streak: %d consecutive errors, injecting escalation guidance", ld.consecutiveErrors)
		return fmt.Sprintf(
			"CRITICAL: You have had %d consecutive tool errors. This is a deep error spiral that is unlikely to resolve by continuing the same approach.\n"+
				"You must change course now:\n"+
				"1. Stop attempting edits to this file or area\n"+
				"2. Use ask_user to ask the user for guidance, or skip this subtask\n"+
				"3. If you must continue, try a completely different tool (e.g. write_file to replace the entire file instead of edit_file)\n"+
				"4. Re-read ALL relevant files from scratch — your mental model of the code is likely wrong",
			ld.consecutiveErrors,
		)
	}

	if ld.consecutiveErrors >= 7 && ld.errorGuidanceLevel < 2 {
		ld.errorGuidanceLevel = 2
		debug.Log("agent", "error-streak: %d consecutive errors, injecting technical debugging guidance", ld.consecutiveErrors)
		return fmt.Sprintf(
			"You have had %d consecutive tool errors. The first reconsideration did not help, so the problem is likely something specific:\n"+
				"1. Check for renamed symbols or moved functions — search by behavior, not by name\n"+
				"2. Check for import cycles or missing dependencies\n"+
				"3. If this is a shared workspace, other agents may have changed files — git diff to see recent changes\n"+
				"4. Check for build tags (e.g. -tags goolm) or environment-specific code paths\n"+
				"5. Try reading the exact current file content with read_file before your next edit\n"+
				"6. Verify the types match — a struct field rename can cascade silently",
			ld.consecutiveErrors,
		)
	}

	if ld.consecutiveErrors >= 4 && ld.errorGuidanceLevel < 1 {
		ld.errorGuidanceLevel = 1
		debug.Log("agent", "error-streak: %d consecutive tool errors, injecting strategic guidance", ld.consecutiveErrors)
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

func (a *Agent) loopDetectionInjection(tc provider.ToolCallDelta) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.loopDetector.checkDuplicate(tc)
}

func (a *Agent) errorStreakCheck(isError bool, toolName string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.loopDetector.recordResult(isError, toolName)
}

func (a *Agent) resetLoopDetector() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.loopDetector.reset()
}
