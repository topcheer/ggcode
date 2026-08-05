package agent

// Search Parameter Quality Guard — Tool Use Optimization
//
// Research basis: Anthropic's "Tool Use Best Practices" (2025) emphasizes that
// tool parameter quality directly impacts agent efficiency. Vague or overly broad
// search parameters produce large result sets that consume context budget without
// providing actionable information — a "context waste pattern" in the ACE framework
// (ICLR 2026). The Tool Learning Survey (Qin et al. 2024) identifies parameter
// refinement as a key tool-use skill.
//
// Competitor analysis:
//   - Claude Code: no search parameter quality check
//   - Cursor: relies on editor state, not applicable to CLI agents
//   - OpenHands: no parameter quality guard
//   - Aider: minimal tool surface, searches done by user
//
// Gap: ggcode already has empty_search_spiral (detects when searches return
// nothing) and tool_output_guard (truncates large results). But NEITHER checks
// the parameter BEFORE execution to warn the agent that its search is too broad.
// This guard catches the problem at parameter-analysis time, nudging the agent to
// narrow its search before the result floods the context window.
//
// Design:
//   - Checks grep patterns for overly broad regexes (e.g., ".*", single chars)
//   - Checks search_files for too-short queries (single char, empty)
//   - Checks glob for overly broad patterns (e.g., "*", "**/*")
//   - Checks code_search for too-short natural-language queries
//   - Non-blocking: guidance appended to tool result, execution proceeds
//   - Fires at most 3 times per run (avoids nagging)
//   - Zero LLM cost — pure deterministic pattern matching

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	searchParamGuardMaxFires = 3
)

type searchParamGuardState struct {
	fires int
}

func newSearchParamGuard() *searchParamGuardState {
	return &searchParamGuardState{}
}

func (s *searchParamGuardState) reset() {
	s.fires = 0
}

// checkParamQuality inspects search tool parameters for overly broad patterns.
// Returns a non-empty guidance string if the parameters are too vague/broad.
func (s *searchParamGuardState) checkParamQuality(toolName string, args []byte) string {
	if s.fires >= searchParamGuardMaxFires {
		return ""
	}

	var hint string
	switch toolName {
	case "grep":
		hint = checkGrepParams(args)
	case "search_files":
		hint = checkSearchParams(args)
	case "glob":
		hint = checkGlobParams(args)
	case "code_search":
		hint = checkCodeSearchParams(args)
	default:
		return ""
	}

	if hint != "" {
		s.fires++
	}
	return hint
}

// checkGrepParams validates grep search patterns.
func checkGrepParams(args []byte) string {
	pattern := extractJSONStringFieldRaw(args, "pattern")
	if pattern == "" {
		return ""
	}

	// Normalize for analysis
	trimmed := strings.TrimSpace(pattern)

	// Pattern is just wildcards: ".*", "*", "."
	stripped := strings.ReplaceAll(trimmed, ".", "")
	stripped = strings.ReplaceAll(stripped, "*", "")
	stripped = strings.TrimSpace(stripped)
	if stripped == "" {
		return "Hint: This grep pattern matches everything (only wildcards). Consider narrowing to a specific string or identifier to avoid flooding context with results."
	}

	// Single literal character (not a regex metachar)
	if len(trimmed) == 1 && !isRegexMetachar(trimmed[0]) {
		return fmt.Sprintf("Hint: The grep pattern '%s' is very short and will match many lines. Consider using a longer, more specific pattern to reduce noise.", trimmed)
	}

	// Two characters with at least one wildcard
	if len(trimmed) <= 2 && (strings.ContainsAny(trimmed, ".*+?")) {
		return fmt.Sprintf("Hint: The grep pattern '%s' is very broad. Consider a more specific pattern to avoid excessive results.", trimmed)
	}

	return ""
}

// checkSearchParams validates search_files queries.
func checkSearchParams(args []byte) string {
	pattern := extractJSONStringFieldRaw(args, "pattern")

	trimmed := strings.TrimSpace(pattern)

	if len(trimmed) == 0 {
		return "Hint: Empty search pattern will match all files. Provide a specific pattern to get useful results."
	}

	if len(trimmed) == 1 {
		return fmt.Sprintf("Hint: The search pattern '%s' is a single character and will match many files. Use a longer pattern for better results.", trimmed)
	}

	return ""
}

// checkGlobParams validates glob patterns.
func checkGlobParams(args []byte) string {
	pattern := extractJSONStringFieldRaw(args, "pattern")
	if pattern == "" {
		return ""
	}

	trimmed := strings.TrimSpace(pattern)

	// Patterns that match everything
	switch trimmed {
	case "*", "**/*", "**", "*.*":
		return fmt.Sprintf("Hint: The glob pattern '%s' matches all files. Consider narrowing to a specific extension or path prefix.", trimmed)
	}

	return ""
}

// checkCodeSearchParams validates code_search natural-language queries.
func checkCodeSearchParams(args []byte) string {
	query := extractJSONStringFieldRaw(args, "query")
	if query == "" {
		return ""
	}

	trimmed := strings.TrimSpace(query)

	if len(trimmed) == 0 {
		return "Hint: Empty code_search query. Provide a descriptive query to get relevant results."
	}

	if len(trimmed) <= 2 {
		return fmt.Sprintf("Hint: The code_search query '%s' is very short. Use a more descriptive query (e.g., 'authentication token refresh') for better results.", trimmed)
	}

	return ""
}

// extractJSONStringFieldRaw extracts a string field from JSON args without
// depending on memoize's extractJSONStringField (different signature expectations).
func extractJSONStringFieldRaw(args []byte, field string) string {
	if len(args) == 0 {
		return ""
	}
	var m map[string]interface{}
	if err := json.Unmarshal(args, &m); err != nil {
		return ""
	}
	v, ok := m[field]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}

// isRegexMetachar checks if a byte is a regex metacharacter.
func isRegexMetachar(b byte) bool {
	switch b {
	case '.', '*', '+', '?', '^', '$', '(', ')', '[', ']', '{', '}', '|', '\\':
		return true
	}
	return false
}
