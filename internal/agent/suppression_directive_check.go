package agent

// Diagnostic Suppression Directive Detector
//
// Research basis: Reward hacking / specification gaming literature (arXiv:
// 2507.05619, "Detecting and Mitigating Reward Hacking in RL") identifies six
// categories of misaligned behavior, including "reward tampering" - modifying
// the verification mechanism rather than solving the problem. In coding agents,
// the most common form is adding lint/type/coverage suppression directives to
// silence diagnostic signals instead of fixing the root cause:
//
//   - Go:      //nolint, //revive:disable, //gosec:disable, //lint:ignore
//   - Python:  # type: ignore, # noqa, # pragma: no cover, # pylint: disable
//   - JS/TS:   eslint-disable, /* eslint-disable */, // eslint-disable-next-line
//   - Ruby:    # rubocop:disable
//   - Java:    @SuppressWarnings
//
// Gap: jsts_antipattern_check.go covers @ts-ignore/@ts-nocheck/@ts-expect-error
// (TypeScript-specific), but NO check covers the broader cross-language linter
// suppression directives. This detector fills that gap.
//
// The detector only fires on NEWLY ADDED suppressions (comparing old vs new
// content) to avoid false positives on pre-existing code.

import (
	"fmt"
	"regexp"
	"strings"
)

const maxSuppressWarnings = 5

// suppressionDirective defines a lint/type/coverage suppression pattern.
type suppressionDirective struct {
	pattern         *regexp.Regexp
	description     string
	languages       []Language // empty = any language
	requiresRule    bool       // true = require specific rule code (scoped), false = bare only
	checkLinePrefix bool       // true = check for line comment prefix (for prose detection)
}

// compileSuppressionDirectives returns the list of suppression patterns.
// Patterns are compiled once at init.
var suppressionDirectives = func() []suppressionDirective {
	return []suppressionDirective{
		// --- Go linter suppressions ---
		{pattern: regexp.MustCompile(`(?m)//\s*nolint`), description: "//nolint suppresses Go linter warnings (golangci-lint)", languages: []Language{LangGo}, requiresRule: true, checkLinePrefix: true},
		{pattern: regexp.MustCompile(`(?m)//\s*revive:disable`), description: "//revive:disable suppresses revive linter warnings", languages: []Language{LangGo}, requiresRule: false, checkLinePrefix: true},
		{pattern: regexp.MustCompile(`(?m)//\s*gosec:disable`), description: "//gosec:disable suppresses gosec security warnings", languages: []Language{LangGo}, requiresRule: false, checkLinePrefix: true},
		{pattern: regexp.MustCompile(`(?m)//\s*lint:ignore`), description: "//lint:ignore suppresses lint warnings", languages: []Language{LangGo}, requiresRule: false, checkLinePrefix: true},

		// --- Python suppressions ---
		{pattern: regexp.MustCompile(`(?m)#\s*type:\s*ignore`), description: "# type: ignore suppresses Python type checker (mypy/pyright) errors", languages: []Language{LangPython}, requiresRule: true, checkLinePrefix: true},
		{pattern: regexp.MustCompile(`(?m)#\s*noqa`), description: "# noqa suppresses Python linter (flake8/ruff) warnings", languages: []Language{LangPython}, requiresRule: true, checkLinePrefix: true},
		{pattern: regexp.MustCompile(`(?m)#\s*pragma:\s*no\s*cover`), description: "# pragma: no cover excludes code from coverage measurement", languages: []Language{LangPython}, requiresRule: false, checkLinePrefix: true},
		{pattern: regexp.MustCompile(`(?m)#\s*pylint:\s*disable`), description: "# pylint: disable suppresses pylint warnings", languages: []Language{LangPython}, requiresRule: false, checkLinePrefix: true},

		// --- JS/TS lint suppressions (NOT @ts-* which are in jsts_antipattern) ---
		{pattern: regexp.MustCompile(`(?i)eslint-disable`), description: "eslint-disable suppresses ESLint warnings", languages: []Language{LangJSTS, LangMarkup}, requiresRule: false, checkLinePrefix: true},
		// #1778 case 4: .vue/.svelte single-file components carry ESLint
		// directives inside their <script> block and stylelint ones in
		// <style> - .vue is stylelint's HOME turf. Both map to LangMarkup,
		// which excluded them entirely.
		{pattern: regexp.MustCompile(`(?i)stylelint-disable`), description: "stylelint-disable suppresses Stylelint warnings", languages: []Language{LangJSTS, LangMarkup}, requiresRule: false, checkLinePrefix: true},

		// --- Ruby suppressions (only for .rb files, NOT unknown extensions) ---
		{pattern: regexp.MustCompile(`(?m)#\s*rubocop:disable`), description: "# rubocop:disable suppresses RuboCop warnings", languages: []Language{LangRuby}, requiresRule: false, checkLinePrefix: true},

		// --- Java suppressions (only for .java files, NOT unknown extensions) ---
		{pattern: regexp.MustCompile(`@SuppressWarnings`), description: "@SuppressWarnings suppresses Java compiler/linter warnings", languages: []Language{LangJava}, requiresRule: false, checkLinePrefix: false},
	}
}()

