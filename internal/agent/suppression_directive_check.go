package agent

import (
	"fmt"
	"regexp"
	"strings"
)

const maxSuppressWarnings = 5

type suppressionDirective struct {
	pattern         *regexp.Regexp
	description     string
	languages       []Language
	requiresRule    bool
	checkLinePrefix bool
}

var suppressionDirectives = func() []suppressionDirective {
	return []suppressionDirective{

		{pattern: regexp.MustCompile(`(?m)//\s*nolint`), description: "//nolint suppresses Go linter warnings (golangci-lint)", languages: []Language{LangGo}, requiresRule: true, checkLinePrefix: true},
		{pattern: regexp.MustCompile(`(?m)//\s*revive:disable`), description: "//revive:disable suppresses revive linter warnings", languages: []Language{LangGo}, requiresRule: false, checkLinePrefix: true},
		{pattern: regexp.MustCompile(`(?m)//\s*gosec:disable`), description: "//gosec:disable suppresses gosec security warnings", languages: []Language{LangGo}, requiresRule: false, checkLinePrefix: true},
		{pattern: regexp.MustCompile(`(?m)//\s*lint:ignore`), description: "//lint:ignore suppresses lint warnings", languages: []Language{LangGo}, requiresRule: false, checkLinePrefix: true},

		{pattern: regexp.MustCompile(`(?m)#\s*type:\s*ignore`), description: "# type: ignore suppresses Python type checker (mypy/pyright) errors", languages: []Language{LangPython}, requiresRule: true, checkLinePrefix: true},
		{pattern: regexp.MustCompile(`(?m)#\s*noqa`), description: "# noqa suppresses Python linter (flake8/ruff) warnings", languages: []Language{LangPython}, requiresRule: true, checkLinePrefix: true},
		{pattern: regexp.MustCompile(`(?m)#\s*pragma:\s*no\s*cover`), description: "# pragma: no cover excludes code from coverage measurement", languages: []Language{LangPython}, requiresRule: false, checkLinePrefix: true},
		{pattern: regexp.MustCompile(`(?m)#\s*pylint:\s*disable`), description: "# pylint: disable suppresses pylint warnings", languages: []Language{LangPython}, requiresRule: false, checkLinePrefix: true},

		{pattern: regexp.MustCompile(`(?i)eslint-disable`), description: "eslint-disable suppresses ESLint warnings", languages: []Language{LangJSTS, LangMarkup}, requiresRule: false, checkLinePrefix: true},

		{pattern: regexp.MustCompile(`(?i)stylelint-disable`), description: "stylelint-disable suppresses Stylelint warnings", languages: []Language{LangJSTS, LangMarkup}, requiresRule: false, checkLinePrefix: true},

		{pattern: regexp.MustCompile(`(?m)#\s*rubocop:disable`), description: "# rubocop:disable suppresses RuboCop warnings", languages: []Language{LangRuby}, requiresRule: false, checkLinePrefix: true},

		{pattern: regexp.MustCompile(`@SuppressWarnings`), description: "@SuppressWarnings suppresses Java compiler/linter warnings", languages: []Language{LangJava}, requiresRule: false, checkLinePrefix: false},
	}
}()

func checkSuppressionDirectives(fp, oldContent, newContent string) []string {
	if strings.TrimSpace(newContent) == "" {
		return nil
	}

	lang := detectLanguage(fp)
	var warnings []string

	for _, sd := range suppressionDirectives {

		if len(sd.languages) > 0 {
			if lang == 0 {

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

		added := countBareMatches(newContent, &sd) - countBareMatches(oldContent, &sd)
		if added <= 0 {
			continue
		}

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

		if sd.checkLinePrefix && !matchRidesComment(line, matched) {
			continue
		}

		if proseMention(line, loc[0]-lineStart, matched) {
			continue
		}
		if isBareSuppression(line, matched, sd.requiresRule) {
			count++
		}
	}
	return count
}

func matchRidesComment(line, matched string) bool {
	for _, marker := range []string{"//", "#", "--", "/*", "*"} {
		if strings.Contains(matched, marker) || strings.Contains(line, marker) {
			return true
		}
	}
	return false
}

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

	if wordBefore {
		return proseContinuation(suffix, false)
	}
	return proseContinuation(suffix, true)
}

var proseWords = map[string]bool{
	"a": true, "an": true, "and": true, "are": true, "as": true,
	"here": true, "in": true, "is": true, "not": true, "of": true,
	"only": true, "or": true, "the": true, "this": true, "to": true,
	"we": true, "when": true, "where": true,
}

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

	return true
}

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

func langInList(langs []Language, target Language) bool {
	for _, lg := range langs {
		if lg == target {
			return true
		}
	}
	return false
}

func isBareSuppression(line, matched string, requiresRule bool) bool {
	if !requiresRule {

		return true
	}
	if matched == "" {
		return true
	}

	idx := strings.LastIndex(line, matched)
	if idx < 0 {
		idx = 0
	}
	rest := strings.TrimSpace(line[idx+len(matched):])

	if rest == "" {
		return true
	}

	if strings.HasPrefix(rest, ":") || strings.HasPrefix(rest, "[") {
		if rest == ":all" || strings.HasPrefix(rest, ":all,") {
			return true
		}
		return false
	}

	if matched == "# noqa" {

		return !regexp.MustCompile(`^[A-Z0-9_:,]+$`).MatchString(rest)
	}

	if strings.Contains(matched, "nolint") {

		if rest == ":all" || strings.HasPrefix(rest, ":all,") || rest == "all" {
			return true
		}
		return !strings.HasPrefix(rest, ":")
	}

	if strings.Contains(matched, "type:") && strings.Contains(matched, "ignore") {
		return true
	}

	return false
}

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

		if checkLinePrefix {
			trimmed := strings.TrimSpace(ln)

			if !strings.HasPrefix(trimmed, "//") &&
				!strings.HasPrefix(trimmed, "#") &&
				!strings.HasPrefix(trimmed, "--") &&
				!strings.Contains(trimmed, "/*") &&
				!strings.Contains(trimmed, "*") {

				continue
			}
		}

		matched := re.FindString(ln)
		if !isBareSuppression(ln, matched, requiresRule) {

			continue
		}

		if oldLineSet == nil || !oldLineSet[strings.TrimSpace(ln)] {
			result = append(result, idx+1)
		}
	}
	return result
}
