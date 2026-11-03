package agent

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

func checkGrepParams(args []byte) string {
	pattern := extractJSONStringFieldRaw(args, "pattern")
	if pattern == "" {
		return ""
	}

	trimmed := strings.TrimSpace(pattern)

	stripped := strings.ReplaceAll(trimmed, ".", "")
	stripped = strings.ReplaceAll(stripped, "*", "")
	stripped = strings.TrimSpace(stripped)
	if stripped == "" {
		return "Hint: This grep pattern matches everything (only wildcards). Consider narrowing to a specific string or identifier to avoid flooding context with results."
	}

	if len(trimmed) == 1 && !isRegexMetachar(trimmed[0]) {
		return fmt.Sprintf("Hint: The grep pattern '%s' is very short and will match many lines. Consider using a longer, more specific pattern to reduce noise.", trimmed)
	}

	if len(trimmed) <= 2 && (strings.ContainsAny(trimmed, ".*+?")) {
		return fmt.Sprintf("Hint: The grep pattern '%s' is very broad. Consider a more specific pattern to avoid excessive results.", trimmed)
	}

	return ""
}

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

func checkGlobParams(args []byte) string {
	pattern := extractJSONStringFieldRaw(args, "pattern")
	if pattern == "" {
		return ""
	}

	trimmed := strings.TrimSpace(pattern)

	switch trimmed {
	case "*", "**/*", "**", "*.*":
		return fmt.Sprintf("Hint: The glob pattern '%s' matches all files. Consider narrowing to a specific extension or path prefix.", trimmed)
	}

	return ""
}

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

func isRegexMetachar(b byte) bool {
	switch b {
	case '.', '*', '+', '?', '^', '$', '(', ')', '[', ']', '{', '}', '|', '\\':
		return true
	}
	return false
}
