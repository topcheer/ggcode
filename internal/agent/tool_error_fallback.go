package agent

// Tool Error Fallback Chain - Actionable Recovery Suggestions on Tool Failure
//
// Research basis: "ACE: AI Context Engineering" (ICLR 2026) and error-recovery
// studies show that when tools fail, AI agents waste 2-4 iterations figuring out
// what to try next - they re-read the error, attempt the same approach with minor
// tweaks, or give up. Production frameworks (LangChain, AutoGen, CrewAI) embed
// fallback strategies into tool wrappers. Claude Code and Cursor leave this to
// the model's own judgment, which is unreliable and costly.
//
// Gap in existing ggcode systems:
//   - transient_retry.go: silently retries the SAME tool on transient errors.
//     Doesn't suggest ALTERNATIVE tools when the failure is structural.
//   - error_classifier.go: classifies error TYPE (syntax, permission, etc.) and
//     fires once per category. Doesn't map failures to specific fallback actions.
//   - recurring_error.go: detects when the same build error persists across
//     edits. Reactive - fires only after 2+ identical failures.
//   - empty_search_spiral: detects consecutive empty searches. Only covers
//     search tools, and fires after multiple consecutive failures.
//   - error_streak: detects consecutive failures across any tools. But gives
//     generic "step back and think" guidance, not actionable alternatives.
//
// This component provides DETERMINISTIC, tool-specific fallback suggestions on
// the FIRST failure, mapping each tool's common failure modes to concrete
// alternative actions the agent can take immediately. This saves iterations by
// giving the agent a recovery path without trial-and-error.
//
// Design:
//   - Zero LLM cost (deterministic lookup table + heuristics)
//   - Fires on first failure per tool per run (subsequent failures already have
//     the suggestion in context)
//   - Tool-specific: each tool has tailored fallbacks based on its common
//     failure modes
//   - Error-content-aware: parses the error to pick the most relevant fallback
//   - Non-blocking: suggestion appended to error result, agent proceeds

import (
	"encoding/json"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

// toolFallbackState tracks which tools have already received fallback suggestions
// in the current run, so each tool only gets a suggestion once.
type toolFallbackState struct {
	mu    sync.Mutex
	fired map[string]bool // tool name → already fired
}

func newToolFallbackState() *toolFallbackState {
	return &toolFallbackState{
		fired: make(map[string]bool),
	}
}

func (t *toolFallbackState) reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.fired = make(map[string]bool)
}

// fallbackRule defines a conditional fallback suggestion for a specific tool.
// The match function receives the error content and returns true if this rule
// applies. The suggestion is the actionable guidance text.
type fallbackRule struct {
	match      func(errorContent string) bool
	suggestion string
}

