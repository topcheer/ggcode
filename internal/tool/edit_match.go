package tool

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

type matchResult struct {
	canonical string
	transform string
	shift     string
	trim      string
	start     int
	anchored  bool
}

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

	if anchored := tryReadFileLineAnchor(content, oldText); anchored.canonical != "" {
		return anchored
	}

	if normalized := normalizeIndentation(content, oldText); normalized != oldText && strings.Contains(content, normalized) {
		return matchResult{canonical: normalized, transform: "indent-normalized"}
	}

	if stripped := stripLineNumberPrefix(oldText); stripped != oldText {
		if strings.Contains(content, stripped) {
			return matchResult{canonical: stripped, transform: "line-numbers-stripped"}
		}
		if normalized := normalizeIndentation(content, stripped); normalized != stripped && strings.Contains(content, normalized) {
			return matchResult{canonical: normalized, transform: "line-numbers-stripped+indent-normalized"}
		}
	}

	if crlf := tryCRLFMatch(content, oldText); crlf != "" {
		return matchResult{canonical: crlf, transform: "crlf-converted"}
	}

	if canonical, shift, trim := tryLeadingIndentShift(content, oldText); canonical != "" {
		return matchResult{canonical: canonical, transform: "leading-indent-shift", shift: shift, trim: trim}
	}

	if trimmed := tryTrailingWhitespaceMatch(content, oldText); trimmed != "" {
		return matchResult{canonical: trimmed, transform: "trailing-whitespace-tolerant"}
	}

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

func adjustNewText(content, newText string, mr matchResult) string {
	out := newText
	if strings.Contains(mr.transform, "read-file-wrapper-stripped") {
		if trimmed, changed := trimReadFileWrapperLines(out); changed {
			out = trimmed
		}
	}
	if strings.Contains(mr.transform, "line-numbers-stripped") {

		out = stripPaddedLineNumberPrefixes(out, content)
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

var lineNumberPrefixRE = regexp.MustCompile(`^\s{0,12}\d+\t`)
var readFileWrapperLineRE = regexp.MustCompile(`^(?:\[(?:indent:|encoding:|Extracted from |File truncated:|File has |multi_file_read summary)|=== (?:FILE|ERROR): |\[end (?:file|error)\]$|\[skipped:)`)

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

func guardedStripAllLineNumberPrefixes(text string) string {
	lines := strings.Split(text, "\n")
	matched, nonEmpty := 0, 0
	ascending := 0
	prev := -1
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		nonEmpty++
		num := -1
		if m := paddedLineNumberPrefixRE.FindStringSubmatch(l); m != nil {
			num = atoiSafe4442(strings.Trim(m[0], " \t"))
		} else if m := bareLineNumberPrefixRE.FindStringSubmatch(l); m != nil {
			num = atoiSafe4442(m[1])
		}
		if num < 0 {
			continue
		}
		matched++
		if num > prev {
			ascending++
			prev = num
		}
	}
	if matched < 2 || matched < (nonEmpty/2+1) || ascending != matched {
		return text
	}
	return stripAllLineNumberPrefixes(text)
}

func atoiSafe4442(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return -1
		}
		n = n*10 + int(r-'0')
		if n > 1<<30 {
			return -1
		}
	}
	return n
}

func countLineAligned(s, multi string) int {
	if !strings.Contains(multi, "\n") {
		return strings.Count(s, multi)
	}
	hayLines := strings.Split(s, "\n")
	needle := strings.Split(multi, "\n")
	n := len(needle)
	if n == 0 || len(hayLines) < n {
		return 0
	}
	count := 0
	for i := 0; i+n <= len(hayLines); i++ {
		ok := true
		for j := 0; j < n; j++ {
			if hayLines[i+j] != needle[j] {
				ok = false
				break
			}
		}
		if ok {
			count++
		}
	}
	return count
}

var paddedLineNumberPrefixRE = regexp.MustCompile(`^\s{1,12}\d+\t`)

var bareLineNumberPrefixRE = regexp.MustCompile(`^(\d+)\t`)

func stripPaddedLineNumberPrefixes(text, content string) string {
	maxLine := strings.Count(content, "\n") + 1
	lines := trimDanglingReadFileLineNumberOnlyLines(strings.Split(text, "\n"))
	for i, l := range lines {
		if readFileLineNumberOnlyRE.MatchString(l) {
			lines[i] = ""
			continue
		}
		if paddedLineNumberPrefixRE.MatchString(l) {
			lines[i] = paddedLineNumberPrefixRE.ReplaceAllString(l, "")
			continue
		}
		if m := bareLineNumberPrefixRE.FindStringSubmatch(l); m != nil {
			if n, err := strconv.Atoi(m[1]); err == nil && n >= 1 && n <= maxLine {
				lines[i] = bareLineNumberPrefixRE.ReplaceAllString(l, "")
			}
		}
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

func fileUsesCRLF(content string) bool {
	crlf := strings.Count(content, "\r\n")
	if crlf == 0 {
		return false
	}
	bareLF := strings.Count(content, "\n") - crlf
	return crlf > bareLF
}

func normalizeNewTextEOL(content, newText string) string {
	if !strings.Contains(newText, "\n") || strings.Contains(newText, "\r\n") {
		return newText
	}
	if !fileUsesCRLF(content) {
		return newText
	}
	return strings.ReplaceAll(newText, "\n", "\r\n")
}

func leadingWhitespace(s string) string {
	i := 0
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	return s[:i]
}

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

// maxMatchLineHint caps how many match line numbers are listed in
// non-unique-anchor errors before the "(showing first N)" truncation note.
const maxMatchLineHint = 10

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
		if len(out) >= maxMatchLineHint {
			break
		}
		pos = abs + len(oldText)
		if pos >= len(content) {
			break
		}
	}
	return out
}

