package agent

// User Premise Sycophancy Detector
//
// Research basis:
//   - "LLMs Know They're Wrong and Agree Anyway: The Shared..." (arXiv:2604.19117,
//     2026): across twelve open-weight models, the same attention heads carry a
//     "this statement is wrong" signal whether evaluating a claim independently or
//     under pressure to agree -- yet the model AGREES anyway. This proves
//     sycophancy is not a detection failure but an explicit compliance choice.
//   - "SycEval: Evaluating LLM Sycophancy" (arXiv:2502.08177, 2025): models
//     prioritize user agreement over independent reasoning, posing reliability
//     risks in professional settings.
//   - "Agent Sycophancy and Confirmation Bias: Defence Patterns for Codex CLI"
//     (2026): proposes anti-sycophancy hooks that intercept agent agreement with
//     user premises and force re-verification.
//
// Problem: When a user states a factual premise ("the bug is in auth.go",
// "this function returns an error", "the config uses YAML", "that API is
// deprecated"), a coding agent often AGREES and builds on that premise --
// without independently verifying it -- even when its internal knowledge or
// tool access would reveal the premise is wrong. This is sycophancy:
// deferring to the user's stated belief rather than grounding in evidence.
//
// This is a DISTINCT failure class from existing detectors:
//   - assumption_track.go: detects the agent's OWN unverified guesses ("I assume")
//   - false_premise.go: detects agent claiming success contradicting tool ERRORS
//   - selective_evidence.go: detects cherry-picking evidence to confirm hypothesis
//   - contradiction_track.go: detects self-contradiction across turns
//   - THIS detector: detects the agent AGREEING with a USER-STATED premise
//     without verification -- the only detector that cross-references user input.
//
// Detection approach (zero LLM cost):
//   1. When a user message arrives, extract candidate "premise statements" --
//      declarative assertions about code/library/project facts (not questions
//      or commands).
//   2. Track whether the agent used a verification tool (read_file, grep,
//      run_command, lsp_*, etc.) BEFORE responding to that premise.
//   3. In the assistant response, detect agreement/affirmation language
//      ("you're right", "correct", "exactly", "as you noted").
//   4. If the agent agreed with an unverified user premise, inject guidance to
//      independently verify before building on it.

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	// sycophancyMaxWarnings caps warnings per run to avoid nagging.
	sycophancyMaxWarnings = 2

	// sycophancyMaxPremises caps how many user premises we track at once.
	sycophancyMaxPremises = 8

	// sycophancyMaxExamples limits examples shown in a guidance message.
	sycophancyMaxExamples = 3
)

// userPremise is a candidate factual assertion extracted from a user message.
type userPremise struct {
	excerpt  string
	verified bool // true if a verification tool was used before agreement
	agreed   bool // true if the assistant affirmed this premise
	consumed bool // true once processed (avoid re-firing)
}

// sycophancyState tracks user premises and agreement patterns across a turn.
type sycophancyState struct {
	premises []userPremise
	warnings int
}

func newSycophancyState() *sycophancyState {
	return &sycophancyState{}
}

func (s *sycophancyState) reset() {
	s.premises = nil
	s.warnings = 0
}

// --- Premise extraction from user messages ---

// premisePattern identifies a declarative factual assertion in user text.
// We look for "X is Y", "X uses Y", "X returns Y", "the bug is", etc.
// We deliberately EXCLUDE questions (end with "?") and commands ("please",
// "can you", "do X").
var premisePatterns = []*regexp.Regexp{
	// "the X is/uses/returns/requires Y" -- factual state claims
	regexp.MustCompile(`(?i)\b(?:the|this|that)\s+\w[\w.\-]*\s+(?:is|are|uses?|returns?|requires?|contains?|calls?|extends?|implements?|depends\s+on)\s+\S`),
	// "X is deprecated/broken/wrong/missing" -- evaluative state claims
	regexp.MustCompile(`(?i)\b\w[\w.\-]*\s+is\s+(?:deprecated|broken|wrong|missing|outdated|obsolete|the\s+(?:bug|issue|cause|problem))`),
	// "we need to / should" requirement premises (forward-looking assertions)
	// Only catch short ones to avoid treating long commands as premises.
	regexp.MustCompile(`(?i)\bwe\s+(?:need|should|must)\s+(?:use|change|add|remove|update|replace)\s+\S`),
}

// premiseNegationRe excludes questions, commands, and requests.
var premiseNegationRe = regexp.MustCompile(`(?i)^\s*(?:please|can\s+you|could\s+you|would\s+you|do\s+you|are\s+you|is\s+there|what|why|how|when|where|who|which|let'?s|how\s+about|what\s+about)\b`)

// extractPremises scans user text for candidate factual assertions.
func extractPremises(userText string) []userPremise {
	if len(userText) == 0 {
		return nil
	}
	var out []userPremise
	seen := make(map[string]bool)

	for _, line := range strings.Split(userText, "\n") {
		excerpt := extractPremiseFromLine(line)
		if excerpt == "" {
			continue
		}
		key := strings.ToLower(excerpt)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, userPremise{excerpt: excerpt})
	}
	return out
}

