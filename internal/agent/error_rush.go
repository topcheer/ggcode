package agent

// error_rush.go -- Error Rush Detector (Panic Coding Acceleration)
//
// Research basis:
//   - "Agentic Uncertainty Reveals Agentic Overconfidence" (arXiv 2026): agents
//     systematically miscalibrate under error conditions -- instead of slowing down
//     to analyze failures, they accelerate tool call frequency, producing rapid-fire
//     edits with diminishing reasoning between them ("panic coding").
//   - AgentDiet (FSE 2026, arXiv:2509.23586): trajectory waste compounds when agents
//     issue error-triggered edits without processing the error output, creating
//     a feedback loop of broken fixes.
//   - CAR-bench (arXiv 2026): evaluates agent "consistency and limit-awareness under
//     real-world uncertainty" -- the failure to modulate effort after errors is a
//     primary source of inconsistency.
//
// This detector identifies a specific behavioral anti-pattern: after 2+ consecutive
// failed tool calls (build errors, test failures, edit errors), the agent issues
// a mutation (edit/write) WITHOUT an intervening read/analysis step. This means
// the agent is "blind-fixing" -- attempting corrections without re-examining the
// error output or the code it's trying to fix.
//
// The pattern: error -> error -> immediate edit (no read/diagnosis between them).
// The fix: error -> error -> [pause: read the error, re-read the file, then fix].
//
// Distinct from existing detectors:
//   - mindlessAction: rapid-fire calls in general (no error precondition)
//   - errorStrategyLoop: reusing the same fix strategy across failures
//   - correctionSpiral: error severity escalation tracking
//   - strategyFixation: same file edited N times with failures
//   - bareEditStreak: consecutive edits without verification (no error trigger)
//   - silentError: proceeding past an unaddressed error
//   - THIS detector: the ACCELERATION dynamic -- shrinking diagnostic gap between
//     consecutive errors and the next mutation, specifically the absence of any
//     read/analysis tool call between error recovery attempts.

import (
	"fmt"
	"strings"
)

// errorRushState tracks the agent's error-to-action dynamics.
type errorRushState struct {
	// consecutiveErrors counts consecutive failed tool calls (any type).
	consecutiveErrors int
	// lastErrorOutput captures the output of the most recent error for context.
	lastErrorOutput string
	// hasInterimRead tracks whether a read/analysis tool was used since the last error.
	hasInterimRead bool
	// lastRushStreak snapshots consecutiveErrors at the moment a rush was
	// detected, before recordToolCall resets the counter on the successful
	// mutation. check() formats the first warning from this snapshot (#149)
	// so it doesn't read the already-reset zero.
	lastRushStreak int
	// rushCount counts how many times the agent did a blind-fix (edit after errors with no read).
	rushCount int
	// warnCount caps total warnings per run.
	warnCount int
}

const (
	errorRushConsecutiveThreshold = 2   // 2+ consecutive errors before checking
	errorRushMaxTotalWarns        = 2   // cap total warnings per run
	errorSnippetMaxLen            = 200 // max chars of error output to include in guidance
)

func newErrorRushState() *errorRushState {
	return &errorRushState{}
}

func (s *errorRushState) reset() {
	s.consecutiveErrors = 0
	s.lastErrorOutput = ""
	s.hasInterimRead = false
	s.rushCount = 0
	s.warnCount = 0
}

// errorRushIsDiagnostic returns true for tools that read/analyze (not mutate).
func errorRushIsDiagnostic(toolName string) bool {
	switch toolName {
	case "read_file", "multi_file_read", "grep", "search_files", "glob",
		"lsp_hover", "lsp_definition", "lsp_references", "lsp_symbols",
		"lsp_diagnostics", "list_directory", "code_search", "lsp_document_highlights":
		return true
	default:
		return false
	}
}

// errorRushIsMutation returns true for tools that modify files or state.
// Backed by the canonical agentMutationEditTools set (#639) so the edit-tool
// list can no longer drift between solution_fixation / error_rush /
// momentum_loss (multi_file_edit was missing here before, multi_file_write /
// batch_replace / lsp_rename were present here but absent from the others).
func errorRushIsMutation(toolName string) bool {
	return agentMutationEditTools[toolName]
}

