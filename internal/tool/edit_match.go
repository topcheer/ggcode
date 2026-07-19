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
	trim      string // for leading-indent-shift: prefix to trim from new_text lines
	start     int    // byte offset of canonical in content when the match is anchored
	anchored  bool
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
	if trimmed, changed := trimReadFileWrapperLines(oldText); changed {
		if mr := resolveOldText(content, trimmed); mr.canonical != "" {
			mr.transform = prependTransform("read-file-wrapper-stripped", mr.transform)
			return mr
		}
	}

	// 0. read_file line-number anchors. This is the most reliable path for weak
	// models because it lets them paste numbered lines directly from read_file,
	// including single-line edits and duplicate text that would otherwise be
	// ambiguous.
	if anchored := tryReadFileLineAnchor(content, oldText); anchored.canonical != "" {
		return anchored
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

	// 4. Leading-indent shift: LLM provided the right text but with a
	// consistent extra/missing base indentation. Match by structural equality
	// after stripping both leading and trailing whitespace, then return the
	// file's actual block plus the indent delta to apply to new_text.
	if canonical, shift, trim := tryLeadingIndentShift(content, oldText); canonical != "" {
		return matchResult{canonical: canonical, transform: "leading-indent-shift", shift: shift, trim: trim}
	}

	// 5. Trailing-whitespace tolerance: leading whitespace already matches
	// (otherwise step 4 would have caught the mismatch first); only the
	// per-line trailing differs.
	if trimmed := tryTrailingWhitespaceMatch(content, oldText); trimmed != "" {
		return matchResult{canonical: trimmed, transform: "trailing-whitespace-tolerant"}
	}

	// 6. Fuzzy per-line match: strip leading and trailing whitespace from
	// every line in both the file content and old_text, then compare the
	// trimmed text. This catches mixed indentation, inconsistent spacing,
	// and other subtle whitespace differences that the targeted strategies
	// above miss. The canonical form is the file's actual bytes (not the
	// trimmed version), so the replacement targets the right location.
	if canonical := tryFuzzyLineMatch(content, oldText); canonical != "" {
		return matchResult{canonical: canonical, transform: "fuzzy-line-match"}
	}

	return matchResult{}
}

var readFileLineRE = regexp.MustCompile(`^\s{0,12}(\d+)\t(.*)$`)
var readFileLineNumberOnlyRE = regexp.MustCompile(`^\s{0,12}\d+\s*$`)

type numberedBlock struct {
	startLine int
	lines     []string
}

type fileLine struct {
	text  string
	start int
	end   int
}

func splitFileLines(content string) []fileLine {
	if content == "" {
		return nil
	}
	lines := make([]fileLine, 0, strings.Count(content, "\n")+1)
	start := 0
	for start < len(content) {
		rel := strings.IndexByte(content[start:], '\n')
		if rel < 0 {
			lines = append(lines, fileLine{
				text:  content[start:],
				start: start,
				end:   len(content),
			})
			break
		}
		end := start + rel
		lines = append(lines, fileLine{
			text:  content[start:end],
			start: start,
			end:   end,
		})
		start = end + 1
	}
	return lines
}

func parseReadFileNumberedBlock(text string) (numberedBlock, bool) {
	lines := trimDanglingReadFileLineNumberOnlyLines(strings.Split(text, "\n"))
	if len(lines) == 0 {
		return numberedBlock{}, false
	}

	body := make([]string, len(lines))
	startLine := 0
	for i, line := range lines {
		n, lineText, ok := parseReadFileLine(line)
		if !ok {
			return numberedBlock{}, false
		}
		if i == 0 {
			startLine = n
		} else if n != startLine+i {
			return numberedBlock{}, false
		}
		body[i] = lineText
	}

	return numberedBlock{
		startLine: startLine,
		lines:     body,
	}, true
}

func parseReadFileLine(line string) (lineNumber int, text string, ok bool) {
	if m := readFileLineRE.FindStringSubmatch(line); m != nil {
		fmt.Sscanf(m[1], "%d", &lineNumber)
		if lineNumber <= 0 {
			return 0, "", false
		}
		return lineNumber, m[2], true
	}
	if readFileLineNumberOnlyRE.MatchString(line) {
		fmt.Sscanf(strings.TrimSpace(line), "%d", &lineNumber)
		if lineNumber <= 0 {
			return 0, "", false
		}
		return lineNumber, "", true
	}
	return 0, "", false
}

