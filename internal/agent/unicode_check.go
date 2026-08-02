package agent

import (
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

// Invisible / Problematic Unicode Character Detection in File Writes
//
// Research basis: LLMs (GPT, Claude, Gemini, GLM, DeepSeek) frequently
// introduce typographically "pretty" Unicode characters into source code
// when generating or editing files. These characters are visually similar
// or identical to their ASCII equivalents but cause compilation errors,
// runtime failures, or invisible bugs across all programming languages.
//
// Known problematic categories:
//   1. Smart/curly quotes — break string literals in every language.
//   2. Non-breaking space (U+00A0) — visually identical to regular space.
//   3. Em-dash and en-dash — can leak into identifiers, flags, paths.
//   4. Zero-width characters (U+200B, U+200C, U+200D, U+FEFF) — invisible.
//   5. BOM (U+FEFF) at non-start positions — causes parsing failures.
//   6. Fullwidth variants — visually similar to ASCII, break parsers.
//
// Competitor analysis:
//   - Claude Code: no detection (relies on go/parser catching syntax errors)
//   - Cursor: no detection (relies on language server diagnostics)
//   - Cline/OpenHands: no detection
//   - Aider: no detection
//   - This is a gap in ALL major AI coding agents.
//
// ggcode's approach: delta-based detection — only flag problematic Unicode
// characters INTRODUCED by this edit (present in newContent but not
// oldContent). Zero LLM cost, <1ms per file.

// unicodeConfusable describes a problematic Unicode character and its
// recommended ASCII replacement.
type unicodeConfusable struct {
	rune     rune
	name     string // human-readable name
	ascii    rune   // recommended replacement (0 = remove)
	severity string // "error" (breaks compilation) or "warning" (style)
}

// Problematic Unicode characters that LLMs commonly introduce.
var problematicUnicodeChars = []unicodeConfusable{
	// Smart quotes — break string literals in every language
	{'\u2018', "left single quote", '\'', "error"},
	{'\u2019', "right single quote", '\'', "error"},
	{'\u201c', "left double quote", '"', "error"},
	{'\u201d', "right double quote", '"', "error"},
	{'\u201a', "single low-9 quote", '\'', "error"},
	{'\u201e', "double low-9 quote", '"', "error"},
	{'\u2032', "prime", '\'', "error"},
	{'\u2033', "double prime", '"', "error"},

	// Non-breaking space — invisible, breaks indentation-sensitive code
	{'\u00a0', "non-breaking space", ' ', "error"},

	// Zero-width characters — completely invisible, break identifiers
	{'\u200b', "zero-width space", 0, "error"},
	{'\u200c', "zero-width non-joiner", 0, "error"},
	{'\u200d', "zero-width joiner", 0, "error"},
	{'\u2060', "word joiner", 0, "error"},
	{'\ufeff', "BOM / zero-width no-break space", 0, "error"},

	// Dashes — can leak into identifiers, flags, paths
	{'\u2013', "en-dash", '-', "warning"},
	{'\u2014', "em-dash", '-', "warning"},
	{'\u2015', "horizontal bar", '-', "warning"},

	// Fullwidth variants — visually similar to ASCII, break parsers
	{'\uff01', "fullwidth exclamation", '!', "warning"},
	{'\uff08', "fullwidth left paren", '(', "warning"},
	{'\uff09', "fullwidth right paren", ')', "warning"},
	{'\uff1b', "fullwidth semicolon", ';', "warning"},
	{'\uff1d', "fullwidth equals", '=', "warning"},
	{'\uff5b', "fullwidth left brace", '{', "warning"},
	{'\uff5d', "fullwidth right brace", '}', "warning"},

	// Other confusables
	{'\u00b7', "middle dot", '*', "warning"},
	{'\u2026', "horizontal ellipsis", 0, "warning"},
}

// unicodeCharMap is built in init() for O(1) rune lookup.
var unicodeCharMap map[rune]unicodeConfusable

func init() {
	unicodeCharMap = make(map[rune]unicodeConfusable, len(problematicUnicodeChars))
	for _, c := range problematicUnicodeChars {
		unicodeCharMap[c.rune] = c
	}
}

// maxUnicodeWarnings caps the number of character-type warnings per write.
const maxUnicodeWarnings = 5

// checkUnicodeChars detects problematic Unicode characters INTRODUCED by
// this edit (present in newContent but not in oldContent). Returns a
// non-empty guidance string if issues are detected.
//
// Delta-based detection avoids false positives on pre-existing content.
func checkUnicodeChars(filePath, oldContent, newContent string) string {
	// Count how many of each problematic char was in the old content so we
	// can compute the delta (newly introduced instances).
	oldCounts := make(map[rune]int)
	for _, r := range oldContent {
		if _, ok := unicodeCharMap[r]; ok {
			oldCounts[r]++
		}
	}

	// Count newly introduced instances in new content.
	type charFinding struct {
		confusable unicodeConfusable
		count      int // newly introduced count
		firstLine  int // 1-based line number of first occurrence
	}

	var findings []charFinding
	findingMap := make(map[rune]int) // rune -> index in findings

	lineNum := 1
	for _, r := range newContent {
		if r == '\n' {
			lineNum++
			continue
		}
		if info, ok := unicodeCharMap[r]; ok {
			// Compute remaining count (how many of this char existed before)
			if oldCounts[r] > 0 {
				oldCounts[r]--
				continue // this instance was pre-existing, not introduced
			}

			// This is a newly introduced problematic character
			idx, exists := findingMap[r]
			if !exists {
				findingMap[r] = len(findings)
				findings = append(findings, charFinding{
					confusable: info,
					count:      1,
					firstLine:  lineNum,
				})
			} else {
				findings[idx].count++
			}
		}
	}

	if len(findings) == 0 {
		return ""
	}

	debug.Log("unicode-check", "detected %d type(s) of problematic Unicode chars in %s", len(findings), filePath)

	// Separate errors from warnings for clearer messaging.
	var errs, warns []charFinding
	for _, f := range findings {
		if f.confusable.severity == "error" {
			errs = append(errs, f)
		} else {
			warns = append(warns, f)
		}
	}

	// Prioritize errors, then warnings, cap at maxUnicodeWarnings.
	var selected []charFinding
	selected = append(selected, errs...)
	if len(selected) < maxUnicodeWarnings {
		remaining := maxUnicodeWarnings - len(selected)
		if len(warns) > remaining {
			warns = warns[:remaining]
		}
		selected = append(selected, warns...)
	} else {
		selected = selected[:maxUnicodeWarnings]
	}

	totalIntroduced := 0
	for _, f := range findings {
		totalIntroduced += f.count
	}

	var b strings.Builder
	b.WriteString("[Problematic Unicode characters detected]")
	b.WriteString(fmt.Sprintf("\n%d problematic Unicode character(s) introduced by this edit in %s.", totalIntroduced, filePath))
	b.WriteString("\nThese characters cause invisible compilation errors or subtle bugs:")

	for _, f := range selected {
		name := f.confusable.name
		if f.confusable.ascii != 0 {
			name = fmt.Sprintf("%s (U+%04X), replace with '%c'", name, f.confusable.rune, f.confusable.ascii)
		} else {
			name = fmt.Sprintf("%s (U+%04X), remove it", name, f.confusable.rune)
		}
		b.WriteString(fmt.Sprintf("\n  - %dx %s, first at line %d", f.count, name, f.firstLine))
	}

	if len(findings) > len(selected) {
		b.WriteString(fmt.Sprintf("\n  ...and %d more character type(s).", len(findings)-len(selected)))
	}

	b.WriteString("\nReplace these with their ASCII equivalents and re-write the file.")

	return b.String()
}
