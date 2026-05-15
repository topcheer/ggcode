package tool

import (
	"fmt"
	"regexp"
	"strings"
)

// matchResult describes a successful old_text resolution.
type matchResult struct {
	canonical string // the actual bytes in content that will be replaced
	transform string // diagnostic tag for which fallback fired ("" = exact)
	shift     string // for leading-indent-shift: prefix to prepend to new_text lines
}

// resolveOldText tries multiple strategies to locate oldText in content,
// helping weaker LLMs whose `old_text` argument frequently differs from the
// file in trivial ways (indentation style, line-number prefix copied from
// read_file output, CRLF vs LF, trailing whitespace, missing leading
// indentation).
//
// On success, canonical is the exact substring of content that will be
// substituted (so a strings.Replace targets the right bytes). On failure
// canonical is "".
func resolveOldText(content, oldText string) matchResult {
	if oldText == "" {
		return matchResult{}
	}
	if strings.Contains(content, oldText) {
		return matchResult{canonical: oldText}
	}

	// 1. Indentation normalization (tabs <-> spaces).
	if normalized := normalizeIndentation(content, oldText); normalized != oldText && strings.Contains(content, normalized) {
		return matchResult{canonical: normalized, transform: "indent-normalized"}
	}

	// 2. Line-number prefix from read_file output: "   42\t<line>".
	if stripped := stripLineNumberPrefix(oldText); stripped != oldText {
		if strings.Contains(content, stripped) {
			return matchResult{canonical: stripped, transform: "line-numbers-stripped"}
		}
		if normalized := normalizeIndentation(content, stripped); normalized != stripped && strings.Contains(content, normalized) {
			return matchResult{canonical: normalized, transform: "line-numbers-stripped+indent-normalized"}
		}
	}

	// 3. CRLF / LF mismatch.
	if crlf := tryCRLFMatch(content, oldText); crlf != "" {
		return matchResult{canonical: crlf, transform: "crlf-converted"}
	}

	// 4. Leading-indent shift: LLM provided the right text but with less
	// (or no) leading indentation. Match by structural equality after
	// stripping both leading and trailing whitespace, then return the
	// file's actual block plus the shift to prepend to new_text lines.
	if canonical, shift := tryLeadingIndentShift(content, oldText); canonical != "" {
		return matchResult{canonical: canonical, transform: "leading-indent-shift", shift: shift}
	}

	// 5. Trailing-whitespace tolerance: leading whitespace already matches
	// (otherwise step 4 would have caught the mismatch first); only the
	// per-line trailing differs.
	if trimmed := tryTrailingWhitespaceMatch(content, oldText); trimmed != "" {
		return matchResult{canonical: trimmed, transform: "trailing-whitespace-tolerant"}
	}

	return matchResult{}
}

// adjustNewText applies the same transform that was used to find old_text
// to new_text, so the replacement remains consistent with the file.
func adjustNewText(content, newText string, mr matchResult) string {
	out := newText
	if strings.Contains(mr.transform, "line-numbers-stripped") {
		out = stripLineNumberPrefix(out)
	}
	if strings.Contains(mr.transform, "indent-normalized") {
		out = normalizeIndentation(content, out)
	}
	if mr.transform == "crlf-converted" && !strings.Contains(out, "\r\n") {
		out = strings.ReplaceAll(out, "\n", "\r\n")
	}
	if mr.transform == "leading-indent-shift" && mr.shift != "" {
		out = applyLeadingIndentShift(out, mr.shift)
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

// leadingWhitespace returns the prefix of s that consists of spaces and tabs.
func leadingWhitespace(s string) string {
	i := 0
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	return s[:i]
}

// tryLeadingIndentShift handles the very common LLM failure where the
// model provides the right text but with less (or no) leading indentation
// than the file actually uses — e.g. file has "\t\t// foo" but the LLM
// supplied just "// foo" as old_text.
//
// It looks for a contiguous block in content whose lines, after stripping
// both leading and trailing whitespace, equal the lines of oldText. To
// guard against unrelated false matches it requires:
//   - The file's leading whitespace on each non-empty matched line must
//     start with the LLM's leading whitespace on the corresponding old
//     line (so the shift is well-defined).
//   - The "shift" — the extra prefix the file has — must be IDENTICAL on
//     every non-empty matched line, preserving the relative indent of the
//     LLM's old_text.
//
// Returns the canonical block from the file plus the shift to prepend to
// each line of new_text.
func tryLeadingIndentShift(content, oldText string) (canonical, shift string) {
	oldLines := strings.Split(oldText, "\n")
	contentLines := strings.Split(content, "\n")
	if len(oldLines) == 0 || len(oldLines) > len(contentLines) {
		return "", ""
	}

	stripBoth := func(s string) string { return strings.TrimSpace(s) }

	sOld := make([]string, len(oldLines))
	firstNonEmpty := -1
	for i, l := range oldLines {
		s := stripBoth(l)
		sOld[i] = s
		if s != "" && firstNonEmpty < 0 {
			firstNonEmpty = i
		}
	}
	if firstNonEmpty < 0 {
		return "", ""
	}

	for i := 0; i+len(sOld) <= len(contentLines); i++ {
		match := true
		for j := range sOld {
			if stripBoth(contentLines[i+j]) != sOld[j] {
				match = false
				break
			}
		}
		if !match {
			continue
		}

		baseFile := leadingWhitespace(contentLines[i+firstNonEmpty])
		baseOld := leadingWhitespace(oldLines[firstNonEmpty])
		if !strings.HasPrefix(baseFile, baseOld) {
			continue
		}
		s := baseFile[len(baseOld):]
		if s == "" {
			// No actual shift — exact path or trailing-whitespace path
			// would have already matched. Keep searching for a real
			// shifted occurrence rather than returning a no-op.
			continue
		}

		// Verify every non-empty line has the same shift on top of the
		// LLM's per-line leading indent.
		consistent := true
		for j := range sOld {
			if sOld[j] == "" {
				continue
			}
			wantOldLead := leadingWhitespace(oldLines[j])
			wantFileLead := s + wantOldLead
			if leadingWhitespace(contentLines[i+j]) != wantFileLead {
				consistent = false
				break
			}
		}
		if !consistent {
			continue
		}

		return strings.Join(contentLines[i:i+len(sOld)], "\n"), s
	}
	return "", ""
}

// applyLeadingIndentShift prepends shift to every non-empty line of text.
// Empty lines are left empty so trailing blank lines in new_text don't
// gain spurious whitespace.
func applyLeadingIndentShift(text, shift string) string {
	if shift == "" {
		return text
	}
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		if l == "" {
			continue
		}
		lines[i] = shift + l
	}
	return strings.Join(lines, "\n")
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
