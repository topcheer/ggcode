package agent

import (
	"strings"
)

// truncation_advisory.go provides tool-specific continuation guidance when
// guardToolOutput truncates a tool result.
//
// Research basis: Anthropic's "Effective Agents" (2025) and "Context Engineering"
// papers emphasize that agents need actionable feedback when context is managed.
// Silent truncation forces agents into blind re-querying (wasteful, often repeats
// the same large call). A tool-specific advisory tells the agent exactly HOW to
// retrieve the missing data efficiently.
//
// Competitor analysis:
//   - Claude Code: shows truncation notice with line count, suggests /more
//   - Cursor: paginates large outputs with "Show More" UI
//   - Aider: avoids truncation by using targeted git diffs
//   - Cline/OpenHands: truncates with a note but no actionable guidance
//
// Our approach: when guardToolOutput truncates, append a tool-specific advisory
// string that recommends the most efficient retrieval strategy for that tool type.
// This is purely mechanical (no LLM cost) and reduces wasteful retry loops.

// truncationAdvisory generates a tool-specific guidance string appended after
// the truncation marker when guardToolOutput truncates a tool result.
// toolName is the name of the tool that produced the output.
// originalLen is the byte length of the original (pre-truncation) output.
func truncationAdvisory(toolName string, originalLen int) string {
	sizeStr := formatBytes(originalLen)
	var advice string

	switch toolName {
	case "run_command", "start_command", "read_command_output", "wait_command":
		advice = "Output was " + sizeStr + " and has been truncated. To retrieve specific information: " +
			"(1) re-run the command with output piped through grep/head/tail, " +
			"(2) if this is a background job, use read_command_output with since_line to page through incrementally, " +
			"(3) redirect output to a file and read_file specific sections with offset/limit."

	case "read_file", "multi_file_read":
		advice = "File content was " + sizeStr + " and has been truncated. " +
			"Use read_file with offset and limit parameters to page through specific sections of the file."

	case "grep", "search_files":
		advice = "Search results (" + sizeStr + ") were truncated. " +
			"Narrow your search: use more specific patterns, reduce max_results, " +
			"or filter by file type (glob/type parameters) to get targeted matches."

	case "code_search":
		advice = "Code search results (" + sizeStr + ") were truncated. " +
			"Refine your query to be more specific, or reduce max_results to get fewer, more relevant matches."

	case "web_fetch":
		advice = "Web page content (" + sizeStr + ") was truncated. " +
			"Use web_search to find specific sections, or use the browser tool for targeted extraction."

	case "code_execution":
		advice = "Execution output (" + sizeStr + ") was truncated. " +
			"Add console.log() statements to print only the specific values you need, " +
			"or write results to a file and read_file the relevant section."

	case "git_diff", "git_show", "git_log":
		advice = "Git output (" + sizeStr + ") was truncated. " +
			"Scope your query: use specific file paths, limit commit count, or use --stat for a summary."

	case "lsp_references", "lsp_definition", "lsp_workspace_symbols", "lsp_implementation":
		advice = "LSP results (" + sizeStr + ") were truncated. " +
			"Narrow your search by specifying a more precise symbol or limiting the scope."

	case "list_directory":
		advice = "Directory listing (" + sizeStr + ") was truncated. " +
			"Use glob with specific patterns to find files, or search_files with content patterns."

	case "browser":
		advice = "Browser output (" + sizeStr + ") was truncated. " +
			"Use browser actions (extract, evaluate, screenshot) to target specific elements instead of full-page content."

	default:
		// Generic advisory for any other tool
		advice = "Tool output was " + sizeStr + " and has been truncated due to context pressure. " +
			"Re-run the tool with tighter scope or use a more targeted approach to retrieve only the relevant section."
	}

	return "\n[Truncation advisory: " + advice + "]"
}

// advisoryForTruncation is called when guardToolOutput truncates a result.
// It returns the advisory string to append, or "" if no advisory is needed
// (e.g., for very small truncations where the head-tail is sufficient).
func advisoryForTruncation(toolName string, originalLen int) string {
	// Only add advisory for outputs that were meaningfully large.
	// Below 8KB, the head-tail preservation already captures most content.
	if originalLen < 8*1024 {
		return ""
	}
	return truncationAdvisory(toolName, originalLen)
}

// withTruncationAdvisory appends a tool-specific advisory to already-truncated
// content. This is the integration point called from the agent loop.
func withTruncationAdvisory(truncatedContent, toolName string, originalLen int) string {
	advisory := advisoryForTruncation(toolName, originalLen)
	if advisory == "" {
		return truncatedContent
	}
	if strings.HasSuffix(truncatedContent, advisory) {
		return truncatedContent // idempotent
	}
	return truncatedContent + advisory
}
