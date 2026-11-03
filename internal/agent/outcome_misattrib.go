package agent

import (
	"regexp"
	"strings"
)

const (
	outcomeMisattribMaxWarnings = 2
)

var outcomeFailureRe = regexp.MustCompile(
	`(?i)(?:error[s]?\s|fail(?:ed|ure|ures)?\b|panic:|fatal:|cannot |could not |not found\b|no results?\b|no matches?\b|no such file|undefined:|compilation aborted|BUILD FAILURE|exit code [1-9]|0 tests? pass|0 matches|traceback|exception|segfault|core dumped)`,
)

var outcomeZeroCountContextRe = regexp.MustCompile(`(?i)(?:0|zero)\s+$`)

func isZeroCountMatch(content string, start, end int) bool {
	tok := strings.ToLower(content[start:end])
	if !strings.Contains(tok, "fail") && !strings.Contains(tok, "error") {
		return false
	}
	if start == 0 {
		return false
	}
	prefix := content[:start]
	loc := outcomeZeroCountContextRe.FindStringIndex(prefix)
	if loc == nil {
		return false
	}

	if loc[0] > 0 && prefix[loc[0]-1] >= '0' && prefix[loc[0]-1] <= '9' {
		return false
	}
	return true
}

var outcomeSuccessClaimRe = regexp.MustCompile(
	`(?i)\b(?:done|fixed|resolved|solved|works?\s+(?:correctly|as\s+expected|now)|all\s+(?:set|good|passing|tests?\s+(?:pass|are\s+passing))|verified\s+(?:that\s+)?(?:it|this|everything)\s+works|successfully\s+(?:completed|implemented|fixed|updated)|everything\s+(?:looks?|is)\s+(?:good|correct|fine)|the\s+(?:fix|change|test|build)\s+(?:works?|passes?|is\s+correct)|no\s+(?:issues?|problems?|errors?))\b`,
)

var outcomeVerifiableTools = map[string]bool{
	"run_command":         true,
	"start_command":       true,
	"read_command_output": true,
	"git_commit":          true,
	"lsp_diagnostics":     true,
}

var outcomeCorrectiveTools = sourceMutatingTools

type outcomeMisattribState struct {
	warnings             int
	pendingFailureIter   int
	pendingFailureType   string
	correctiveActionSeen bool
}

func newOutcomeMisattribState() *outcomeMisattribState {
	return &outcomeMisattribState{
		pendingFailureIter: -1,
	}
}

func containsFailureIndicator(content string) (bool, string) {
	if len(content) < 5 {
		return false, ""
	}
	var hit string
	for _, m := range outcomeFailureRe.FindAllStringIndex(content, -1) {
		if isZeroCountMatch(content, m[0], m[1]) {
			continue
		}
		hit = content[m[0]:m[1]]
		break
	}
	if hit == "" {
		return false, ""
	}

	lower := strings.ToLower(hit)
	switch {
	case strings.Contains(lower, "test") || strings.Contains(lower, "fail"):
		return true, "test/build failure"
	case strings.Contains(lower, "error") || strings.Contains(lower, "panic") || strings.Contains(lower, "undefined"):
		return true, "error in output"
	case strings.Contains(lower, "not found") || strings.Contains(lower, "no result") || strings.Contains(lower, "no match") || strings.Contains(lower, "no such"):
		return true, "empty/not-found result"
	default:
		return true, "failure indicator"
	}
}

func (s *outcomeMisattribState) recordResult(toolName string, resultContent string, isError bool, iter int) {

	if isError {
		s.pendingFailureIter = iter
		s.pendingFailureType = "tool error"
		s.correctiveActionSeen = false
		return
	}

	if !outcomeVerifiableTools[toolName] {
		return
	}

	hasFail, failType := containsFailureIndicator(resultContent)
	if hasFail {
		s.pendingFailureIter = iter
		s.pendingFailureType = failType
		s.correctiveActionSeen = false
	}
}

func (s *outcomeMisattribState) recordToolCallForOM(toolName string) {
	if s.pendingFailureIter < 0 {
		return
	}
	if outcomeCorrectiveTools[toolName] {
		s.correctiveActionSeen = true
	}
}

func (s *outcomeMisattribState) checkMisattribution(assistantText string, iter int) string {
	if s.pendingFailureIter < 0 {
		return ""
	}
	if s.warnings >= outcomeMisattribMaxWarnings {
		return ""
	}

	if iter != s.pendingFailureIter+1 {

		if iter > s.pendingFailureIter+1 {
			s.pendingFailureIter = -1
		}
		return ""
	}

	if len(assistantText) < 5 {
		return ""
	}

	if s.correctiveActionSeen {

		s.pendingFailureIter = -1
		return ""
	}

	claims := outcomeSuccessClaimRe.FindAllString(assistantText, -1)
	if len(claims) == 0 {
		return ""
	}

	s.warnings++
	s.pendingFailureIter = -1

	claimExcerpt := claims[0]
	if len(claimExcerpt) > 50 {
		claimExcerpt = claimExcerpt[:50]
	}

	msg := "[outcome misattribution] Your text claims success (\"" + claimExcerpt + "\") but the preceding tool result contained " + s.pendingFailureType + ". Re-verify the actual outcome before proceeding - claiming success despite a failure result is a common source of silently broken work."
	return msg
}
