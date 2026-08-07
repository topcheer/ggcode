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
func errorRushIsMutation(toolName string) bool {
	switch toolName {
	case "edit_file", "write_file", "multi_edit_file", "multi_file_write",
		"notebook_edit", "batch_replace", "lsp_rename":
		return true
	default:
		return false
	}
}

// recordToolCall processes each tool execution result.
func (s *errorRushState) recordToolCall(toolName string, output string, isError bool) {
	if isError {
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

	const firstWarnFormat = "[Error Rush / Panic Coding] You are issuing edits immediately after %d consecutive error(s) " +
		"without reading or analyzing the error output in between. " +
		"Research (Agentic Overconfidence, arXiv 2026; AgentDiet, FSE 2026) shows this \"blind-fixing\" " +
		"pattern compounds errors: fixes based on incomplete error understanding rarely succeed. " +
		"SLOW DOWN. Before your next edit: (1) re-read the error message carefully, " +
		"(2) read_file the relevant code section to understand the actual state, " +
		"(3) verify your fix targets the root cause, not the symptom."

	const repeatWarnFormat = "[Error Rush / Panic Coding] This is your %dth+ blind-fix attempt after errors. " +
		"Repeatedly editing without diagnosis between failures is a known failure mode " +
		"(Agentic Overconfidence, arXiv 2026). STOP editing. Read the error. " +
		"Read the file. Understand WHY the previous fix failed before trying another."

	var guidance string
	if s.rushCount > 1 {
		guidance = fmt.Sprintf(repeatWarnFormat, s.rushCount+1)
	} else {
		guidance = fmt.Sprintf(firstWarnFormat, s.consecutiveErrors)
	}

	if errSnippet != "" {
		guidance += "\nLast error: " + errSnippet
	}

	return guidance
}
