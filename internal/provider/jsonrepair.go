package provider

import (
	"bytes"
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/topcheer/ggcode/internal/debug"
)

// RepairJSON attempts to fix common JSON malformations in LLM tool-call
// arguments. Many OpenAI-compatible backends (LiteLLM, vLLM, ZAI compat)
// and weaker reasoning models produce arguments that are *almost* valid JSON
// but fail json.Valid due to:
//
//   - Stream truncation: missing closing braces/brackets
//   - Trailing commas before } or ]
//   - Markdown code fences wrapping the JSON
//   - Extra prose before/after the JSON object
//   - Smart quotes (curly) instead of straight quotes
//
// Returns (repaired, true) if repair was applied and the result is valid JSON.
// Returns (original, false) if the input was already valid or repair failed.
func RepairJSON(raw []byte) ([]byte, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return raw, false
	}

	// Fast path: already valid JSON.
	if json.Valid(trimmed) {
		return trimmed, false
	}

	original := string(trimmed)
	repaired := original

	// Step 1: Strip markdown code fences (```json ... ``` or ``` ... ```).
	repaired = stripCodeFences(repaired)

	// Step 2: Replace smart/curly quotes with straight quotes.
	repaired = normalizeQuotes(repaired)

	// Re-check after fence stripping and quote normalization.
	if json.Valid([]byte(repaired)) {
		debug.Log("jsonrepair", "repaired by fence/quote normalization: %s -> %s", truncateForLog(original), truncateForLog(repaired))
		return []byte(repaired), true
	}

	// Step 3: Extract the JSON object — find the first '{' and try to
	// extend to the matching '}'. This removes leading/trailing prose.
	repaired = extractJSONObject(repaired)
	if json.Valid([]byte(repaired)) {
		debug.Log("jsonrepair", "repaired by object extraction: %s -> %s", truncateForLog(original), truncateForLog(repaired))
		return []byte(repaired), true
	}

	// Step 4: Remove trailing commas before closing braces/brackets.
	repaired = removeTrailingCommas(repaired)
	if json.Valid([]byte(repaired)) {
		debug.Log("jsonrepair", "repaired by trailing comma removal: %s -> %s", truncateForLog(original), truncateForLog(repaired))
		return []byte(repaired), true
	}

	// Step 5: Close unclosed braces and brackets (stream truncation fix).
	repaired = closeUnclosed(repaired)
	if json.Valid([]byte(repaired)) {
		debug.Log("jsonrepair", "repaired by closing unclosed delimiters: %s -> %s", truncateForLog(original), truncateForLog(repaired))
		return []byte(repaired), true
	}

	// Repair failed — return original.
	debug.Log("jsonrepair", "repair failed for: %s", truncateForLog(original))
	return raw, false
}

// stripCodeFences removes markdown code fence wrappers.
func stripCodeFences(s string) string {
	s = strings.TrimSpace(s)
	// Match ```json\n...\n``` or ```\n...\n```
	if strings.HasPrefix(s, "```") {
		// Find the opening fence end (first newline after ```)
		nl := strings.IndexByte(s, '\n')
		if nl > 0 {
			rest := s[nl+1:]
			// Remove closing fence if present
			if idx := strings.LastIndex(rest, "```"); idx >= 0 {
				rest = rest[:idx]
			}
			s = strings.TrimSpace(rest)
		}
	}
	return s
}

// normalizeQuotes replaces smart/curly quotes with straight ASCII quotes.
func normalizeQuotes(s string) string {
	r := make([]byte, 0, len(s))
	for _, b := range []byte(s) {
		switch b {
		case 0xe2: // UTF-8 multi-byte start for smart quotes
			// Handled below via rune-level replacement
			r = append(r, b)
		default:
			r = append(r, b)
		}
	}
	// Rune-level replacement for multi-byte smart quotes
	result := string(r)
	result = strings.ReplaceAll(result, "\u201c", `"`) // left double quotation mark
	result = strings.ReplaceAll(result, "\u201d", `"`) // right double quotation mark
	result = strings.ReplaceAll(result, "\u2018", "'") // left single quotation mark
	result = strings.ReplaceAll(result, "\u2019", "'") // right single quotation mark
	result = strings.ReplaceAll(result, "\u00ab", `"`) // left angle quote
	result = strings.ReplaceAll(result, "\u00bb", `"`) // right angle quote
	return result
}

// extractJSONObject finds the first '{' and the last '}' and returns
// the substring between them (inclusive). This strips surrounding prose
// that some models emit around tool-call JSON.
func extractJSONObject(s string) string {
	first := strings.IndexByte(s, '{')
	if first < 0 {
		return s
	}
	last := strings.LastIndexByte(s, '}')
	if last <= first {
		// No closing brace — return from first '{' to end (closeUnclosed will handle)
		return s[first:]
	}
	return s[first : last+1]
}

// removeTrailingCommas removes commas that appear immediately before
// a closing '}' or ']' (with optional whitespace between).
func removeTrailingCommas(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		if c == ',' {
			// Look ahead: skip whitespace, check for } or ]
			j := i + 1
			for j < len(runes) && (runes[j] == ' ' || runes[j] == '\t' || runes[j] == '\n' || runes[j] == '\r') {
				j++
			}
			if j < len(runes) && (runes[j] == '}' || runes[j] == ']') {
				// Skip the comma
				continue
			}
		}
		// Handle comma at end of input (truncated)
		if c == ',' && i == len(runes)-1 {
			continue
		}
		b.WriteRune(c)
	}
	return b.String()
}

// closeUnclosed balances unclosed '{', '[', and '"' delimiters by
// appending the appropriate closing characters. This is the primary
// fix for stream-truncated JSON where the model's output was cut off
// mid-argument.
func closeUnclosed(s string) string {
	var stack []byte
	inString := false
	escaped := false

	for i := 0; i < len(s); i++ {
		c := s[i]
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
		switch c {
		case '{':
			stack = append(stack, '}')
		case '[':
			stack = append(stack, ']')
		case '}', ']':
			// Pop matching close from stack
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
	}

	// If we're inside an unterminated string, close it first.
	if inString {
		s += `"`
	}

	// Remove trailing comma if present (common before truncation point).
	s = strings.TrimRight(strings.TrimSpace(s), ",")

	// Append closing delimiters in reverse order.
	for i := len(stack) - 1; i >= 0; i-- {
		s += string(stack[i])
	}

	return s
}

// truncateForLog truncates a string for debug logging (avoids huge log lines).
func truncateForLog(s string) string {
	const max = 200
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	return string(runes[:max]) + "...(truncated)"
}