// checkSuppressionDirectives detects newly added lint/type/coverage suppression
// directives. It compares old vs new content to only flag ADDED suppressions,
// avoiding noise from pre-existing code.
func checkSuppressionDirectives(fp, oldContent, newContent string) []string {
	if strings.TrimSpace(newContent) == "" {
		return nil
	}

	lang := detectLanguage(fp)
	var warnings []string

	for _, sd := range suppressionDirectives {
		// Skip if language filter doesn't match
		// LangAny(0) means "unknown language" - skip language-bound patterns
		if len(sd.languages) > 0 {
			if lang == 0 {
				// Unknown extension - skip all language-bound patterns
				continue
			}
			if !langInList(sd.languages, lang) {
				continue
			}
		}

		newMatches := sd.pattern.FindAllStringIndex(newContent, -1)
		if len(newMatches) == 0 {
			continue
		}

		// #572 B2: count only BARE suppressions. Scoped forms with a specific
		// rule code (//nolint:errcheck, # noqa: E501, # type: ignore[assignment])
		// are legitimate targeted suppressions and must not warn. Bare and
		// scoped forms are distinguished per match, on the line containing it,
		// so trailing comments ("def f(): pass  # noqa") are judged correctly.
		added := countBareMatches(newContent, &sd) - countBareMatches(oldContent, &sd)
		if added <= 0 {
			continue
		}

		// Find line numbers of the newly added instances for actionable feedback
		lines := findAddedSuppressionLines(newContent, oldContent, sd.pattern, sd.requiresRule, sd.checkLinePrefix)
		excerpt := ""
		if len(lines) > 0 {
			excerpt = fmt.Sprintf(" (line %d)", lines[0])
		}

		warnings = append(warnings, fmt.Sprintf(
			"Added %d suppression directive(s): %s%s. This silences diagnostic signals instead of fixing the root cause. Consider addressing the underlying lint/type/coverage issue rather than suppressing it.",
			added, sd.description, excerpt,
		))

		if len(warnings) >= maxSuppressWarnings {
			warnings = append(warnings, fmt.Sprintf("[... more suppression directives found (showing first %d)]", maxSuppressWarnings))
			break
		}
	}

	return warnings
}

// countBareMatches counts pattern matches in content that are in bare form
// (no rule code) for requiresRule directives; all matches otherwise. Each
// match is judged on the line that contains it, so trailing-comment
// suppressions are handled correctly.
func countBareMatches(content string, sd *suppressionDirective) int {
	count := 0
	for _, loc := range sd.pattern.FindAllStringIndex(content, -1) {
		lineStart := strings.LastIndexByte(content[:loc[0]], '\n') + 1
		lineEnd := len(content)
		if idx := strings.IndexByte(content[loc[1]:], '\n'); idx >= 0 {
			lineEnd = loc[1] + idx
		}
		line := content[lineStart:lineEnd]
		matched := content[loc[0]:loc[1]]
		// #1778 case 3: prose/string-literal guard - a match inside code
		// (const banner = "eslint-disable") counted as a suppression with
		// NO line to point at. A REAL directive's match text itself rides
		// a comment marker ("# noqa", "// nolint") or the line embeds one
		// for trailing forms; a bare keyword in a string does neither.
		if sd.checkLinePrefix && !matchRidesComment(line, matched) {
			continue
		}
		// #3607: prose riding INSIDE a comment - the most natural place to
		// DISCUSS suppression policies - inverted the #1778 guard: the
		// comment marker on the line let "do not use eslint-disable in this
		// repo" or docstring prose "Use # noqa only as last resort" count as
		// real directives. Adjacency tells them apart: prose mentions have
		// natural words welded to the match (a letter word ends the prefix
		// and/or a lowercase sentence continues after), while real forms
		// start at the comment marker, end the line, or trail code.
		if proseMention(line, loc[0]-lineStart, matched) {
			continue
		}
		if isBareSuppression(line, matched, sd.requiresRule) {
			count++
		}
	}
	return count
}

