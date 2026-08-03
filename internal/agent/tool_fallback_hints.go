package agent

// Tool Error Fallback Hints - Context-aware alternative suggestions on tool failures.
//
// Research basis: Claude Code and Cursor both inject "did you mean" style hints
// when tools fail. When an LSP query times out, Claude Code suggests "try
// search_files instead." When grep returns nothing, Cursor suggests "try broader
// patterns or code_search." These hints save 1-2 wasted agent iterations where
// the model blindly retries the same failing tool.
//
// Gap in ggcode: tool errors return raw error messages without suggesting
// alternative approaches. The agent retries the same tool or hallucinates a
// new approach, wasting iterations. This module adds deterministic, zero-LLM-cost
// fallback hints keyed on tool name + error pattern.
//
// Design:
//   - Hints are only appended when result.IsError is true
//   - Hints are context-specific (LSP timeout vs. file not found vs. grep empty)
//   - Hints are short (1-2 lines) to minimize token overhead
//   - Each tool gets at most one hint per error to avoid noise
//   - Hints suggest CONCRETE alternative tools, not generic advice

import (
	"strings"
)

// toolFallbackHint returns a short, context-aware fallback suggestion for a
// failed tool call. Returns empty string if no hint is applicable.
//
// The suggestion is based on the tool name and the error content. It maps known
// failure patterns to concrete alternative tools the agent should try next.
func toolFallbackHint(toolName, errorContent string) string {
	if toolName == "" || errorContent == "" {
		return ""
	}

	lower := strings.ToLower(errorContent)

	switch {
	// LSP tools — suggest search-based alternatives
	case isLSPTool(toolName):
		return lspFallbackHint(lower)

	// Grep/search returning empty — suggest broader search
	case toolName == "grep" || toolName == "search_files":
		if isEmptyToolResult(lower) {
			return "\n[Hint: No matches found. Try: (1) broaden the pattern with wildcards, (2) use code_search for semantic matching, (3) use glob to find files by name pattern, (4) check if the file extension filter is too narrow.]"
		}
		if isTimeout(lower) {
			return "\n[Hint: Search timed out. Try narrowing the search scope (specific directory) or use glob to find files first, then read specific files.]"
		}

	// Code search returning empty — suggest keyword-based alternatives
	case toolName == "code_search":
		if isEmptyToolResult(lower) {
			return "\n[Hint: Semantic search found nothing. Try: (1) grep with exact keywords, (2) glob for file name patterns, (3) list_directory to explore the structure.]"
		}

	// Read file not found — file_suggest.go already handles this, but add
	// a hint for permission errors
	case toolName == "read_file":
		if strings.Contains(lower, "permission denied") || strings.Contains(lower, "not permitted") {
			return "\n[Hint: Permission denied. The file may be protected. Try run_command with 'cat <file>' if shell access is available, or check file permissions.]"
		}

	// Edit/write failures — suggest reading the file first
	case toolName == "edit_file" || toolName == "write_file" || toolName == "multi_edit_file":
		if strings.Contains(lower, "old_text") || strings.Contains(lower, "not found in file") || strings.Contains(lower, "does not match") {
			return "\n[Hint: Edit anchor not found. The file may have changed since last read. Re-read the file with read_file to get current content, then retry the edit with exact lines.]"
		}

	// Web fetch/search failures — suggest alternative approach
	case toolName == "web_fetch":
		if isNetworkError(lower) {
			return "\n[Hint: Web fetch failed. Try: (1) web_search to find alternative URLs, (2) retry after a few seconds for transient errors, (3) use browser tool for JavaScript-heavy pages.]"
		}
	case toolName == "web_search":
		if isRateLimited(lower) {
			return "\n[Hint: Search rate limited. Wait a moment and try with different keywords or fewer results.]"
		}

	// Git operations — suggest checking status first
	case toolName == "git_add" || toolName == "git_commit":
		if strings.Contains(lower, "nothing to commit") || strings.Contains(lower, "no changes") {
			return "\n[Hint: No changes staged. Run git_status to see current state, then git_diff to review modifications.]"
		}

	// Run command — suggest start_command for long-running
	case toolName == "run_command":
		if isTimeout(lower) {
			return "\n[Hint: Command timed out. For long-running processes (dev servers, watchers, builds), use start_command to run in background, then use read_command_output to check results.]"
		}
	}

	return ""
}

// isLSPTool returns true for any LSP-based tool.
func isLSPTool(name string) bool {
	switch name {
	case "lsp_hover", "lsp_definition", "lsp_references", "lsp_implementation",
		"lsp_symbols", "lsp_workspace_symbols", "lsp_diagnostics",
		"lsp_document_highlights", "lsp_code_actions", "lsp_incoming_calls",
		"lsp_outgoing_calls", "lsp_prepare_call_hierarchy", "lsp_rename":
		return true
	}
	return false
}

// lspFallbackHint returns hints for LSP tool failures.
func lspFallbackHint(lowerErr string) string {
	if isTimeout(lowerErr) || strings.Contains(lowerErr, "not ready") ||
		strings.Contains(lowerErr, "server crashed") || strings.Contains(lowerErr, "starting") {
		return "\n[Hint: LSP server unavailable. Try: (1) grep/search_files to find the symbol by name, (2) code_search for semantic lookup, (3) read the file directly and scan for the definition.]"
	}
	if strings.Contains(lowerErr, "no result") || strings.Contains(lowerErr, "not found") {
		return "\n[Hint: LSP found no matches. Try: (1) lsp_workspace_symbols for project-wide search, (2) grep for the exact symbol name, (3) check if you're in the right file.]"
	}
	return ""
}

// isEmptyToolResult checks if the error indicates no results were found.
func isEmptyToolResult(s string) bool {
	return strings.Contains(s, "no match") || strings.Contains(s, "no result") ||
		strings.Contains(s, "no symbol") || strings.Contains(s, "not found") ||
		strings.Contains(s, "empty")
}

// isTimeout checks if the error indicates a timeout.
func isTimeout(s string) bool {
	return strings.Contains(s, "timeout") || strings.Contains(s, "timed out") ||
		strings.Contains(s, "deadline exceeded")
}

// isNetworkError checks for common network error patterns.
func isNetworkError(s string) bool {
	return strings.Contains(s, "connection") || strings.Contains(s, "dns") ||
		strings.Contains(s, "network") || strings.Contains(s, "timeout") ||
		strings.Contains(s, "eof") || strings.Contains(s, "unreachable")
}

// isRateLimited checks for rate limiting patterns.
func isRateLimited(s string) bool {
	return strings.Contains(s, "rate limit") || strings.Contains(s, "too many") ||
		strings.Contains(s, "429") || strings.Contains(s, "throttl")
}
