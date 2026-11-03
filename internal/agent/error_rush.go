package agent

import (
	"fmt"
	"strings"
)

type errorRushState struct {
	consecutiveErrors int
	lastErrorOutput   string
	hasInterimRead    bool
	lastRushStreak    int
	rushCount         int
	warnCount         int
}

const (
	errorRushConsecutiveThreshold = 2
	errorRushMaxTotalWarns        = 1
	errorSnippetMaxLen            = 200
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

func errorRushIsMutation(toolName string) bool {
	return agentMutationEditTools[toolName]
}

var errorRushNonCodeErrorMarkers = []string{

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

	"tool unavailable",
	"mcp server unavailable",
	"mcp timeout",
	"mcp server timeout",
	"mcp server not running",
	"lsp unavailable",
	"lsp server not running",
}

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

func (s *errorRushState) recordToolCall(toolName string, output string, isError bool) {
	if isError {

		if errorRushIsNonCodeError(output) {
			return
		}
		s.consecutiveErrors++
		s.lastErrorOutput = output
		s.hasInterimRead = false
		return
	}

	if errorRushIsDiagnostic(toolName) {
		s.hasInterimRead = true
	}

	if errorRushIsMutation(toolName) && s.consecutiveErrors >= errorRushConsecutiveThreshold && !s.hasInterimRead {
		s.rushCount++

		s.lastRushStreak = s.consecutiveErrors
	}

	s.consecutiveErrors = 0
}

func (s *errorRushState) check() string {
	if s.warnCount >= errorRushMaxTotalWarns {
		return ""
	}
	if s.rushCount < 1 {
		return ""
	}

	errSnippet := ""
	if s.lastErrorOutput != "" {
		lines := strings.Split(strings.TrimSpace(s.lastErrorOutput), "\n")

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

		if errSnippet == "" && len(lines) > 0 {
			errSnippet = strings.TrimSpace(lines[0])
		}

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

		guidance = fmt.Sprintf(firstWarnFormat, s.lastRushStreak)
	}

	if errSnippet != "" {
		guidance += "\nLast error: " + errSnippet
	}

	return guidance
}