func resolveAnchoredCandidate(content, candidate, oldText string) matchResult {
	if candidate == oldText {
		return matchResult{canonical: candidate}
	}
	if normalized := normalizeIndentation(content, oldText); normalized == candidate {
		return matchResult{canonical: candidate, transform: "indent-normalized"}
	}
	if strings.Contains(candidate, "\r\n") && !strings.Contains(oldText, "\r\n") {
		if strings.ReplaceAll(oldText, "\n", "\r\n") == candidate {
			return matchResult{canonical: candidate, transform: "crlf-converted"}
		}
	}
	if canonical, shift, trim := tryLeadingIndentShift(candidate, oldText); canonical == candidate {
		return matchResult{canonical: candidate, transform: "leading-indent-shift", shift: shift, trim: trim}
	}
	if trimmed := tryTrailingWhitespaceMatch(candidate, oldText); trimmed == candidate {
		return matchResult{canonical: candidate, transform: "trailing-whitespace-tolerant"}
	}
	return matchResult{}
}

func prependTransform(prefix, suffix string) string {
	switch {
	case prefix == "":
		return suffix
	case suffix == "":
		return prefix
	default:
		return prefix + "+" + suffix
	}
}

func tryReadFileLineAnchor(content, oldText string) matchResult {
	block, ok := parseReadFileNumberedBlock(oldText)
	if !ok {
		return matchResult{}
	}

	lines := splitFileLines(content)
	if block.startLine <= 0 || block.startLine+len(block.lines)-1 > len(lines) {
		return matchResult{}
	}

	startIdx := block.startLine - 1
	endIdx := startIdx + len(block.lines) - 1
	candidate := content[lines[startIdx].start:lines[endIdx].end]
	mr := resolveAnchoredCandidate(content, candidate, strings.Join(block.lines, "\n"))
	if mr.canonical == "" {
		return matchResult{}
	}
	mr.canonical = candidate
	mr.transform = prependTransform("line-numbers-stripped", mr.transform)
	mr.start = lines[startIdx].start
	mr.anchored = true
	return mr
}

// adjustNewText applies the same transform that was used to find old_text
// to new_text, so the replacement remains consistent with the file.
func adjustNewText(content, newText string, mr matchResult) string {
	out := newText
	if strings.Contains(mr.transform, "read-file-wrapper-stripped") {
		if trimmed, changed := trimReadFileWrapperLines(out); changed {
			out = trimmed
		}
	}
	if strings.Contains(mr.transform, "line-numbers-stripped") {
		out = stripAllLineNumberPrefixes(out)
	}
	if strings.Contains(mr.transform, "indent-normalized") {
		out = normalizeIndentation(content, out)
	}
	if strings.Contains(mr.transform, "crlf-converted") && !strings.Contains(out, "\r\n") {
		out = strings.ReplaceAll(out, "\n", "\r\n")
	}
	if strings.Contains(mr.transform, "leading-indent-shift") {
		if mr.shift != "" {
			out = applyLeadingIndentShift(out, mr.shift)
		}
		if mr.trim != "" {
			out = trimLeadingIndentShift(out, mr.trim)
		}
	}
	return out
}

// lineNumberPrefixRE matches prefixes produced by readFileRange:
// up to 6 leading spaces, then 1+ digits, then a tab.
// We allow more leading spaces in case the LLM reformatted slightly.
var lineNumberPrefixRE = regexp.MustCompile(`^\s{0,12}\d+\t`)
var readFileWrapperLineRE = regexp.MustCompile(`^(?:\[(?:indent:|encoding:|Extracted from |File truncated:|File has |multi_file_read summary)|=== (?:FILE|ERROR): |\[end (?:file|error)\]$|\[skipped:)`)

// stripLineNumberPrefix removes "  42\t" style prefixes if a clear majority
// of non-empty lines have them. This catches the common failure where an
// LLM pastes back read_file output (which is line-numbered) verbatim as
// old_text.
func stripLineNumberPrefix(text string) string {
	lines := trimDanglingReadFileLineNumberOnlyLines(strings.Split(text, "\n"))
	matched, nonEmpty := 0, 0
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		nonEmpty++
		if lineNumberPrefixRE.MatchString(l) || readFileLineNumberOnlyRE.MatchString(l) {
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
		if readFileLineNumberOnlyRE.MatchString(l) {
			out[i] = ""
			continue
		}
		out[i] = lineNumberPrefixRE.ReplaceAllString(l, "")
	}
	return strings.Join(out, "\n")
}

func stripAllLineNumberPrefixes(text string) string {
	lines := trimDanglingReadFileLineNumberOnlyLines(strings.Split(text, "\n"))
	for i, l := range lines {
		if readFileLineNumberOnlyRE.MatchString(l) {
			lines[i] = ""
			continue
		}
		lines[i] = lineNumberPrefixRE.ReplaceAllString(l, "")
	}
	return strings.Join(lines, "\n")
}

func trimDanglingReadFileLineNumberOnlyLines(lines []string) []string {
	if len(lines) == 0 {
		return lines
	}
	hasAnchoredLine := false
	for _, line := range lines {
		if readFileLineRE.MatchString(line) || readFileLineNumberOnlyRE.MatchString(line) {
			hasAnchoredLine = true
			break
		}
	}
	if !hasAnchoredLine {
		return lines
	}
	start, end := 0, len(lines)
	for start < end && readFileLineNumberOnlyRE.MatchString(lines[start]) {
		start++
	}
	for end > start && readFileLineNumberOnlyRE.MatchString(lines[end-1]) {
		end--
	}
	return lines[start:end]
}

