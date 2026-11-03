package agent

import (
	"encoding/json"
	"strings"
)

func hasInlineToolCall(text string) bool {
	if len(text) < 20 {
		return false
	}

	maxScan := len(text)
	if maxScan > 4096 {
		maxScan = 4096
	}
	scanText := text[:maxScan]

	if !strings.Contains(scanText, "\"name\"") &&
		!strings.Contains(scanText, "\"tool\"") &&
		!strings.Contains(scanText, "tool_call") &&
		!strings.Contains(scanText, "function_call") {
		return false
	}

	for i := 0; i < len(scanText); i++ {
		if scanText[i] != '{' {
			continue
		}

		end := findJSONEnd(text, i)
		if end <= i {
			continue
		}
		fragment := text[i : end+1]
		if isToolCallJSON(fragment) {
			return true
		}
	}
	return false
}

func isToolCallJSON(s string) bool {
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return false
	}

	name, hasName := m["name"]
	_, hasTool := m["tool"]
	if !hasName && !hasTool {
		return false
	}

	if hasName {
		if n, ok := name.(string); !ok || n == "" {
			return false
		}
	}

	_, hasArgs := m["arguments"]
	_, hasParams := m["parameters"]
	_, hasInput := m["input"]
	return hasArgs || hasParams || hasInput
}

func findJSONEnd(text string, pos int) int {
	depth := 0
	inString := false
	escaped := false
	for i := pos; i < len(text); i++ {
		c := text[i]
		if escaped {
			escaped = false
			continue
		}
		if c == '\\' && inString {
			escaped = true
			continue
		}
		if c == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		if c == '{' {
			depth++
		} else if c == '}' {
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}