// matchRidesComment reports whether the matched directive text is (or
// sits on) a comment: the match itself contains a comment marker, or the
// line embeds one (trailing forms like "x = 1  # noqa").
func matchRidesComment(line, matched string) bool {
	for _, marker := range []string{"//", "#", "--", "/*", "*"} {
		if strings.Contains(matched, marker) || strings.Contains(line, marker) {
			return true
		}
	}
	return false
}

// proseMention reports whether the match at line[off:off+len(matched)] is
// natural-language prose DISCUSSING a suppression directive rather than
// the directive itself (#3607). Signals: a letter word ending the prefix
// ("do not use <match>", "Use <match>") plus a prose continuation after
// ("<match> in this repo", "<match> is banned here").
// #3618: the old marker-less "either side suffices" shortcut inverted at
// comment-start position - the prefix is always the comment marker there,
// so wordAfter alone had to distinguish prose from real rule arguments
// and pure-lowercase rule names ("eslint-disable semi") were skipped as
// prose. Disambiguator: English function words (is/in/the/only/as/...)
// never open a rule list. With a word before, a continuation counts when
// it opens with a function word or spans 2+ lowercase words (single
// trailing adverbs like "sparingly" are accepted as a narrow residual);
// at comment-start, ONLY a function-word opener counts as prose - a bare
// lowercase identifier re-fires as a directive argument.
// For matches that embed a comment marker ("# noqa"), prose requires BOTH
// sides so bare trailing forms ("value  # noqa" - nothing after) and
// comment-start forms (line begins at the marker) stay flagged.
func proseMention(line string, off int, matched string) bool {
	if off < 0 || off+len(matched) > len(line) {
		return false
	}
	prefix := strings.TrimRight(line[:off], " \t")
	wordBefore := len(prefix) > 0 && isLetterByte(prefix[len(prefix)-1])
	suffix := strings.TrimLeft(line[off+len(matched):], " \t")
	matchedHasMarker := strings.ContainsAny(matched, "#") || strings.Contains(matched, "//") || strings.Contains(matched, "/*") || strings.Contains(matched, "--")
	if matchedHasMarker {
		return wordBefore && proseContinuation(suffix, false)
	}
	// Marker-less match ("eslint-disable"): a word before + prose
	// continuation, or (comment-start) a function-word opener.
	if wordBefore {
		return proseContinuation(suffix, false)
	}
	return proseContinuation(suffix, true)
}

// proseWords are English function words that never open a lint rule list
// but do open prose continuations ("... is banned", "... in this repo").
var proseWords = map[string]bool{
	"a": true, "an": true, "and": true, "are": true, "as": true,
	"here": true, "in": true, "is": true, "not": true, "of": true,
	"only": true, "or": true, "the": true, "this": true, "to": true,
	"we": true, "when": true, "where": true,
}

// proseContinuation reports whether suffix reads as a natural-language
// sentence continuation. With requireFunctionWord the first token MUST be
// a function word (comment-start position: a bare lowercase identifier
// like "semi" is a rule argument, not prose); otherwise a function-word
// opener or a 2+ lowercase-word span both count (rule arguments carry
// hyphens/commas/colons and never satisfy either).
func proseContinuation(suffix string, requireFunctionWord bool) bool {
	if suffix == "" || suffix[0] < 'a' || suffix[0] > 'z' {
		return false
	}
	first := suffix
	if idx := strings.IndexAny(suffix, " \t"); idx >= 0 {
		first = suffix[:idx]
	}
	if !isLowerWord(first) {
		return false
	}
	if proseWords[first] {
		return true
	}
	if requireFunctionWord {
		return false
	}
	// Word-before context: any lowercase word after a letter word is the
	// both-sides prose signal (single trailing adverbs included).
	return true
}

// isLowerWord reports whether s is a non-empty pure lowercase letter word.
func isLowerWord(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < 'a' || s[i] > 'z' {
			return false
		}
	}
	return true
}

