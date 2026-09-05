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
		{pattern: regexp.MustCompile(`(?i)eslint-disable`), description: "eslint-disable suppresses ESLint warnings", languages: []Language{LangJSTS}, requiresRule: false, checkLinePrefix: true},
		{pattern: regexp.MustCompile(`(?i)stylelint-disable`), description: "stylelint-disable suppresses Stylelint warnings", languages: []Language{LangJSTS}, requiresRule: false, checkLinePrefix: true},

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
		if isBareSuppression(content[lineStart:lineEnd], content[loc[0]:loc[1]], sd.requiresRule) {
			count++
		}
	}
	return count
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
