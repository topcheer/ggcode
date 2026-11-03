package agent

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	inputUnderspecMinWords = 15

	inputUnderspecMaxWords = 5
)

type inputUnderspecState struct {
	warned bool
}

func newInputUnderspecState() *inputUnderspecState {
	return &inputUnderspecState{}
}

func (s *inputUnderspecState) reset() {
	s.warned = false
}

var inputIdentifierPatterns = []*regexp.Regexp{
	regexp.MustCompile(`[\w\-]+\.(go|js|ts|tsx|jsx|py|rs|java|rb|c|cpp|h|css|html|sql|yaml|yml|json|toml|md|sh)`),
	regexp.MustCompile(`\b0x[0-9a-fA-F]+\b`),
	regexp.MustCompile(`\b[0-9a-f]{7,40}\b`),
	regexp.MustCompile("```"),
	regexp.MustCompile(`"[^"]{3,}"`),
	regexp.MustCompile(`'[^']{3,}'`),
	regexp.MustCompile(`\b(func|def|class|interface|struct|type|method|fn)\s+\w+`),
	regexp.MustCompile(`\b[A-Z]\w+\.[a-z]\w+\(`),
	regexp.MustCompile(`--?[a-z][-a-zA-Z]*`),
	regexp.MustCompile(`/[a-z][-a-zA-Z0-9/]*`),
	regexp.MustCompile(`:\d{2,5}\b`),
	regexp.MustCompile(`\b(line|L)\d+\b`),
}

var inputVagueWords = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(faster|slower|better|cleaner|nicer|smoother|more|less)\b`),
	regexp.MustCompile(`(?i)\b(improve|optimize|enhance|refactor|streamline)\b`),
}

var inputActionVerbs = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(fix|add|update|change|remove|delete|create|implement|write|build|test|deploy|debug)\b`),
}

var inputDeicticPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(this|that|the|it)\s+(bug|issue|problem|error|thing|file|function|feature|test)\b`),
	regexp.MustCompile(`(?i)\b(here|there)\b`),
}

func (a *Agent) maybeWarnInputUnderspec(userText string) string {
	if a.inputUnderspec == nil || a.inputUnderspec.warned {
		return ""
	}

	text := strings.TrimSpace(userText)
	if len(text) == 0 {
		return ""
	}

	words := strings.Fields(text)
	wordCount := len(words)

	vagueCount := 0
	for _, p := range inputVagueWords {
		if p.MatchString(text) {
			vagueCount++
		}
	}
	deicticCount := 0
	for _, p := range inputDeicticPatterns {
		matches := p.FindAllString(text, -1)
		deicticCount += len(matches)
	}
	if wordCount > inputUnderspecMinWords {

		hasIdentifier := false
		for _, p := range inputIdentifierPatterns {
			if p.MatchString(text) {
				hasIdentifier = true
				break
			}
		}
		if hasIdentifier {
			return ""
		}

		if vagueCount < 2 && deicticCount < 3 {
			return ""
		}

	} else if wordCount > inputUnderspecMaxWords {

		hasIdentifier := false
		for _, p := range inputIdentifierPatterns {
			if p.MatchString(text) {
				hasIdentifier = true
				break
			}
		}
		if hasIdentifier {
			return ""
		}

		if vagueCount == 0 && deicticCount == 0 {
			return ""
		}

	} else {

		hasIdentifier := false
		for _, p := range inputIdentifierPatterns {
			if p.MatchString(text) {
				hasIdentifier = true
				break
			}
		}
		if hasIdentifier {
			return ""
		}
		hasAction := false
		for _, p := range inputActionVerbs {
			if p.MatchString(text) {
				hasAction = true
				break
			}
		}
		if !hasAction {
			return ""
		}

	}

	a.inputUnderspec.warned = true

	wordCountStr := "short"
	if wordCount > inputUnderspecMaxWords {
		wordCountStr = fmt.Sprintf("%d-word", wordCount)
	}

	return fmt.Sprintf(
		"[INFO-input-underspec] The user's request appears underspecified "+
			"(%s request without concrete identifiers like file paths, function names, "+
			"error messages, or code references). "+
			"Before diving into tool calls, consider: (1) checking if prior context "+
			"anchors the request (error output, active file, diff), (2) if not, briefly "+
			"summarizing your understanding and asking for specifics. "+
			"Ambig-SWE research shows underspecified requests lead to wasted exploration "+
			"and incorrect assumptions. If you already have enough context from the "+
			"session, proceed normally -- this is advisory only.\n"+
			"Request: \"%s\"",
		wordCountStr,
		truncateInputForLog(text, 120),
	)
}

func truncateInputForLog(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
