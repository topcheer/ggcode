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
	pattern     *regexp.Regexp
	description string
	languages   []Language // empty = any language
}

// compileSuppressionDirectives returns the list of suppression patterns.
// Patterns are compiled once at init.
var suppressionDirectives = func() []suppressionDirective {
	return []suppressionDirective{
		// --- Go linter suppressions ---
		{pattern: regexp.MustCompile(`(?m)//\s*nolint`), description: "//nolint suppresses Go linter warnings (golangci-lint)", languages: []Language{LangGo}},
		{pattern: regexp.MustCompile(`(?m)//\s*revive:disable`), description: "//revive:disable suppresses revive linter warnings", languages: []Language{LangGo}},
		{pattern: regexp.MustCompile(`(?m)//\s*gosec:disable`), description: "//gosec:disable suppresses gosec security warnings", languages: []Language{LangGo}},
		{pattern: regexp.MustCompile(`(?m)//\s*lint:ignore`), description: "//lint:ignore suppresses lint warnings", languages: []Language{LangGo}},

		// --- Python suppressions ---
		{pattern: regexp.MustCompile(`(?m)#\s*type:\s*ignore`), description: "# type: ignore suppresses Python type checker (mypy/pyright) errors", languages: []Language{LangPython}},
		{pattern: regexp.MustCompile(`(?m)#\s*noqa`), description: "# noqa suppresses Python linter (flake8/ruff) warnings", languages: []Language{LangPython}},
		{pattern: regexp.MustCompile(`(?m)#\s*pragma:\s*no\s*cover`), description: "# pragma: no cover excludes code from coverage measurement", languages: []Language{LangPython}},
		{pattern: regexp.MustCompile(`(?m)#\s*pylint:\s*disable`), description: "# pylint: disable suppresses pylint warnings", languages: []Language{LangPython}},

		// --- JS/TS lint suppressions (NOT @ts-* which are in jsts_antipattern) ---
		{pattern: regexp.MustCompile(`(?i)eslint-disable`), description: "eslint-disable suppresses ESLint warnings", languages: []Language{LangJSTS}},
		{pattern: regexp.MustCompile(`(?i)stylelint-disable`), description: "stylelint-disable suppresses Stylelint warnings", languages: []Language{LangJSTS}},

		// --- Ruby suppressions ---
		{pattern: regexp.MustCompile(`(?m)#\s*rubocop:disable`), description: "# rubocop:disable suppresses RuboCop warnings", languages: []Language{LangAny}},

		// --- Java suppressions ---
		{pattern: regexp.MustCompile(`@SuppressWarnings`), description: "@SuppressWarnings suppresses Java compiler/linter warnings", languages: []Language{LangAny}},
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
		if len(sd.languages) > 0 && !langInList(sd.languages, lang) {
			continue
		}

		newMatches := sd.pattern.FindAllStringIndex(newContent, -1)
		if len(newMatches) == 0 {
			continue
		}

		// Count how many existed in old content (pre-existing)
		oldCount := len(sd.pattern.FindAllStringIndex(oldContent, -1))

		// Only warn on newly added suppressions
		added := len(newMatches) - oldCount
		if added <= 0 {
			continue
		}

		// Find line numbers of the newly added instances for actionable feedback
		lines := findAddedSuppressionLines(newContent, oldContent, sd.pattern)
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

// containsLang checks if a language is in a list.
func langInList(langs []Language, target Language) bool {
	for _, lg := range langs {
		if lg == target {
			return true
		}
	}
	return false
}

// findAddedSuppressionLines returns line numbers of suppression directives
// that appear in newContent but not in oldContent.
func findAddedSuppressionLines(newContent, oldContent string, re *regexp.Regexp) []int {
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
		// If this exact line didn't exist in old content, it's newly added
		if oldLineSet == nil || !oldLineSet[strings.TrimSpace(ln)] {
			result = append(result, idx+1)
		}
	}
	return result
}
