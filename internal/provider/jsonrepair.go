package provider

import (
	"bytes"
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/topcheer/ggcode/internal/debug"
)

func RepairJSON(raw []byte) ([]byte, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return raw, false
	}

	if json.Valid(trimmed) {
		return trimmed, false
	}

	original := string(trimmed)
	repaired := original

	repaired = stripCodeFences(repaired)

	repaired = normalizeQuotes(repaired)

	if json.Valid([]byte(repaired)) {
		debug.Log("jsonrepair", "repaired by fence/quote normalization: %s -> %s", truncateForLog(original), truncateForLog(repaired))
		return []byte(repaired), true
	}

	repaired = normalizePythonLiterals(repaired)

	repaired = quoteUnquotedKeys(repaired)

	repaired = normalizeSingleQuotedStrings(repaired)

	if json.Valid([]byte(repaired)) {
		debug.Log("jsonrepair", "repaired by literal/key/quote normalization: %s -> %s", truncateForLog(original), truncateForLog(repaired))
		return []byte(repaired), true
	}

	repaired = extractJSONObject(repaired)
	if json.Valid([]byte(repaired)) {
		debug.Log("jsonrepair", "repaired by object extraction: %s -> %s", truncateForLog(original), truncateForLog(repaired))
		return []byte(repaired), true
	}

	repaired = removeTrailingCommas(repaired)
	if json.Valid([]byte(repaired)) {
		debug.Log("jsonrepair", "repaired by trailing comma removal: %s -> %s", truncateForLog(original), truncateForLog(repaired))
		return []byte(repaired), true
	}

	repaired, closable := closeUnclosed(repaired)
	if closable && json.Valid([]byte(repaired)) {
		debug.Log("jsonrepair", "repaired by closing unclosed delimiters: %s -> %s", truncateForLog(original), truncateForLog(repaired))
		return []byte(repaired), true
	}
	if !closable {
		debug.Log("jsonrepair", "refused repair: stream ends inside an unterminated string (truncated value): %s", truncateForLog(original))
	}

	debug.Log("jsonrepair", "repair failed for: %s", truncateForLog(original))
	return raw, false
}

func stripCodeFences(s string) string {
	s = strings.TrimSpace(s)

	if strings.HasPrefix(s, "```") {

		nl := strings.IndexByte(s, '\n')
		if nl > 0 {
			rest := s[nl+1:]

			if idx := strings.LastIndex(rest, "```"); idx >= 0 {
				rest = rest[:idx]
			}
			s = strings.TrimSpace(rest)
		}
	}
	return s
}

func normalizeQuotes(s string) string {
	r := make([]byte, 0, len(s))
	for _, b := range []byte(s) {
		switch b {
		case 0xe2:

			r = append(r, b)
		default:
			r = append(r, b)
		}
	}

	result := string(r)
	result = strings.ReplaceAll(result, "\u201c", `"`)
	result = strings.ReplaceAll(result, "\u201d", `"`)
	result = strings.ReplaceAll(result, "\u2018", "'")
	result = strings.ReplaceAll(result, "\u2019", "'")
	result = strings.ReplaceAll(result, "\u00ab", `"`)
	result = strings.ReplaceAll(result, "\u00bb", `"`)
	return result
}

func extractJSONObject(s string) string {
	first := strings.IndexByte(s, '{')
	if first < 0 {
		return s
	}
	last := strings.LastIndexByte(s, '}')
	if last <= first {

		return s[first:]
	}
	return s[first : last+1]
}

func removeTrailingCommas(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		if c == ',' {

			j := i + 1
			for j < len(runes) && (runes[j] == ' ' || runes[j] == '\t' || runes[j] == '\n' || runes[j] == '\r') {
				j++
			}
			if j < len(runes) && (runes[j] == '}' || runes[j] == ']') {

				continue
			}
		}

		if c == ',' && i == len(runes)-1 {
			continue
		}
		b.WriteRune(c)
	}
	return b.String()
}