// extractPremiseFromLine checks a single line for a candidate premise and
// returns a trimmed excerpt (possibly centered on the match), or "" if the
// line should be skipped.
func extractPremiseFromLine(rawLine string) string {
	line := strings.TrimSpace(rawLine)
	if len(line) < 15 || len(line) > 200 {
		return ""
	}
	if strings.HasSuffix(line, "?") {
		return ""
	}
	if premiseNegationRe.MatchString(line) {
		return ""
	}
	for _, p := range premisePatterns {
		loc := p.FindStringIndex(line)
		if loc == nil {
			continue
		}
		return centerExcerpt(line, loc)
	}
	return ""
}

// centerExcerpt returns the line if short, or a match-centered excerpt with
// ellipses if the line exceeds the max excerpt length.
func centerExcerpt(line string, loc []int) string {
	if len(line) <= 90 {
		return line
	}
	start := loc[0]
	if start > 30 {
		start -= 30
	}
	end := loc[1] + 40
	if end > len(line) {
		end = len(line)
	}
	if start < 0 {
		start = 0
	}
	excerpt := line[start:end]
	if start > 0 {
		excerpt = "..." + excerpt
	}
	if end < len(line) {
		excerpt = excerpt + "..."
	}
	return excerpt
}

// --- Verification tracking ---

// isPremiseVerificationTool reports whether a tool independently verifies a premise.
func isPremiseVerificationTool(name string) bool {
	switch name {
	case "read_file", "multi_file_read", "grep", "search_files", "glob",
		"code_search", "run_command", "list_directory",
		"lsp_definition", "lsp_references", "lsp_symbols", "lsp_workspace_symbols",
		"lsp_hover", "lsp_implementation", "lsp_diagnostics", "git_show", "git_blame":
		return true
	}
	return false
}

// markVerified flips the verified flag on all pending (un-agreed) premises.
func (s *sycophancyState) markVerified() {
	for i := range s.premises {
		if !s.premises[i].agreed {
			s.premises[i].verified = true
		}
	}
}

// --- Agreement detection in assistant text ---

var agreementPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\byou'?re\s+(?:right|correct|absolutely\s+right)\b`),
	regexp.MustCompile(`(?i)\bthat'?s\s+(?:correct|right|exactly|true)\b`),
	regexp.MustCompile(`(?i)\bexactly\b`),
	regexp.MustCompile(`(?i)\bcorrect[,.]?\s`),
	regexp.MustCompile(`(?i)\bas\s+you\s+(?:mentioned|noted|said|pointed\s+out|stated|observed)\b`),
	regexp.MustCompile(`(?i)\bgood\s+catch\b`),
	regexp.MustCompile(`(?i)\byou\s+pointed\s+out\b`),
	regexp.MustCompile(`(?i)\bspot\s+on\b`),
	regexp.MustCompile(`(?i)\bi\s+agree\b`),
	regexp.MustCompile(`(?i)\byes,?\s+(?:that'?s|you'?re)\b`),
	regexp.MustCompile(`(?i)\bi\s+(?:see|confirm)\s+(?:that|you)\b`),
}

// assistantAgrees reports whether the assistant text affirms a user premise.
func assistantAgrees(text string) bool {
	lowered := strings.ToLower(text)
	for _, p := range agreementPatterns {
		if p.MatchString(lowered) {
			return true
		}
	}
	return false
}

// --- Main detection ---

// captureUserPremises is called when a new user message is processed.
// It resets pending premises (already-consumed ones are dropped).
func (s *sycophancyState) captureUserPremises(userText string) {
	newPremises := extractPremises(userText)
	if len(newPremises) == 0 {
		// Keep room for verification of prior premises; trim consumed ones.
		s.dropConsumed()
		return
	}
	// Drop consumed premises, append new ones, cap the list.
	s.dropConsumed()
	s.premises = append(s.premises, newPremises...)
	if len(s.premises) > sycophancyMaxPremises {
		s.premises = s.premises[len(s.premises)-sycophancyMaxPremises:]
	}
}

func (s *sycophancyState) dropConsumed() {
	var keep []userPremise
	for _, p := range s.premises {
		if !p.consumed {
			keep = append(keep, p)
		}
	}
	s.premises = keep
}

// checkSycophancy scans assistant text for unverified agreement with user premises.
// Returns a guidance message, or "" if no warning is needed.
func (s *sycophancyState) checkSycophancy(assistantText string) string {
	if len(s.premises) == 0 {
		return ""
	}
	if s.warnings >= sycophancyMaxWarnings {
		return ""
	}
	if !assistantAgrees(assistantText) {
		return ""
	}

	var unverified []userPremise
	for i := range s.premises {
		p := &s.premises[i]
		if p.consumed || p.agreed {
			continue
		}
		// The assistant agreed with a premise that was NOT verified by a tool.
		p.agreed = true
		if !p.verified {
			unverified = append(unverified, *p)
			p.consumed = true
		}
	}

	if len(unverified) == 0 {
		return ""
	}

	s.warnings++

	var examples []string
	for _, p := range unverified {
		if len(examples) >= sycophancyMaxExamples {
			break
		}
		examples = append(examples, fmt.Sprintf("  - \"%s\"", p.excerpt))
	}

	return fmt.Sprintf(
		"[Sycophancy Guard] You agreed with %d user premise(s) without independent verification:\n%s\n"+
			"Research (arXiv:2604.19117) shows LLMs can internally detect a user's premise is wrong "+
			"yet still agree. Before building on a user-stated fact, verify it yourself with "+
			"read_file, grep, or a build command. If your own investigation contradicts the premise, "+
			"say so plainly rather than deferring.",
		len(unverified),
		strings.Join(examples, "\n"),
	)
}