func isLetterByte(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// containsLang checks if a language is in a list.
func langInList(langs []Language, target Language) bool {
	for _, lg := range langs {
		if lg == target {
			return true
		}
	}
	return false
}

// isBareSuppression checks if a matched line is a "bare" suppression
// (without specific rule code) vs a scoped one (with rule).
//
// For requiresRule=true patterns (//nolint, # noqa, etc.), we only flag
// the bare form (no rule number) as problematic. Scoped forms like
// //nolint:errcheck or # noqa: E501 are considered legitimate.
func isBareSuppression(line, matched string, requiresRule bool) bool {
	if !requiresRule {
		// Pattern doesn't distinguish bare vs scoped - all matches are flagged
		return true
	}
	if matched == "" {
		return true
	}

	// #572 B2: locate the match within the line. Suppression comments are
	// usually TRAILING comments ("def f(): pass  # noqa"), so trimming the
	// match from the start of the line — as the old code did — left `rest`
	// holding the entire line and misclassified scoped forms as bare.
	idx := strings.LastIndex(line, matched)
	if idx < 0 {
		idx = 0
	}
	rest := strings.TrimSpace(line[idx+len(matched):])

	// If nothing comes after, it's bare
	if rest == "" {
		return true
	}

	// If it starts with colon or bracket, it's scoped (legitimate)
	// Examples: //nolint:errcheck, # noqa: E501, # type: ignore[assignment]
	// Exception: ":all" is a blanket suppress-everything rule — as dangerous
	// as the bare form (#571 expects //nolint:all to fire).
	if strings.HasPrefix(rest, ":") || strings.HasPrefix(rest, "[") {
		if rest == ":all" || strings.HasPrefix(rest, ":all,") {
			return true
		}
		return false
	}

	// For Python's # noqa, any alphanumeric code after space is scoped
	// Examples: # noqa E501, # noqa: F401
	if matched == "# noqa" {
		// Check if rest looks like a rule code (alphanumeric, possibly with : or ,)
		return !regexp.MustCompile(`^[A-Z0-9_:,]+$`).MatchString(rest)
	}

	// For //nolint, check if rest starts with colon or specific rules
	if strings.Contains(matched, "nolint") {
		// //nolint:all, //nolint:errcheck, //nolint:gosec are scoped
		// //nolint (bare) is problematic
		// Exception: ":all" is a blanket suppress-everything rule — as
		// dangerous as the bare form (#571 expects it to fire).
		if rest == ":all" || strings.HasPrefix(rest, ":all,") || rest == "all" {
			return true
		}
		return !strings.HasPrefix(rest, ":")
	}

	// For Python's # type: ignore, mypy's syntax makes it scoped only via
	// error codes in brackets (# type: ignore[return-value]) - already
	// handled by the HasPrefix("[") branch above. A space followed by free
	// text is the DOCUMENTED recommended form for bare ignores with an
	// explanation; the old default treated it as scoped and never reported
	// it (#1500).
	if strings.Contains(matched, "type:") && strings.Contains(matched, "ignore") {
		return true
	}

	// Default: if there's something after, assume it's scoped
	return false
}

// findAddedSuppressionLines returns line numbers of suppression directives
// that appear in newContent but not in oldContent.
func findAddedSuppressionLines(newContent, oldContent string, re *regexp.Regexp, requiresRule, checkLinePrefix bool) []int {
	newLines := strings.Split(newContent, "\n")
	var oldLineSet map[string]bool
	if oldContent != "" {
		oldLines := strings.Split(oldContent, "\n")
		oldLineSet = make(map[string]bool, len(oldLines))
		for _, l := range oldLines {
			oldLineSet[strings.TrimSpace(l)] = true
		}
	}

	var result []int
	for idx, ln := range newLines {
		if !re.MatchString(ln) {
			continue
		}

		// For line-comment based patterns, verify this is actually in a comment context
		// to avoid matching prose in Markdown/unknown files
		if checkLinePrefix {
			trimmed := strings.TrimSpace(ln)
			// Check if line starts with comment prefix (//, #, --)
			if !strings.HasPrefix(trimmed, "//") &&
				!strings.HasPrefix(trimmed, "#") &&
				!strings.HasPrefix(trimmed, "--") &&
				!strings.Contains(trimmed, "/*") &&
				!strings.Contains(trimmed, "*") {
				// This is prose, not a comment - skip it
				continue
			}
		}

		// Check if this is a bare (problematic) vs scoped (legitimate) suppression
		matched := re.FindString(ln)
		if !isBareSuppression(ln, matched, requiresRule) {
			// Scoped form with rule code - not a problem
			continue
		}

		// If this exact line didn't exist in old content, it's newly added
		if oldLineSet == nil || !oldLineSet[strings.TrimSpace(ln)] {
			result = append(result, idx+1)
		}
	}
	return result
}