func formatMatchLines(lines []int) string {
	parts := make([]string, len(lines))
	for i, n := range lines {
		parts[i] = fmt.Sprintf("%d", n)
	}
	return strings.Join(parts, ", ")
}

// MatchLineHint renders the "Matches start at line(s): ..." suffix for a
// non-unique old_text, mirroring the edit-file error contract. It caps the
// listed lines at maxMatchLineHint and appends a "(showing first N)" note
// when more matches exist than are shown. Returns "" when there are no
// match lines to report.
func MatchLineHint(count int, lines []int) string {
	if len(lines) == 0 {
		return ""
	}
	more := ""
	if count > len(lines) {
		more = fmt.Sprintf(" (showing first %d)", len(lines))
	}
	return fmt.Sprintf("Matches start at line(s): %s%s.", formatMatchLines(lines), more)
}

// MatchLines returns the 1-based line numbers where oldText occurs in
// content, capped at maxMatchLineHint occurrences.
func MatchLines(content, oldText string) []int {
	return findMatchLineNumbers(content, oldText)
}

// NeighborHint diagnoses a failed old_text match and returns guidance such
// as indentation mismatch hints and the nearest matching lines. It is the
// same hint the edit_file tool appends to its not-found error.
func NeighborHint(content, oldText string) string {
	return diagnoseMatchFailure(content, oldText)
}

// FormatMatchLines renders match line numbers as a comma-separated list.
func FormatMatchLines(lines []int) string {
	return formatMatchLines(lines)
}

func lenientRecount(content, rawOld string, mr matchResult) (int, []int) {
	if mr.anchored || mr.transform == "" {
		return 0, nil
	}
	return lenientRecountRaw(content, rawOld, mr.transform)
}

func LenientRecount(content, rawOld, transform string) (int, []int) {
	if transform == "" {
		return 0, nil
	}
	return lenientRecountRaw(content, rawOld, transform)
}

func lenientRecountRaw(content, rawOld, transform string) (int, []int) {
	probe := rawOld
	if strings.Contains(transform, "read-file-wrapper-stripped") {
		if trimmed, changed := trimReadFileWrapperLines(probe); changed {
			probe = trimmed
		}
	}
	if strings.Contains(transform, "line-numbers-stripped") {
		probe = stripAllLineNumberPrefixes(probe)
	}
	if probe == "" {
		return 0, nil
	}

	if strings.Contains(transform, "crlf-converted") {
		return strings.Count(content, strings.ReplaceAll(probe, "\n", "\r\n")), nil
	}

	if strings.Contains(transform, "line-numbers-stripped") {
		strippedFile := guardedStripAllLineNumberPrefixes(content)
		return countLineAligned(strippedFile, probe), nil
	}
	trimmedOld := make([]string, 0, strings.Count(probe, "\n")+1)

	rstripOnly := strings.Contains(transform, "trailing-whitespace-tolerant")
	for _, l := range strings.Split(probe, "\n") {
		if rstripOnly {
			trimmedOld = append(trimmedOld, strings.TrimRight(l, " \t"))
		} else {
			trimmedOld = append(trimmedOld, strings.TrimSpace(l))
		}
	}
	fileLines := strings.Split(content, "\n")
	nOld := len(trimmedOld)
	count := 0
	var lines []int
	for start := 0; start+nOld <= len(fileLines); start++ {
		matched := true
		for j := range trimmedOld {
			var cand string
			if rstripOnly {
				cand = strings.TrimRight(fileLines[start+j], " \t")
			} else {
				cand = strings.TrimSpace(fileLines[start+j])
			}
			if cand != trimmedOld[j] {
				matched = false
				break
			}
		}
		if matched {
			count++
			if len(lines) < 10 {
				lines = append(lines, start+1)
			}
		}
	}
	return count, lines
}

func tryFuzzyLineMatch(content, oldText string) string {
	oldLines := strings.Split(oldText, "\n")
	if len(oldLines) == 0 {
		return ""
	}

	trimmedOld := make([]string, len(oldLines))
	for i, l := range oldLines {
		trimmedOld[i] = strings.TrimSpace(l)
	}

	fileLines := strings.Split(content, "\n")
	nFile := len(fileLines)
	nOld := len(trimmedOld)

	for start := 0; start <= nFile-nOld; start++ {
		matched := true
		for j := 0; j < nOld; j++ {
			if strings.TrimSpace(fileLines[start+j]) != trimmedOld[j] {
				matched = false
				break
			}
		}
		if matched {

			return strings.Join(fileLines[start:start+nOld], "\n")
		}
	}
	return ""
}
