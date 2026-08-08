package agent

import (
	"fmt"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

// Agentic Abstention Detector -- Untimely Continuation Awareness
//
// Research basis:
//   - "Agentic Abstention: Do Agents Know When to Stop Instead of Act?"
//     (Luo, Wen, Wang -- arXiv:2606.28733, June 2026): defines the problem of
//     deciding WHEN an agent should stop acting under uncertainty. Key finding:
//     "Some agents never abstain when they should, while others do so only after
//     many unnecessary interactions." The gap is especially large on tasks where
//     "the instruction appears feasible until the environment reveals otherwise
//     (e.g., no valid result matches the instruction)." The paper introduces
//     CONVOLVE, a context-engineering method that distills interaction
//     trajectories into reusable stopping rules.
//   - fp8.co "AI Agents That Know When Not to Guess" (July 2026): RLHF
//     degrades calibration, making agents overconfident. "An agent that guesses
//     a wrong file path at step 3 will build steps 4 through 12 on top of that
//     mistake." The solution is an abstention gate that checks confidence at
//     each tool-call boundary.
//
// The gap: ggcode has premature_surrender (detects giving up too early in text),
// but has NO detector for the OPPOSITE failure: the agent continues making tool
// calls AFTER the environment has clearly signaled the goal is unachievable.
// For example:
//   - Agent searches for a file/package/API that doesn't exist. First search
//     returns empty. Agent rephrases and searches again. Still empty. Agent
//     tries a third variant. This continues for 5+ iterations consuming budget.
//   - Agent tries to import a package that isn't in go.mod. Each variation of
//     the import fails with the same "not found" error. Agent keeps trying.
//   - Agent queries an API endpoint that returns 404. Agent retries with
//     different parameters instead of recognizing the endpoint doesn't exist.
//
// Key distinction from existing detectors:
//   - empty_search_spiral: tracks ONLY search tools returning empty (grep,
//     search_files, glob). Abstention tracks ANY tool returning negative
//     signals (not just search tools) -- file not found errors, 404s, import
//     failures, missing package errors across read_file, run_command, etc.
//   - premature_surrender: detects giving up in TEXT. Abstention detects the
//     INVERSE: NOT giving up when the environment says you should.
//   - patch_exhaust: detects re-reading the same DIRECTORY. Abstention detects
//     re-attempting the same FAILED GOAL (different files, same negative result).
//   - futile_cycle: detects circular read patterns. Abstention detects LINEAR
//     accumulation of negative signals without course correction.
//
// Detection heuristic:
//  1. Track tool results that signal "not found" or "doesn't exist" across all
//     tool types, not just search.
//  2. Track whether the agent has acknowledged any of these signals (appeared
//     in its text response as "not found", "doesn't exist", etc.)
//  3. When N consecutive negative signals accumulate WITHOUT acknowledgment,
//     inject abstention guidance: stop retrying, state what's unavailable,
//     and ask the user for guidance.

const (
	maxAbstainWarnings      = 1 // fire at most once per run
	negativeSignalThreshold = 3 // consecutive unacknowledged negatives
	negativeSignalWindow    = 6 // sliding window size for consecutive tracking
)

// negativeSignalPatterns detect "not found" / "doesn't exist" / "unavailable"
// signals in tool RESULT content. These are case-insensitive substring checks.
var negativeResultSignals = []string{
	"no such file or directory",
	"does not exist",
	"doesn't exist",
	"not found",
	"no results",
	"no matches",
	"0 results",
	"zero matches",
	"no files found",
	"nothing found",
	"package not found",
	"module not found",
	"cannot find",
	"could not find",
	"unable to find",
	"no matching",
	"no entries",
	"empty result",
	"found 0",
	"0 matches",
	"404",
	"not available",
	"unavailable",
	"no such package",
	"undefined:",
	"unresolved reference",
}

// acknowledgmentPatterns detect when the agent's TEXT acknowledges a negative
// signal (showing awareness rather than blindly retrying).
var acknowledgmentPatterns = []string{
	"not found",
	"doesn't exist",
	"does not exist",
	"isn't available",
	"is not available",
	"unavailable",
	"no such",
	"no longer exists",
	"has been removed",
	"has been deleted",
	"can't find",
	"cannot find",
	"unable to find",
	"missing",
	"not present",
	"absent",
	"not installed",
	"not available in",
	"no matching",
}

// abstainState tracks unacknowledged negative environment signals across a run.
type abstainState struct {
	mu                   sync.Mutex
	fired                bool
	consecutiveNegatives int    // consecutive unacknowledged negatives in window
	lastAcknowledged     bool   // whether the last assistant text acknowledged
	negativeHistory      []bool // sliding window: true=negative signal, false=positive
	totalNegativeSignals int
}

func newAbstainState() *abstainState {
	return &abstainState{
		negativeHistory: make([]bool, 0, negativeSignalWindow),
	}
}

func (s *abstainState) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fired = false
	s.consecutiveNegatives = 0
	s.lastAcknowledged = false
	s.negativeHistory = s.negativeHistory[:0]
	s.totalNegativeSignals = 0
}