// fallbackRules maps tool names to ordered lists of fallback rules.
// The first matching rule wins. A default rule (always-matching) at the end
// provides a generic fallback if no specific pattern matches.
var fallbackRules = map[string][]fallbackRule{
	"grep": {
		{
			match: func(e string) bool {
				return strings.Contains(e, "no match") || strings.Contains(e, "no files") || strings.Contains(e, "0 match")
			},
			suggestion: "[fallback] grep returned no matches. Try: (1) search_files with a broader regex or different directory, " +
				"(2) code_search with a natural-language description of what you're looking for, " +
				"(3) glob to find files by name pattern first, then grep within specific files.",
		},
		{
			match: func(e string) bool {
				return strings.Contains(e, "invalid") || strings.Contains(e, "malformed") || strings.Contains(e, "regex")
			},
			suggestion: "[fallback] grep regex pattern is invalid. Simplify the pattern, escape special characters, " +
				"or use search_files which accepts simpler patterns.",
		},
	},
	"search_files": {
		{
			match: func(e string) bool {
				return strings.Contains(e, "no match") || strings.Contains(e, "no files") || strings.Contains(e, "0 match") || strings.Contains(e, "no results")
			},
			suggestion: "[fallback] search_files returned no results. Try: (1) broaden the regex pattern, " +
				"(2) search a parent directory, (3) use code_search for semantic/conceptual matching, " +
				"(4) use glob to discover files by name pattern.",
		},
	},
	"code_search": {
		{
			match: func(e string) bool {
				return strings.Contains(e, "no match") || strings.Contains(e, "no results") || strings.Contains(e, "no relevant")
			},
			suggestion: "[fallback] code_search found no relevant files. Try: (1) rephrase the query with different terminology, " +
				"(2) use grep or search_files with specific keywords, (3) use glob to list files and inspect manually.",
		},
		{
			match: func(e string) bool {
				return strings.Contains(e, "index") || strings.Contains(e, "not ready") || strings.Contains(e, "building")
			},
			suggestion: "[fallback] code_search index is not ready. Use grep or search_files for immediate results — " +
				"the index will rebuild shortly.",
		},
	},
	"glob": {
		{
			match: func(e string) bool {
				return strings.Contains(e, "no match") || strings.Contains(e, "no files") || strings.Contains(e, "0 match") || strings.Contains(e, "no results")
			},
			suggestion: "[fallback] glob found no matching files. Try: (1) broaden the pattern (use ** for deeper nesting), " +
				"(2) use list_directory to explore the directory structure, (3) check the base directory path is correct.",
		},
	},
	"lsp_definition": {
		{
			match: func(e string) bool {
				return strings.Contains(e, "no definition") || strings.Contains(e, "not found") || strings.Contains(e, "no results")
			},
			suggestion: "[fallback] lsp_definition found no definition. Try: (1) lsp_workspace_symbols to search by name, " +
				"(2) lsp_references to find usages, (3) grep for the symbol name across the codebase.",
		},
		{
			match: func(e string) bool {
				return strings.Contains(e, "not ready") || strings.Contains(e, "indexing") || strings.Contains(e, "crashed") || strings.Contains(e, "starting")
			},
			suggestion: "[fallback] LSP server not ready. Try: (1) grep for the symbol as a fallback, (2) search_files, " +
				"(3) retry lsp_definition in a moment — the server may still be indexing.",
		},
	},
	"lsp_references": {
		{
			match: func(e string) bool {
				return strings.Contains(e, "no reference") || strings.Contains(e, "no results") || strings.Contains(e, "not found")
			},
			suggestion: "[fallback] lsp_references found no references. Try: (1) grep for the exact symbol name, " +
				"(2) search_files with the name as pattern, (3) lsp_workspace_symbols to verify the symbol exists.",
		},
	},
	"lsp_hover": {
		{
			match: func(e string) bool {
				return strings.Contains(e, "no hover") || strings.Contains(e, "no info") || strings.Contains(e, "not found") || strings.Contains(e, "no results")
			},
			suggestion: "[fallback] lsp_hover returned no type info. Try: (1) lsp_definition to jump to the declaration, " +
				"(2) grep for the symbol to find its definition manually.",
		},
	},
	"lsp_workspace_symbols": {
		{
			match: func(e string) bool {
				return strings.Contains(e, "no symbol") || strings.Contains(e, "no results") || strings.Contains(e, "not found")
			},
			suggestion: "[fallback] lsp_workspace_symbols found nothing. Try: (1) simplify or shorten the query, " +
				"(2) grep for partial name matches, (3) glob for files with similar names.",
		},
	},
	"lsp_diagnostics": {
		{
			match: func(e string) bool {
				return strings.Contains(e, "not ready") || strings.Contains(e, "indexing") || strings.Contains(e, "crashed") || strings.Contains(e, "starting")
			},
			suggestion: "[fallback] LSP not ready for diagnostics. Try: (1) run the build/lint command directly to get compiler errors, " +
				"(2) retry lsp_diagnostics shortly.",
		},
	},
	"web_fetch": {
		{
			match: func(e string) bool {
				return strings.Contains(e, "timeout") || strings.Contains(e, "timed out") || strings.Contains(e, "deadline")
			},
			suggestion: "[fallback] web_fetch timed out. Try: (1) retry with a simpler URL, (2) use web_search to find a cached or mirror version, " +
				"(3) use browser tool for interactive/large pages.",
		},
		{
			match: func(e string) bool {
				return strings.Contains(e, "403") || strings.Contains(e, "forbidden") || strings.Contains(e, "blocked")
			},
			suggestion: "[fallback] web_fetch was blocked. Try: (1) use the browser tool which renders JavaScript and handles auth, " +
				"(2) web_search for the content from a different source.",
		},
		{
			match: func(e string) bool {
				return strings.Contains(e, "404") || strings.Contains(e, "not found")
			},
			suggestion: "[fallback] web_fetch URL not found (404). Verify the URL is correct, or use web_search to find the current page location.",
		},
	},
	"web_search": {
		{
			match: func(e string) bool {
				return strings.Contains(e, "rate limit") || strings.Contains(e, "429") || strings.Contains(e, "too many")
			},
			suggestion: "[fallback] web_search rate limited. Try: (1) wait and retry in a moment, (2) use web_fetch on a known URL directly, " +
				"(3) simplify the query.",
		},
	},
	"read_file": {
		{
			match: func(e string) bool {
				return strings.Contains(e, "not found") || strings.Contains(e, "no such file") || strings.Contains(e, "does not exist")
			},
			suggestion: "[fallback] read_file: file not found. Try: (1) glob to find the correct path, (2) list_directory to explore, " +
				"(3) check for typos or case sensitivity in the filename.",
		},
	},
	"run_command": {
		{
			match: func(e string) bool {
				return strings.Contains(e, "command not found") || strings.Contains(e, "not found") && strings.Contains(e, "executable")
			},
			suggestion: "[fallback] run_command: command not found. Try: (1) check the command name/spelling, " +
				"(2) use 'which <cmd>' or 'command -v <cmd>' to verify it's installed, (3) check PATH.",
		},
		{
			match: func(e string) bool {
				return strings.Contains(e, "permission denied")
			},
			suggestion: "[fallback] run_command: permission denied. Check file permissions, or try with appropriate privileges. " +
				"For build/test commands, ensure the working directory and build artifacts are accessible.",
		},
	},
	"edit_file": {
		{
			match: func(e string) bool {
				return strings.Contains(e, "not unique") || strings.Contains(e, "multiple match") || strings.Contains(e, "ambiguous")
			},
			suggestion: "[fallback] edit_file: old_text is not unique. Add 2-3 more surrounding lines from read_file output to anchor the edit, " +
				"or use replace_all=true if all occurrences should be changed.",
		},
		{
			match: func(e string) bool {
				return strings.Contains(e, "not found") || strings.Contains(e, "no match") || strings.Contains(e, "does not match")
			},
			suggestion: "[fallback] edit_file: old_text not found in file. The file may have changed — re-read it with read_file, " +
				"then copy the exact text (including whitespace) for old_text.",
		},
	},
	"git_diff": {
		{
			match: func(e string) bool {
				return strings.Contains(e, "not a git") || strings.Contains(e, "not a repository")
			},
			suggestion: "[fallback] git_diff: not in a git repository. Check the working directory, or use run_command 'git status' to diagnose.",
		},
	},
	// Default fallback for any tool not explicitly handled
	"_default": {
		{
			match: func(e string) bool { return true },
			suggestion: "[fallback] Consider an alternative approach: use a different tool, verify input parameters, " +
				"or simplify the request.",
		},
	},
}