func trimReadFileWrapperLines(text string) (string, bool) {
	lines := strings.Split(text, "\n")
	start, end := 0, len(lines)
	firstContent := start
	for firstContent < end && strings.TrimSpace(lines[firstContent]) == "" {
		firstContent++
	}
	if firstContent < end && readFileWrapperLineRE.MatchString(lines[firstContent]) {
		start = firstContent + 1
		for start < end && (strings.TrimSpace(lines[start]) == "" || readFileWrapperLineRE.MatchString(lines[start])) {
			start++
		}
	}
	lastContent := end - 1
	for lastContent >= start && strings.TrimSpace(lines[lastContent]) == "" {
		lastContent--
	}
	if lastContent >= start && readFileWrapperLineRE.MatchString(lines[lastContent]) {
		end = lastContent
		for end > start && (strings.TrimSpace(lines[end-1]) == "" || readFileWrapperLineRE.MatchString(lines[end-1])) {
			end--
		}
	}
	if start == 0 && end == len(lines) {
		return text, false
	}
	trimmed := strings.Join(lines[start:end], "\n")
	if trimmed == "" {
		return text, false
	}
	return trimmed, true
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
// model provides the right text but with a consistently different base
// indentation than the file — e.g. file has "\t\t// foo" but the LLM
// supplied either "// foo" or "\t\t\t// foo" as old_text.
//
// It looks for a contiguous block in content whose lines, after stripping
// both leading and trailing whitespace, equal the lines of oldText. To
// guard against unrelated false matches it requires:
//   - The file and old_text leading whitespace must differ by the same
//     prefix on every non-empty matched line, in either direction.
//
// Returns the canonical block from the file plus the shift to prepend to
// or trim from each line of new_text.
func tryLeadingIndentShift(content, oldText string) (canonical, shift, trim string) {
	oldLines := strings.Split(oldText, "\n")
	contentLines := strings.Split(content, "\n")
	if len(oldLines) == 0 || len(oldLines) > len(contentLines) {
		return "", "", ""
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
		return "", "", ""
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
		extraFile, extraOld := "", ""
		switch {
		case strings.HasPrefix(baseFile, baseOld):
			extraFile = baseFile[len(baseOld):]
		case strings.HasPrefix(baseOld, baseFile):
			extraOld = baseOld[len(baseFile):]
		default:
			continue
		}
		if extraFile == "" && extraOld == "" {
			continue
		}

		// Verify every non-empty line has the same indent delta while
		// preserving the old_text's relative indentation.
		consistent := true
		for j := range sOld {
			if sOld[j] == "" {
				continue
			}
			fileLead := leadingWhitespace(contentLines[i+j])
			oldLead := leadingWhitespace(oldLines[j])
			switch {
			case extraFile != "":
				if fileLead != extraFile+oldLead {
					consistent = false
				}
			case extraOld != "":
				if oldLead != extraOld+fileLead {
					consistent = false
				}
			}
			if !consistent {
				break
			}
		}
		if !consistent {
			continue
		}

		return strings.Join(contentLines[i:i+len(sOld)], "\n"), extraFile, extraOld
	}
	return "", "", ""
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

func trimLeadingIndentShift(text, trim string) string {
	if trim == "" {
		return text
	}
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		if l == "" {
			continue
		}
		if strings.HasPrefix(l, trim) {
			lines[i] = l[len(trim):]
		}
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

// tryFuzzyLineMatch compares old_text against the file content by stripping
// leading/trailing whitespace from every line in both. This catches the most
// common LLM edit failure: the text content is correct but whitespace (tab vs
// space, inconsistent indentation depth, trailing spaces) prevents an exact
// match. Returns the file's actual bytes for the matched block so the
// replacement targets the right location.
func tryFuzzyLineMatch(content, oldText string) string {
	oldLines := strings.Split(oldText, "\n")
	if len(oldLines) == 0 {
		return ""
	}

	// Trim each old_text line for comparison.
	trimmedOld := make([]string, len(oldLines))
	for i, l := range oldLines {
		trimmedOld[i] = strings.TrimSpace(l)
	}

	fileLines := strings.Split(content, "\n")
	nFile := len(fileLines)
	nOld := len(trimmedOld)

	// Slide a window over the file lines.
	for start := 0; start <= nFile-nOld; start++ {
		matched := true
		for j := 0; j < nOld; j++ {
			if strings.TrimSpace(fileLines[start+j]) != trimmedOld[j] {
				matched = false
				break
			}
		}
		if matched {
			// Return the file's actual bytes for this block.
			return strings.Join(fileLines[start:start+nOld], "\n")
		}
	}
	return ""
}
