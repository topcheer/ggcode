package agent

import (
	"strings"
)

func truncationAdvisory(toolName string, originalLen int) string {
	return truncationAdvisoryWithGuard(toolName, originalLen, 0)
}

// truncationAdvisoryWithGuard renders the post-guard advisory. When the
// guard byte limit is known (>0) the advice names the constraint that
// actually bound (#5268 item 3): a context-pressure byte cap applied AFTER
// the tool's own line window - so for read_file the remedy is a narrower
// per-call window or the spilled full copy, not repeating the same
// offset/limit call that was just truncated.
func truncationAdvisoryWithGuard(toolName string, originalLen, guardLimit int) string {
	sizeStr := formatBytes(originalLen)
	var advice string

	if guardLimit > 0 && (toolName == "read_file" || toolName == "multi_file_read") {
		return "\n[Truncation advisory: output was " + sizeStr +
			" and was cut by the context-pressure byte cap (" + formatBytes(guardLimit) +
			"), not by your line window - the tool already applied offset/limit. " +
			"Re-calling read_file with the same window returns the same bytes and truncates again. " +
			"Either request a narrower window (smaller limit) per call, or read the spilled full copy " +
			"named above with offset/limit to page through the omitted middle.]"
	}

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

	case "task_output", "a2a_get_task":

		if toolName == "task_output" {
			advice = "Task output (" + sizeStr + ") was truncated. task_output returns the same snapshot on every call — " +
				"re-running it will not yield more. Re-call once with the tail_lines parameter to page through the end, " +
				"or wait for auto-compact to free context."
		} else {
			advice = "Task status (" + sizeStr + ") was truncated. This is a deterministic snapshot of the same task — " +
				"re-running with identical arguments returns the same bytes. Wait for auto-compact or use a2a_list_tasks pagination instead."
		}

	default:

		switch {
		case strings.HasPrefix(toolName, "mcp__"):

			advice = "MCP tool output (" + sizeStr + ") was truncated under context pressure. " +
				"The tool's parameters are defined by the MCP server schema — if none of them can narrow the result, " +
				"re-calling will return the same content and be truncated again. Prefer a different tool for the specific " +
				"information you need, or wait for auto-compact to relieve context pressure."
		default:
			advice = "Tool output was " + sizeStr + " and has been truncated due to context pressure. " +
				"Re-run the tool with tighter scope or use a more targeted approach to retrieve only the relevant section."
		}
	}

	return "\n[Truncation advisory: " + advice + "]"
}

func advisoryForTruncation(toolName string, originalLen int) string {
	return advisoryForTruncationGuard(toolName, originalLen, 0)
}

func advisoryForTruncationGuard(toolName string, originalLen, guardLimit int) string {
	if originalLen < 8*1024 {
		return ""
	}
	return truncationAdvisoryWithGuard(toolName, originalLen, guardLimit)
}

func withTruncationAdvisory(truncatedContent, toolName string, originalLen int) string {
	return withTruncationAdvisoryGuard(truncatedContent, toolName, originalLen, 0)
}

func withTruncationAdvisoryGuard(truncatedContent, toolName string, originalLen, guardLimit int) string {
	advisory := advisoryForTruncationGuard(toolName, originalLen, guardLimit)
	if advisory == "" {
		return truncatedContent
	}
	if strings.HasSuffix(truncatedContent, advisory) {
		return truncatedContent
	}
	return truncatedContent + advisory
}