// maybeFallbackSuggestion checks whether a fallback suggestion should be injected
// for the given tool failure. Returns the suggestion text, or "" if none applies.
// Each tool gets at most one suggestion per run.
func (t *toolFallbackState) maybeFallbackSuggestion(toolName, errorContent string) string {
	t.mu.Lock()
	if t.fired[toolName] {
		t.mu.Unlock()
		return ""
	}
	t.fired[toolName] = true
	t.mu.Unlock()

	rules, ok := fallbackRules[toolName]
	if !ok {
		rules = fallbackRules["_default"]
	}

	lowerContent := strings.ToLower(errorContent)
	for _, rl := range rules {
		if rl.match(lowerContent) {
			debug.Log("tool-fallback", "fallback suggestion for %s: %s",
				toolName, truncateString(rl.suggestion, 80))
			return rl.suggestion
		}
	}

	// Should not reach here since _default always matches, but be safe.
	return ""
}

// extractToolNameFromJSON safely extracts the tool name from tool call arguments.
func extractToolNameFromJSON(args []byte) string {
	if len(args) == 0 {
		return ""
	}
	var m map[string]interface{}
	if err := json.Unmarshal(args, &m); err != nil {
		return ""
	}
	if name, ok := m["name"].(string); ok {
		return name
	}
	return ""
}

// --- Agent integration methods ---

// toolFallbackCheck evaluates a failed tool result and returns an actionable
// fallback suggestion if one is available. Called after all other error hooks
// so the suggestion is the last (most actionable) piece of guidance.
func (a *Agent) toolFallbackCheck(toolName, errorContent string) string {
	if a.toolFallback == nil {
		return ""
	}
	if errorContent == "" {
		return ""
	}
	return a.toolFallback.maybeFallbackSuggestion(toolName, errorContent)
}

// resetToolFallback clears state for a new run.
func (a *Agent) resetToolFallback() {
	if a.toolFallback != nil {
		a.toolFallback.reset()
	}
}