// hasNegativeSignal checks if tool result content contains "not found" signals.
func hasNegativeSignal(content string) bool {
	if content == "" {
		return false
	}
	lower := strings.ToLower(content)
	for _, sig := range negativeResultSignals {
		if strings.Contains(lower, sig) {
			return true
		}
	}
	return false
}

// hasAcknowledgment checks if assistant text acknowledges negative signals.
func hasAcknowledgment(text string) bool {
	if strings.TrimSpace(text) == "" {
		return false
	}
	lower := strings.ToLower(text)
	for _, pat := range acknowledgmentPatterns {
		if strings.Contains(lower, pat) {
			return true
		}
	}
	return false
}

// recordResult tracks a tool result for negative-signal detection.
func (s *abstainState) recordResult(resultContent string, isError bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	isNegative := false
	if isError {
		isNegative = hasNegativeSignal(resultContent) || isNotFoundLike(resultContent)
	} else {
		// Even non-error results can carry negative signals (empty search, etc.)
		isNegative = hasNegativeSignal(resultContent)
	}

	// Update sliding window
	if len(s.negativeHistory) >= negativeSignalWindow {
		s.negativeHistory = s.negativeHistory[1:]
	}
	s.negativeHistory = append(s.negativeHistory, isNegative)

	if isNegative {
		s.totalNegativeSignals++
		if !s.lastAcknowledged {
			s.consecutiveNegatives++
		} else {
			s.consecutiveNegatives = 1 // reset on acknowledgment
		}
	} else {
		// Positive result resets consecutive counter
		s.consecutiveNegatives = 0
	}
}

// recordAcknowledgment tracks whether the assistant text acknowledged negatives.
func (s *abstainState) recordAcknowledgment(assistantText string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastAcknowledged = hasAcknowledgment(assistantText)
	if s.lastAcknowledged {
		s.consecutiveNegatives = 0 // acknowledged, so reset
	}
}

// isNotFoundLike checks for tool-specific "not found" patterns in error results.
// Some tools return errors without the standard "not found" string.
func isNotFoundLike(content string) bool {
	lower := strings.ToLower(content)
	// Common "not found" patterns across all tool types
	return strings.Contains(lower, "command not found") ||
		strings.Contains(lower, "no such file") ||
		strings.Contains(lower, "cannot find package") ||
		strings.Contains(lower, "package ") && strings.Contains(lower, " not found") ||
		strings.Contains(lower, "no matches") ||
		strings.Contains(lower, "0 matches")
}

// checkAbstention evaluates whether the agent should be guided to stop and
// acknowledge the negative environment. Returns non-empty guidance if detected.
//
// Parameters:
//   - currentIter: 1-based current iteration number
//   - maxIter: maximum iteration budget
func (s *abstainState) checkAbstention(currentIter, maxIter int) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.fired {
		return ""
	}
	if s.consecutiveNegatives < negativeSignalThreshold {
		return ""
	}

	// Don't fire in the last iteration (agent is wrapping up anyway)
	if currentIter >= maxIter {
		return ""
	}

	s.fired = true
	debug.Log("abstain-detect", "agentic abstention guidance: %d consecutive unacknowledged negative signals at iter %d/%d (total_negatives=%d)",
		s.consecutiveNegatives, currentIter, maxIter, s.totalNegativeSignals)

	return fmt.Sprintf(
		"[Agentic Abstention] The environment has returned %d consecutive negative signals "+
			"(not found, unavailable, no results) without your acknowledgment. "+
			"Continuing to retry variations of the same approach is unlikely to succeed and wastes budget.\n\n"+
			"Recommended actions:\n"+
			"1. STOP and explicitly state what is unavailable or not found in your response.\n"+
			"2. Determine whether the target genuinely doesn't exist, or whether you're using the wrong identifier/path.\n"+
			"3. If the target truly doesn't exist, inform the user clearly rather than silently retrying.\n"+
			"4. Consider whether an ALTERNATIVE approach could achieve the underlying goal.\n\n"+
			"Do not make another attempt at the same goal without first acknowledging the negative result.",
		s.consecutiveNegatives,
	)
}
