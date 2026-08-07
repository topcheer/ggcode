package agent

// Input Underspecification Detector
//
// Research basis:
//   - Ambig-SWE (arXiv 2502.13069, Feb 2025): "AI agents are increasingly
//     being deployed to automate tasks, often based on underspecified user
//     instructions. Making unwarranted assumptions to compensate for the
//     missing information and failing to ask clarifying questions can lead
//     to suboptimal outcomes, safety risks due to tool misuse, and wasted
//     computational resources."
//   - Proactive Clarification & Active Disambiguation (agentic-design.ai,
//     2025): Information Gain Reward framework -- agents should clarify
//     ambiguity before acting when the expected information gain outweighs
//     the cost of asking.
//   - SkillRL / Trajectory-Informed Memory (arXiv 2603.10600): agents
//     "often repeat inefficient patterns" because they start executing
//     on underspecified tasks instead of resolving ambiguity first.
//
// Problem: When a user gives a vague request like "fix the bug", "make it
// faster", "add tests", or "update the docs", the agent often dives into
// tool calls immediately without having enough specifics to be effective.
// It explores randomly, reads wrong files, and wastes iterations before
// discovering what the user actually meant. This is a leading cause of
// wasted context budget and analysis-paralysis trajectories.
//
// Detection heuristics (all deterministic, zero LLM cost):
//   1. Very short requests (<15 words) combined with action verbs but no
//      concrete identifiers (no file paths, function names, error text,
//      test names, commit hashes, etc.)
//   2. Vague magnitude words ("faster", "better", "cleaner", "more") without
//      measurable criteria or targets
//   3. Deictic references ("that bug", "this issue", "the problem") without
//      prior context anchoring (no error message, no test output, no file)
//
// Design:
//   - Fires ONLY on the first user turn of a run (not mid-conversation)
//   - Fires at most once per run (advisory, non-blocking)
//   - Zero LLM cost -- pure regex/length heuristics on user input text

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	// inputUnderspecMinWords: requests shorter than this are candidates.
	inputUnderspecMinWords = 15

	// inputUnderspecMaxWords: requests longer than this are detailed enough.
	inputUnderspecMaxWords = 5
)

// inputUnderspecState tracks whether we've already warned this run.
type inputUnderspecState struct {
	warned bool
}

func newInputUnderspecState() *inputUnderspecState {
	return &inputUnderspecState{}
}

func (s *inputUnderspecState) reset() {
	s.warned = false
}

// Concrete identifier patterns -- if ANY of these are present, the request
// is likely specific enough.
var inputIdentifierPatterns = []*regexp.Regexp{
	regexp.MustCompile(`[\w\-]+\.(go|js|ts|tsx|jsx|py|rs|java|rb|c|cpp|h|css|html|sql|yaml|yml|json|toml|md|sh)`), // file paths
	regexp.MustCompile(`\b0x[0-9a-fA-F]+\b`),                                       // memory addresses
	regexp.MustCompile(`\b[0-9a-f]{7,40}\b`),                                       // git commit hashes
	regexp.MustCompile("```"),                                                      // backtick code spans (simplified)
	regexp.MustCompile(`"[^"]{3,}"`),                                               // double-quoted strings
	regexp.MustCompile(`'[^']{3,}'`),                                               // single-quoted strings
	regexp.MustCompile(`\b(func|def|class|interface|struct|type|method|fn)\s+\w+`), // code declarations
	regexp.MustCompile(`\b[A-Z]\w+\.[a-z]\w+\(`),                                   // Method.Call() patterns
	regexp.MustCompile(`--?[a-z][-a-zA-Z]*`),                                       // CLI flags
	regexp.MustCompile(`/[a-z][-a-zA-Z0-9/]*`),                                     // URL paths / endpoint paths
	regexp.MustCompile(`:\d{2,5}\b`),                                               // port numbers / line refs
	regexp.MustCompile(`\b(line|L)\d+\b`),                                          // line numbers
}

// Vague magnitude / quality words without measurable criteria.
var inputVagueWords = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(faster|slower|better|cleaner|nicer|smoother|more|less)\b`),
	regexp.MustCompile(`(?i)\b(improve|optimize|enhance|refactor|streamline)\b`),
}

// Action verb patterns -- common in coding requests.
var inputActionVerbs = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(fix|add|update|change|remove|delete|create|implement|write|build|test|deploy|debug)\b`),
}

// Deictic / anaphoric references without anchored context.
var inputDeicticPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(this|that|the|it)\s+(bug|issue|problem|error|thing|file|function|feature|test)\b`),
	regexp.MustCompile(`(?i)\b(here|there)\b`),
}

// maybeWarnInputUnderspec checks if the user's initial request is too
// underspecified for effective action. Returns guidance message if so.
// Should only be called once, at the start of a run with the first user message.
func (a *Agent) maybeWarnInputUnderspec(userText string) string {
	if a.inputUnderspec == nil || a.inputUnderspec.warned {
		return ""
	}

	text := strings.TrimSpace(userText)
	if len(text) == 0 {
		return ""
	}

	// Word count as a quick complexity proxy.
	words := strings.Fields(text)
	wordCount := len(words)

	// If the request is long enough, it probably has detail.
	if wordCount > inputUnderspecMinWords {
		// Even for longer requests, check if they're entirely vague
		// (multiple vague words + deictics but zero identifiers).
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
		// Long but no identifiers -- check if it's vague throughout.
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
		if vagueCount < 2 && deicticCount < 3 {
			return ""
		}
		// Falls through to warning below.
	} else if wordCount > inputUnderspecMaxWords {
		// Medium-length request (6-15 words): check for identifiers.
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
		// Falls through to warning below.
	} else {
		// Very short (<=5 words): check if it has an action verb but no identifier.
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
		// Falls through to warning below.
	}

	a.inputUnderspec.warned = true

	// Build the warning.
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
