package agent

import (
	"encoding/json"
	"strings"
)

// hasInlineToolCall checks whether the assistant's text/reasoning output
// contains what looks like a tool call that wasn't properly structured.
// This happens with lower-reasoning models that "think" about tool calls
// in prose instead of emitting structured tool_use blocks.
//
// Detects common patterns:
//   - <tool_call>{"name":"read_file","arguments":{...}}</tool_call>
//   - ```json\n{"name":"read_file","arguments":{...}}\n```
//   - Plain JSON with "name" and "arguments"/"parameters" keys
//   - {"tool":"read_file","input":{...}}
func hasInlineToolCall(text string) bool {
	if len(text) < 20 {
		return false
	}
	// Limit scan to first 4KB — inline tool calls appear early in the
	// output. Scanning the entire text for every '{' would be O(n²)
	// on large code-heavy responses.
	maxScan := len(text)
	if maxScan > 4096 {
		maxScan = 4096
	}
	scanText := text[:maxScan]

	// Fast check: must contain "name" or "tool" as a key
	if !strings.Contains(scanText, "\"name\"") &&
		!strings.Contains(scanText, "\"tool\"") &&
		!strings.Contains(scanText, "tool_call") &&
		!strings.Contains(scanText, "function_call") {
		return false
	}

	// Look for JSON objects that have a "name" field and either
	// "arguments" or "parameters" or "input" field.
	// We scan for potential JSON object boundaries and validate.
	// This is a heuristic — false positives are acceptable (worst case
	// we inject a nudge that the model ignores).
	for i := 0; i < len(scanText); i++ {
		if scanText[i] != '{' {
			continue
		}
		// Find the matching closing brace (search in full text to not
		// miss closing braces beyond maxScan)
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

// isToolCallJSON checks if a JSON string looks like a tool call.
func isToolCallJSON(s string) bool {
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return false
	}
	// Must have a "name" or "tool" field
	name, hasName := m["name"]
	_, hasTool := m["tool"]
	if !hasName && !hasTool {
		return false
	}
	// Name must be a non-empty string
	if hasName {
		if n, ok := name.(string); !ok || n == "" {
			return false
		}
	}
	// Must have arguments/parameters/input field
	_, hasArgs := m["arguments"]
	_, hasParams := m["parameters"]
	_, hasInput := m["input"]
	return hasArgs || hasParams || hasInput
}

// findJSONEnd finds the position of the closing '}' for the JSON object
// starting at pos (which must be '{'). Handles nested objects and strings.
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
	return -1 // unbalanced
}
