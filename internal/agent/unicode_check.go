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

// charFinding records a newly introduced problematic character type.
type charFinding struct {
	confusable unicodeConfusable
	count      int // newly introduced count
	firstLine  int // 1-based line number of first occurrence
	// allInCJK is true when EVERY introduced instance of this character sits
	// on a line containing CJK script (#1217). Such instances are treated as
	// legitimate CJK typography rather than broken ASCII delimiters.
	allInCJK bool
}

// checkUnicodeChars detects problematic Unicode characters INTRODUCED by
// this edit (present in newContent but not in oldContent). Returns a
// non-empty guidance string if issues are detected.
//
// Delta-based detection avoids false positives on pre-existing content.
func checkUnicodeChars(filePath, oldContent, newContent string) string {
	findings := scanUnicodeDelta(oldContent, newContent)
	if len(findings) == 0 {
		return ""
	}

	debug.Log("unicode-check", "detected %d type(s) of problematic Unicode chars in %s", len(findings), filePath)
	return formatUnicodeFindings(filePath, findings)
}

// scanUnicodeDelta scans newContent and returns findings for problematic
// Unicode characters that were NOT present in oldContent (delta detection).
func scanUnicodeDelta(oldContent, newContent string) []charFinding {
	// Count how many of each problematic char was in the old content.
	oldCounts := make(map[rune]int)
	for _, r := range oldContent {
		if _, ok := unicodeCharMap[r]; ok {
			oldCounts[r]++
		}
	}

	var findings []charFinding
	findingMap := make(map[rune]int) // rune -> index in findings

	// Line-oriented scan (#1217): visible punctuation introduced on a line
	// that also contains CJK script is overwhelmingly likely to be legitimate
	// Chinese/Japanese/Korean typography (quotes per GB/T 15834, fullwidth
	// parens, dashes, ellipsis) inside prose, comments, or string literals,
	// whereas a broken ASCII delimiter shows up on otherwise pure-ASCII
	// lines. Per-finding allInCJK drives the wording downgrade in
	// formatUnicodeFindings; invisible characters are exempt.
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
			// Skip if this instance was pre-existing (not introduced by this edit).
			if oldCounts[r] > 0 {
				oldCounts[r]--
				continue
			}
			// Record newly introduced problematic character.
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

// isCJKRune reports whether r belongs to a CJK script (Han, Hiragana,
// Katakana, Hangul). Lines containing CJK script are treated as prose /
// comment / string-literal content for Unicode severity purposes: curly
// quotes and fullwidth punctuation there are standard CJK typography (e.g.
// GB/T 15834 for Chinese), not broken ASCII delimiters (#1217). Fullwidth
// forms (U+FF00-U+FFEF) are deliberately NOT included - they are the
// flagged characters themselves and must not self-certify their line.
func isCJKRune(r rune) bool {
	switch {
	case r >= 0x4E00 && r <= 0x9FFF: // CJK Unified Ideographs
		return true
	case r >= 0x3400 && r <= 0x4DBF: // CJK Extension A
		return true
	case r >= 0x3040 && r <= 0x30FF: // Hiragana + Katakana
		return true
	case r >= 0xAC00 && r <= 0xD7AF: // Hangul syllables
		return true
	case r >= 0xF900 && r <= 0xFAFF: // CJK Compatibility Ideographs
		return true
	}
	return false
}

// isInvisibleUnicode reports whether the problematic character is invisible
// (or renders as plain whitespace). Such characters are never legitimate
// typography - not even in CJK text, which uses U+3000 for spacing - so
// they keep their removal directive regardless of line context (#1217).
func isInvisibleUnicode(r rune) bool {
	switch r {
	case '\u00a0', '\u200b', '\u200c', '\u200d', '\u2060', '\ufeff':
		return true
	}
	return false
}

// formatUnicodeFindings renders a human-readable warning string from
// the detected findings, prioritizing errors over warnings.
func formatUnicodeFindings(filePath string, findings []charFinding) string {
	// #1217: a finding is "context-cleared" when every introduced instance
	// sits on a line containing CJK script AND the character is visible.
	// Curly quotes, dashes, and fullwidth punctuation are standard CJK
	// typography (GB/T 15834) inside strings, comments, and prose, so they
	// must not carry an error-tier ASCII replacement directive. Invisible
	// characters (zero-width, BOM, NBSP) are never legitimate and stay
	// actionable regardless of context.
	cleared := func(f charFinding) bool {
		return f.allInCJK && !isInvisibleUnicode(f.confusable.rune)
	}

	// Separate errors, warnings, and CJK-context notes for prioritized
	// messaging: actionable findings first, downgraded ones last.
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

	// Prioritize errors, then warnings, then CJK notes, cap at maxUnicodeWarnings.
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

// formatCharFinding renders a single character finding as a one-liner.
func formatCharFinding(f charFinding) string {
	name := f.confusable.name
	if f.allInCJK && !isInvisibleUnicode(f.confusable.rune) {
		// #1217: CJK-script context - never direct ASCII replacement, which
		// would corrupt legitimate Chinese/Japanese/Korean punctuation.
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
