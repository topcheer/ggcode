package agent

import (
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

type unicodeConfusable struct {
	rune     rune
	name     string
	ascii    rune
	severity string
}

var problematicUnicodeChars = []unicodeConfusable{

	{'\u2018', "left single quote", '\'', "error"},
	{'\u2019', "right single quote", '\'', "error"},
	{'\u201c', "left double quote", '"', "error"},
	{'\u201d', "right double quote", '"', "error"},
	{'\u201a', "single low-9 quote", '\'', "error"},
	{'\u201e', "double low-9 quote", '"', "error"},
	{'\u2032', "prime", '\'', "error"},
	{'\u2033', "double prime", '"', "error"},

	{'\u00a0', "non-breaking space", ' ', "error"},

	{'\u200b', "zero-width space", 0, "error"},
	{'\u200c', "zero-width non-joiner", 0, "error"},
	{'\u200d', "zero-width joiner", 0, "error"},
	{'\u2060', "word joiner", 0, "error"},
	{'\ufeff', "BOM / zero-width no-break space", 0, "error"},

	{'\u2013', "en-dash", '-', "warning"},
	{'\u2014', "em-dash", '-', "warning"},
	{'\u2015', "horizontal bar", '-', "warning"},

	{'\uff01', "fullwidth exclamation", '!', "warning"},
	{'\uff08', "fullwidth left paren", '(', "warning"},
	{'\uff09', "fullwidth right paren", ')', "warning"},
	{'\uff1b', "fullwidth semicolon", ';', "warning"},
	{'\uff1d', "fullwidth equals", '=', "warning"},
	{'\uff5b', "fullwidth left brace", '{', "warning"},
	{'\uff5d', "fullwidth right brace", '}', "warning"},

	{'\u00b7', "middle dot", '*', "warning"},
	{'\u2026', "horizontal ellipsis", 0, "warning"},
}

var unicodeCharMap map[rune]unicodeConfusable

func init() {
	unicodeCharMap = make(map[rune]unicodeConfusable, len(problematicUnicodeChars))
	for _, c := range problematicUnicodeChars {
		unicodeCharMap[c.rune] = c
	}
}

const maxUnicodeWarnings = 5

type charFinding struct {
	confusable unicodeConfusable
	count      int
	firstLine  int
	allInCJK   bool
}

func checkUnicodeChars(filePath, oldContent, newContent string) string {
	findings := scanUnicodeDelta(oldContent, newContent)
	if len(findings) == 0 {
		return ""
	}

	debug.Log("unicode-check", "detected %d type(s) of problematic Unicode chars in %s", len(findings), filePath)
	return formatUnicodeFindings(filePath, findings)
}

func scanUnicodeDelta(oldContent, newContent string) []charFinding {

	oldCounts := make(map[rune]int)
	for _, r := range oldContent {
		if _, ok := unicodeCharMap[r]; ok {
			oldCounts[r]++
		}
	}

	var findings []charFinding
	findingMap := make(map[rune]int)

	for i, line := range strings.Split(newContent, "\n") {
		lineHasCJK := false
		for _, r := range line {
			if isCJKRune(r) {
				lineHasCJK = true
				break
			}
		}
		for _, r := range line {
			info, ok := unicodeCharMap[r]
			if !ok {
				continue
			}

			if oldCounts[r] > 0 {
				oldCounts[r]--
				continue
			}

			if idx, exists := findingMap[r]; exists {
				findings[idx].count++
				if !lineHasCJK {
					findings[idx].allInCJK = false
				}
			} else {
				findingMap[r] = len(findings)
				findings = append(findings, charFinding{
					confusable: info,
					count:      1,
					firstLine:  i + 1,
					allInCJK:   lineHasCJK,
				})
			}
		}
	}
	return findings
}

func isCJKRune(r rune) bool {
	switch {
	case r >= 0x4E00 && r <= 0x9FFF:
		return true
	case r >= 0x3400 && r <= 0x4DBF:
		return true
	case r >= 0x3040 && r <= 0x30FF:
		return true
	case r >= 0xAC00 && r <= 0xD7AF:
		return true
	case r >= 0xF900 && r <= 0xFAFF:
		return true
	}
	return false
}

func isInvisibleUnicode(r rune) bool {
	switch r {
	case '\u00a0', '\u200b', '\u200c', '\u200d', '\u2060', '\ufeff':
		return true
	}
	return false
}

func formatUnicodeFindings(filePath string, findings []charFinding) string {

	cleared := func(f charFinding) bool {
		return f.allInCJK && !isInvisibleUnicode(f.confusable.rune)
	}

	var errs, warns, cjkNotes []charFinding
	for _, f := range findings {
		switch {
		case cleared(f):
			cjkNotes = append(cjkNotes, f)
		case f.confusable.severity == "error":
			errs = append(errs, f)
		default:
			warns = append(warns, f)
		}
	}

	selected := append([]charFinding{}, errs...)
	if len(selected) < maxUnicodeWarnings {
		remaining := maxUnicodeWarnings - len(selected)
		if len(warns) > remaining {
			warns = warns[:remaining]
		}
		selected = append(selected, warns...)
	}
	if len(selected) < maxUnicodeWarnings {
		remaining := maxUnicodeWarnings - len(selected)
		if len(cjkNotes) > remaining {
			cjkNotes = cjkNotes[:remaining]
		}
		selected = append(selected, cjkNotes...)
	} else {
		selected = selected[:maxUnicodeWarnings]
	}

	totalIntroduced := 0
	for _, f := range findings {
		totalIntroduced += f.count
	}

	actionable := len(errs) + len(warns)

	var b strings.Builder
	b.WriteString("[Problematic Unicode characters detected]")
	b.WriteString(fmt.Sprintf("\n%d problematic Unicode character(s) introduced by this edit in %s.", totalIntroduced, filePath))
	if actionable > 0 {
		b.WriteString("\nThese characters cause invisible compilation errors or subtle bugs:")
	} else {
		b.WriteString("\nAll introduced instances sit on lines containing CJK script:")
	}

	for _, f := range selected {
		b.WriteString(fmt.Sprintf("\n  - %s", formatCharFinding(f)))
	}

	if len(findings) > len(selected) {
		b.WriteString(fmt.Sprintf("\n  ...and %d more character type(s).", len(findings)-len(selected)))
	}
	if actionable > 0 {
		b.WriteString("\nReplace these with their ASCII equivalents and re-write the file.")
	} else {
		b.WriteString("\nIf these sit inside string literals, comments, or prose they are standard CJK punctuation - keep them as-is; only replace instances that took the place of actual code delimiters.")
	}
	return b.String()
}

func formatCharFinding(f charFinding) string {
	name := f.confusable.name
	if f.allInCJK && !isInvisibleUnicode(f.confusable.rune) {

		return fmt.Sprintf("%dx %s (U+%04X), first at line %d - CJK context: standard CJK typography if inside a string, comment, or prose; only a problem if it replaced a code delimiter",
			f.count, name, f.confusable.rune, f.firstLine)
	}
	if f.confusable.ascii != 0 {
		name = fmt.Sprintf("%s (U+%04X), replace with '%c'", name, f.confusable.rune, f.confusable.ascii)
	} else {
		name = fmt.Sprintf("%s (U+%04X), remove it", name, f.confusable.rune)
	}
	return fmt.Sprintf("%dx %s, first at line %d", f.count, name, f.firstLine)
}
