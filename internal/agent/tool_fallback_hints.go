package agent

import (
	"strings"
)

func toolFallbackHintOnSuccess(toolName string) bool {
	switch toolName {
	case "grep", "search_files", "code_search",
		"lsp_workspace_symbols", "lsp_references", "lsp_implementation":
		return true
	}
	return false
}

func toolFallbackHint(toolName, errorContent string) string {
	if toolName == "" || errorContent == "" {
		return ""
	}

	lower := strings.ToLower(errorContent)

	switch {

	case isLSPTool(toolName):
		return lspFallbackHint(lower)

	case toolName == "grep" || toolName == "search_files":
		if isZeroMatchSuccess(toolName, errorContent) {
			return "\n[Hint: No matches found. Try: (1) broaden the pattern with wildcards, (2) use code_search for semantic matching, (3) use glob to find files by name pattern, (4) check if the file extension filter is too narrow.]"
		}
		if isTimeout(lower) {
			return "\n[Hint: Search timed out. Try narrowing the search scope (specific directory) or use glob to find files first, then read specific files.]"
		}

	case toolName == "code_search":
		if isZeroMatchSuccess(toolName, errorContent) {
			return "\n[Hint: Semantic search found nothing. Try: (1) grep with exact keywords, (2) glob for file name patterns, (3) list_directory to explore the structure.]"
		}

	case toolName == "read_file":
		if strings.Contains(lower, "permission denied") || strings.Contains(lower, "not permitted") {
			return "\n[Hint: Permission denied. The file may be protected. Try run_command with 'cat <file>' if shell access is available, or check file permissions.]"
		}

	case toolName == "edit_file" || toolName == "write_file" || toolName == "multi_edit_file":
		if strings.Contains(lower, "old_text") || strings.Contains(lower, "not found in file") || strings.Contains(lower, "does not match") {
			return "\n[Hint: Edit anchor not found. The file may have changed since last read. Re-read the file with read_file to get current content, then retry the edit with exact lines.]"
		}

	case toolName == "web_fetch":
		if isNetworkError(lower) {
			return "\n[Hint: Web fetch failed. Try: (1) web_search to find alternative URLs, (2) retry after a few seconds for transient errors, (3) use browser tool for JavaScript-heavy pages.]"
		}
	case toolName == "web_search":
		if isRateLimited(lower) {
			return "\n[Hint: Search rate limited. Wait a moment and try with different keywords or fewer results.]"
		}

	case toolName == "git_add" || toolName == "git_commit":
		if strings.Contains(lower, "nothing to commit") || strings.Contains(lower, "no changes") {
			return "\n[Hint: No changes staged. Run git_status to see current state, then git_diff to review modifications.]"
		}

	case toolName == "run_command":
		if isTimeout(lower) {
			return "\n[Hint: Command timed out. For long-running processes (dev servers, watchers, builds), use start_command to run in background, then use read_command_output to check results.]"
		}
	}

	return ""
}

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

const zeroMatchMaxBytes = 512

func isZeroMatchSuccess(toolName, content string) bool {
	switch toolName {
	case "grep", "search_files", "code_search", "lsp_workspace_symbols", "lsp_references", "lsp_implementation":
	default:
		return false
	}
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return true
	}
	if len(trimmed) > zeroMatchMaxBytes {
		return false
	}
	first := trimmed
	if i := strings.IndexByte(first, '\n'); i >= 0 {
		first = first[:i]
	}
	first = strings.ToLower(strings.TrimSpace(first))
	for _, sentinel := range []string{"no matches found", "no match found", "no files matched", "no results", "no symbols found"} {
		if strings.HasPrefix(first, sentinel) && len(first) <= len(sentinel)+64 {
			return true
		}
	}
	return false
}

func isTimeout(s string) bool {
	return strings.Contains(s, "timeout") || strings.Contains(s, "timed out") ||
		strings.Contains(s, "deadline exceeded")
}

func isNetworkError(s string) bool {
	return strings.Contains(s, "connection") || strings.Contains(s, "dns") ||
		strings.Contains(s, "network") || strings.Contains(s, "timeout") ||
		strings.Contains(s, "eof") || strings.Contains(s, "unreachable")
}

func isRateLimited(s string) bool {
	return strings.Contains(s, "rate limit") || strings.Contains(s, "too many") ||
		strings.Contains(s, "429") || strings.Contains(s, "throttl")
}