// errorRushNonCodeErrorMarkers: error-output markers indicating the failure
// is NOT a code-level failure (build/test/edit) but an environmental or
// authorization one: permission denied by the user, tool/MCP/LSP unavailable
// or timed out. These do not count toward the error streak because the
// documented detector scope is "build errors, test failures, edit errors" —
// counting permission rejections would let two denied approvals followed by
// a normal edit fire a false "blind-fixing" warning (#640).
//
// #653: the table must stay NARROW — tool/framework-level identifiers only.
// Generic infrastructure substrings ("timed out", "timeout", "connection
// refused", "server error", "service unavailable", "no such host", ...) were
// removed because they routinely appear inside REAL build/test failure output
// (e.g. "panic: test timed out after 10m0s", "dial tcp ...: connect:
// connection refused" from a service that failed to start). Matching those
// made the streak blind to exactly the detector's core scenario: the agent
// blindly "fixing" flaky/network-dependent test failures.
var errorRushNonCodeErrorMarkers = []string{
	// user/authorization denials at the tool-framework level
	"permission denied",
	"permission request denied",
	"operation not permitted",
	"user denied",
	"user declined",
	"request denied",
	"denied by user",
	"declined by user",
	"not authorized",
	"unauthorized",
	"authentication failed",
	"invalid api key",
	"rate limit",
	"too many requests",
	// tool/MCP/LSP framework availability markers
	"tool unavailable",
	"mcp server unavailable",
	"mcp timeout",
	"mcp server timeout",
	"mcp server not running",
	"lsp unavailable",
	"lsp server not running",
}

// errorRushIsNonCodeError reports whether an error output indicates an
// environmental/authorization failure rather than a code-level one (#640).
// Conservative substring match on the lowercased output; the marker table is
// restricted to tool-framework identifiers so real command output (build/test
// failures quoting network or timeout text) still feeds the streak (#653).
func errorRushIsNonCodeError(output string) bool {
	if output == "" {
		return false
	}
	lowered := strings.ToLower(output)
	for _, marker := range errorRushNonCodeErrorMarkers {
		if strings.Contains(lowered, marker) {
			return true
		}
	}
	return false
}

// recordToolCall processes each tool execution result.
func (s *errorRushState) recordToolCall(toolName string, output string, isError bool) {
	if isError {
		// #640: only CODE-level failures (build/test/edit errors) feed the
		// streak. Permission denials, tool/MCP/LSP unavailability and network
		// timeouts are environment/authorization failures — they say nothing
		// about the agent rushing fixes and must not fill the streak ahead of
		// a legitimate edit (documented scope: "build errors, test failures,
		// edit errors").
		if errorRushIsNonCodeError(output) {
			return
		}
		s.consecutiveErrors++
		s.lastErrorOutput = output
		s.hasInterimRead = false
		return
	}

	// Successful tool call
	if errorRushIsDiagnostic(toolName) {
		s.hasInterimRead = true
	}

	// If this is a mutation after consecutive errors without any diagnostic read,
	// that's a "blind fix" / panic rush.
	if errorRushIsMutation(toolName) && s.consecutiveErrors >= errorRushConsecutiveThreshold && !s.hasInterimRead {
		s.rushCount++
		// #149: snapshot the streak NOW — the reset below would otherwise
		// zero it before check() formats the first warning.
		s.lastRushStreak = s.consecutiveErrors
	}

	// A successful non-error tool resets the error streak
	s.consecutiveErrors = 0
}

// check returns guidance if the panic-rush pattern is detected.
func (s *errorRushState) check() string {
	if s.warnCount >= errorRushMaxTotalWarns {
		return ""
	}
	if s.rushCount < 1 {
		return ""
	}

	// Build context from the last error output (truncated)
	errSnippet := ""
	if s.lastErrorOutput != "" {
		lines := strings.Split(strings.TrimSpace(s.lastErrorOutput), "\n")
		// Find the most relevant line (contains "error" or "fail")
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			lowered := strings.ToLower(trimmed)
			if strings.Contains(lowered, "error") ||
				strings.Contains(lowered, "fail") ||
				strings.Contains(lowered, "undefined") {
				errSnippet = trimmed
				break
			}
		}
		// Fallback to first non-empty line
		if errSnippet == "" && len(lines) > 0 {
			errSnippet = strings.TrimSpace(lines[0])
		}
		// Truncate
		if len(errSnippet) > errorSnippetMaxLen {
			errSnippet = errSnippet[:errorSnippetMaxLen] + "..."
		}
	}

	s.warnCount++

	const firstWarnFormat = "[error-rush] Editing after %d consecutive error(s) without analysis. Re-read error and code before next edit."

	const repeatWarnFormat = "[error-rush] %dth blind-fix attempt. STOP editing. Read the error and understand why prior fix failed."

	var guidance string
	if s.rushCount > 1 {
		guidance = fmt.Sprintf(repeatWarnFormat, s.rushCount+1)
	} else {
		// #149: consecutiveErrors was reset by the triggering mutation's
		// recordToolCall; use the snapshot taken at detection time instead.
		guidance = fmt.Sprintf(firstWarnFormat, s.lastRushStreak)
	}

	if errSnippet != "" {
		guidance += "\nLast error: " + errSnippet
	}

	return guidance
}
