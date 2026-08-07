package agent

import (
	"strconv"
	"strings"
)

// Strategy Stagnation Detector
//
// Research basis:
//   - Error Recovery and Fallback Strategies in AI Agent Development
//     (GoCodeo, 2025): emphasizes "Modular Agent Design for Fallback
//     Paths" and "Semantic Fallback with Prompt Variants" - agents must
//     switch strategies after failure, not repeat the same approach.
//   - From Aleatoric to Epistemic: Exploring Uncertainty Quantification
//     (arXiv 2501.03282, 2025): under epistemic uncertainty, repeated
//     identical attempts provide diminishing information gain.
//   - Agentic Metacognition (arXiv 2509.19783): strategy diversity
//     after failure is a key metacognitive skill that prevents agents
//     from getting stuck in local minima.
//
// Problem: When a tool call fails (returns an error), the agent often
// retries with the same tool and same target (file path, command, etc.)
// without changing its approach. This "strategy stagnation" wastes
// tokens and indicates the agent is stuck in a local minimum - trying
// harder with the same strategy instead of trying differently.
//
// Existing ggcode detectors that are RELATED but do NOT cover this:
//   - edit_oscillation.go: detects back-and-forth semantic reversals.
//   - failed_fix_cascade.go: detects same wrong assumption compounding.
//   - tool_diversity.go: detects lack of tool variety overall.
//   - convergence_lock.go: detects repeated failed tool calls generally.
//
// Gap: No detector specifically identifies the pattern of same-tool +
// same-target retries after error. This detector catches when the agent
// should pivot to a fundamentally different strategy (e.g., read more
// context, try a different file, use a different tool).
//
// Design:
//   - Tracks the last N tool calls with their name, target, and success.
//   - When 2+ consecutive failures with same tool + same target occur,
//     inject guidance to try a different approach.
//   - Non-blocking advisory, max 2 warnings per run.
//   - Zero LLM cost - pure deterministic pattern matching.

const (
	// stagnationHistorySize: number of recent tool attempts to track.
	stagnationHistorySize = 8

	// stagnationFailureThreshold: consecutive same-tool+target failures
	// needed to trigger a warning.
	stagnationFailureThreshold = 2

	// stagnationMaxWarnings: max warnings per run.
	stagnationMaxWarnings = 2
)

// stagnationAttempt records one tool call attempt.
type stagnationAttempt struct {
	toolName string
	target   string // extracted key argument (file path, command, etc.)
	success  bool
}

// strategyStagnationState tracks recent tool attempts for stagnation.
type strategyStagnationState struct {
	recent   []stagnationAttempt
	warnings int
}

func newStrategyStagnationState() *strategyStagnationState {
	return &strategyStagnationState{}
}

func (s *strategyStagnationState) reset() {
	s.recent = nil
	s.warnings = 0
}

// extractStagnationTarget pulls the primary target identifier from tool
// call arguments. For file-editing tools, this is the file path. For
// command/search tools, the command or pattern. Used to detect whether
// the agent is retrying the exact same target.
func extractStagnationTarget(toolName, argsJSON string) string {
	switch toolName {
	case "edit_file", "write_file", "read_file", "multi_edit_file",
		"lsp_diagnostics", "lsp_symbols", "lsp_definition":
		return extractJSONStringFieldStag(argsJSON, "file_path", "path")
	case "run_command", "start_command":
		return extractJSONStringFieldStag(argsJSON, "command")
	case "grep", "search_files":
		p := extractJSONStringFieldStag(argsJSON, "pattern")
		if p != "" {
			return p
		}
		return extractJSONStringFieldStag(argsJSON, "query")
	case "git_add", "git_commit", "git_diff", "git_status":
		return extractJSONStringFieldStag(argsJSON, "path")
	default:
		return ""
	}
}

// extractJSONStringFieldStag extracts a string field from JSON, trying
// multiple keys in order. Returns "" if none found.
func extractJSONStringFieldStag(jsonStr string, keys ...string) string {
	for _, k := range keys {
		// Search for "key": "value" pattern
		search := "\"" + k + "\":\""
		idx := strings.Index(jsonStr, search)
		if idx == -1 {
			// Try with space after colon
			search = "\"" + k + "\": \""
			idx = strings.Index(jsonStr, search)
			if idx == -1 {
				continue
			}
		}
		start := idx + len(search)
		end := strings.Index(jsonStr[start:], "\"")
		if end == -1 {
			continue
		}
		return jsonStr[start : start+end]
	}
	return ""
}

// recordAttempt records a tool call result and returns true if a
// strategy stagnation warning should fire.
func (s *strategyStagnationState) recordAttempt(toolName, argsJSON string, success bool) bool {
	target := extractStagnationTarget(toolName, argsJSON)
	attempt := stagnationAttempt{
		toolName: toolName,
		target:   target,
		success:  success,
	}

	s.recent = append(s.recent, attempt)
	if len(s.recent) > stagnationHistorySize {
		s.recent = s.recent[1:]
	}

	// Check for consecutive same-tool+target failures at the tail.
	if success || len(s.recent) < stagnationFailureThreshold {
		return false
	}

	consecutiveFailures := 0
	for i := len(s.recent) - 1; i >= 0; i-- {
		entry := s.recent[i]
		if entry.toolName == toolName && entry.target == target && !entry.success {
			consecutiveFailures++
		} else {
			break
		}
	}

	if consecutiveFailures >= stagnationFailureThreshold && s.warnings < stagnationMaxWarnings {
		s.warnings++
		return true
	}

	return false
}

// strategyStagnationWarning returns guidance text when the detector fires.
func strategyStagnationWarning(toolName, target string, failures int) string {
	msg := "[STRATEGY-STAGNATION] " + toolName
	if target != "" {
		t := target
		if len(t) > 60 {
			t = t[:60] + "..."
		}
		msg += " on " + t
	}
	msg += " has failed " + strconv.Itoa(failures) + " consecutive times with the same approach. " +
		"You are stuck in a local minimum. STOP retrying with the same strategy. " +
		"Pivot to a fundamentally different approach: " +
		"(1) re-read the file/error to understand the root cause, " +
		"(2) try a different tool or method, " +
		"(3) simplify the operation into smaller steps, " +
		"(4) verify assumptions about the current state. " +
		"Repeating the same action expecting different results is a waste of tokens."
	return msg
}