func closeUnclosed(s string) (string, bool) {
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

			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
	}

	if inString {
		return s, false
	}

	s = strings.TrimRight(strings.TrimSpace(s), ",")

	for i := len(stack) - 1; i >= 0; i-- {
		s += string(stack[i])
	}

	return s, true
}

func truncateForLog(s string) string {
	const max = 200
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	return string(runes[:max]) + "...(truncated)"
}

func normalizePythonLiterals(s string) string {
	if !strings.ContainsAny(s, "TFN") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	inString := false
	escaped := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if escaped {
			escaped = false
			b.WriteByte(c)
			continue
		}
		if inString {
			if c == '\\' {
				escaped = true
			} else if c == '"' {
				inString = false
			}
			b.WriteByte(c)
			continue
		}
		if c == '"' {
			inString = true
			b.WriteByte(c)
			continue
		}
		switch {
		case c == 'T' && matchBareWord(s, i, "True"):
			b.WriteString("true")
			i += len("True") - 1
		case c == 'F' && matchBareWord(s, i, "False"):
			b.WriteString("false")
			i += len("False") - 1
		case c == 'N' && matchBareWord(s, i, "None"):
			b.WriteString("null")
			i += len("None") - 1
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

func matchBareWord(s string, i int, word string) bool {
	if i+len(word) > len(s) || s[i:i+len(word)] != word {
		return false
	}
	if i > 0 && isIdentPart(s[i-1]) {
		return false
	}
	if j := i + len(word); j < len(s) && isIdentPart(s[j]) {
		return false
	}
	return true
}

func quoteUnquotedKeys(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	inString := false
	escaped := false
	expectKey := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if escaped {
			escaped = false
			b.WriteByte(c)
			continue
		}
		if inString {
			if c == '\\' {
				escaped = true
			} else if c == '"' {
				inString = false
			}
			b.WriteByte(c)
			continue
		}
		switch c {
		case '"':
			inString = true
			expectKey = false
			b.WriteByte(c)
		case '{':
			expectKey = true
			b.WriteByte(c)
		case '[':
			expectKey = false
			b.WriteByte(c)
		case ',':
			expectKey = true
			b.WriteByte(c)
		case ':':
			expectKey = false
			b.WriteByte(c)
		default:
			if expectKey && isIdentStart(c) {
				j := i
				for j < len(s) && isIdentPart(s[j]) {
					j++
				}
				ident := s[i:j]
				k := j
				for k < len(s) && (s[k] == ' ' || s[k] == '\t' || s[k] == '\n' || s[k] == '\r') {
					k++
				}
				expectKey = false
				if k < len(s) && s[k] == ':' {
					b.WriteByte('"')
					b.WriteString(ident)
					b.WriteByte('"')
				} else {
					b.WriteString(ident)
				}
				i = j - 1
				continue
			}
			if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
				expectKey = false
			}
			b.WriteByte(c)
		}
	}
	return b.String()
}

func normalizeSingleQuotedStrings(s string) string {
	if !strings.ContainsRune(s, '\'') {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	inString := false
	escaped := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if escaped {
			escaped = false
			b.WriteByte(c)
			continue
		}
		if inString {
			if c == '\\' {
				escaped = true
			} else if c == '"' {
				inString = false
			}
			b.WriteByte(c)
			continue
		}
		if c == '"' {
			inString = true
			b.WriteByte(c)
			continue
		}
		if c == '\'' {
			end := -1
			for j := i + 1; j < len(s); j++ {
				if s[j] == '\'' {
					end = j
					break
				}
				if s[j] == '\n' {
					break
				}
			}
			if end > 0 {
				inner := s[i+1 : end]
				if !strings.ContainsRune(inner, '\\') {
					b.WriteByte('"')
					b.WriteString(strings.ReplaceAll(inner, `"`, `\"`))
					b.WriteByte('"')
					i = end
					continue
				}
			}
			b.WriteByte(c)
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isIdentPart(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9')
}
