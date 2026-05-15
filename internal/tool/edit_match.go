package tool

import (
	"fmt"
	"regexp"
	"strings"
)

// resolveOldText tries multiple strategies to locate oldText in content,
// helping weaker LLMs whose `old_text` argument frequently differs from the
// file in trivial ways (indentation style, line-number prefix copied from
// read_file output, CRLF vs LF, trailing whitespace).
//
// On success, it returns the canonical text exactly as it appears in
// content (so a strings.Replace will substitute the right bytes) and a
// short transform tag describing what was applied. On failure both return
// values are empty strings.
func resolveOldText(content, oldText string) (canonical string, transform string) {
	if oldText == "" {
		return "", ""
	}
	if strings.Contains(content, oldText) {
		return oldText, ""
	}

	// 1. Indentation normalization (tabs <-> spaces).
	if normalized := normalizeIndentation(content, oldText); normalized != oldText && strings.Contains(content, normalized) {
		return normalized, "indent-normalized"
	}

	// 2. Line-number prefix from read_file output: "   42\t<line>".
	if stripped := stripLineNumberPrefix(oldText); stripped != oldText {
		if strings.Contains(content, stripped) {
			return stripped, "line-numbers-stripped"
		}
		if normalized := normalizeIndentation(content, stripped); normalized != stripped && strings.Contains(content, normalized) {
			return normalized, "line-numbers-stripped+indent-normalized"
		}
	}

	// 3. CRLF / LF mismatch.
	if crlf := tryCRLFMatch(content, oldText); crlf != "" {
		return crlf, "crlf-converted"
	}

	// 4. Trailing-whitespace tolerance: match by per-line right-trimmed
	// equality, then return the file's actual text at those lines so the
	// substitution preserves the file's whitespace exactly.
	if trimmed := tryTrailingWhitespaceMatch(content, oldText); trimmed != "" {
		return trimmed, "trailing-whitespace-tolerant"
	}

	return "", ""
}

// adjustNewText applies the same transform that was used to find old_text
// to new_text, so the replacement remains consistent with the file.
func adjustNewText(content, newText, transform string) string {
	out := newText
	switch {
	case strings.Contains(transform, "line-numbers-stripped"):
		out = stripLineNumberPrefix(out)
	}
	if strings.Contains(transform, "indent-normalized") {
		out = normalizeIndentation(content, out)
	}
	if transform == "crlf-converted" && !strings.Contains(out, "\r\n") {
		out = strings.ReplaceAll(out, "\n", "\r\n")
	}
	return out
}

// lineNumberPrefixRE matches prefixes produced by readFileRange:
// up to 6 leading spaces, then 1+ digits, then a tab.
// We allow more leading spaces in case the LLM reformatted slightly.
var lineNumberPrefixRE = regexp.MustCompile(`^\s{0,12}\d+\t`)

// stripLineNumberPrefix removes "  42\t" style prefixes if a clear majority
// of non-empty lines have them. This catches the common failure where an
// LLM pastes back read_file output (which is line-numbered) verbatim as
// old_text.
func stripLineNumberPrefix(text string) string {
	lines := strings.Split(text, "\n")
	matched, nonEmpty := 0, 0
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		nonEmpty++
		if lineNumberPrefixRE.MatchString(l) {
			matched++
		}
	}
	// Require >=2 lines with prefix AND a strong majority, to avoid false
	// positives on legitimate text that happens to start with digits+TAB.
	if matched < 2 || matched < (nonEmpty/2+1) {
		return text
	}
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = lineNumberPrefixRE.ReplaceAllString(l, "")
	}
	return strings.Join(out, "\n")
}

// tryCRLFMatch handles the case where the file uses CRLF line endings but
// the LLM provided LF-only old_text.
func tryCRLFMatch(content, oldText string) string {
	if !strings.Contains(content, "\r\n") {
		return ""
	}
	if strings.Contains(oldText, "\r\n") {
		return ""
	}
	if !strings.Contains(oldText, "\n") {
		return ""
	}
	candidate := strings.ReplaceAll(oldText, "\n", "\r\n")
	if strings.Contains(content, candidate) {
		return candidate
	}
	return ""
}

// tryTrailingWhitespaceMatch finds oldText in content by ignoring
// per-line trailing whitespace differences. Returns the canonical
// substring from content (with its real trailing whitespace) so that a
// subsequent strings.Replace targets the exact bytes.
func tryTrailingWhitespaceMatch(content, oldText string) string {
	contentLines := strings.Split(content, "\n")
	oldLines := strings.Split(oldText, "\n")
	if len(oldLines) == 0 || len(oldLines) > len(contentLines) {
		return ""
	}
	rstrip := func(s string) string { return strings.TrimRight(s, " \t") }
	rstripOld := make([]string, len(oldLines))
	for i, l := range oldLines {
		rstripOld[i] = rstrip(l)
	}
	rstripContent := make([]string, len(contentLines))
	for i, l := range contentLines {
		rstripContent[i] = rstrip(l)
	}

	for i := 0; i+len(rstripOld) <= len(rstripContent); i++ {
		match := true
		for j := range rstripOld {
			if rstripContent[i+j] != rstripOld[j] {
				match = false
				break
			}
		}
		if match {
			return strings.Join(contentLines[i:i+len(rstripOld)], "\n")
		}
	}
	return ""
}

// findMatchLineNumbers returns the 1-based line numbers where oldText
// occurs in content, capped at 10 entries. Used to make non-unique-match
// errors actionable: "matches at lines 42, 87, 153 — add surrounding
// context to disambiguate".
func findMatchLineNumbers(content, oldText string) []int {
	if oldText == "" {
		return nil
	}
	var out []int
	pos := 0
	for {
		idx := strings.Index(content[pos:], oldText)
		if idx < 0 {
			break
		}
		abs := pos + idx
		ln := 1 + strings.Count(content[:abs], "\n")
		out = append(out, ln)
		if len(out) >= 10 {
			break
		}
		pos = abs + len(oldText)
		if pos >= len(content) {
			break
		}
	}
	return out
}

// formatMatchLines turns [42, 87, 153] into "42, 87, 153".
func formatMatchLines(lines []int) string {
	parts := make([]string, len(lines))
	for i, n := range lines {
		parts[i] = fmt.Sprintf("%d", n)
	}
	return strings.Join(parts, ", ")
}
