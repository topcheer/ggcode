package agent

import (
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

// Reasoning-Action Alignment Verifier
//
// Research basis:
//   - Metacognition-driven LLM frameworks (SAGE Journals 2025, emergentmind.com):
//     agents that self-monitor whether their verbalized reasoning matches their
//     actual actions show substantially improved reliability and transparency.
//     Key methodology: dual-prompt confidence probes and self-monitoring.
//   - "Do AI know what they know?" (journals.sagepub.com, 2025): metacognitive
//     alignment between stated intentions and executed actions is a core predictor
//     of agent success. Misalignment indicates the model is acting on autopilot.
//   - MetaCogAgent (arXiv:2605.17292): self-regulation requires matching the
//     cognitive category of the plan to the cognitive category of the action.
//
// Problem: AI coding agents frequently verbalize one cognitive intent category
// ("I need to understand the data flow", "Let me verify the fix works") but then
// execute tool calls from a mismatched action category (editing code when they
// said "understand", reading files when they said "verify"). This categorical
// misalignment is invisible to users and indicates degraded metacognitive control.
//
// This is DISTINCT from existing detectors:
//   - tool_target_mismatch.go: compares stated FILE PATH vs actual file path argument
//   - plan_abandon_detect.go: checks if declared plan steps were completed at all
//   - success_declare.go: checks if work continues after declaring completion
//   None of these compare the COGNITIVE CATEGORY of the reasoning to the
//   COGNITIVE CATEGORY of the action.
//
// Design:
//   - Scans assistant text for reasoning intent statements and classifies them
//     into categories: understand, verify, fix, search, deploy
//   - Classifies the actual tool calls in the same turn into the same categories
//   - When stated intent category mismatches action category, flags it
//   - Zero LLM cost - pure deterministic keyword/regex matching
//   - Fires at most 2 times per run (advisory, non-blocking)
//   - Resets each run

const (
	// raMaxWarnings: max alignment warnings per run.
	raMaxWarnings = 2
)

// raCategory represents a cognitive action category.
type raCategory int

const (
	raCatNone       raCategory = 0
	raCatUnderstand raCategory = 1 // reading/exploring code to comprehend
	raCatVerify     raCategory = 2 // running tests/builds/lint to confirm
	raCatFix        raCategory = 3 // modifying code to resolve issues
	raCatSearch     raCategory = 4 // searching for patterns/files/info
	raCatDeploy     raCategory = 5 // deploying, committing, pushing
)

// raIntentPatterns maps intent phrases found in reasoning text to categories.
var raIntentPatterns = map[raCategory][]string{
	raCatVerify: {
		"let me verify", "i'll verify", "i will verify",
		"let me test", "i'll test", "i will test",
		"let me check if", "i'll check if the build",
		"i'll check if tests", "let me run the tests",
		"i'll run the tests", "i will run tests",
		"let me run the build", "i'll run the build",
		"let me build", "i'll build",
		"i should verify", "need to verify", "time to verify",
		"let me confirm", "i'll confirm",
		"i should check that", "let me lint", "i'll lint",
	},
	raCatFix: {
		"let me fix", "i'll fix", "i will fix", "i need to fix",
		"let me update", "i'll update", "i will update",
		"let me edit", "i'll edit", "i will edit",
		"let me modify", "i'll modify", "i will modify",
		"let me change", "i'll change", "i should change",
		"let me correct", "i'll correct",
		"let me patch", "i'll patch",
		"let me implement", "i'll implement", "i will implement",
	},
	raCatUnderstand: {
		"let me understand", "i need to understand", "i want to understand",
		"let me see how", "let me look at how",
		"i'll examine", "i will examine",
		"let me read", "i'll read", "i will read",
		"let me check the structure", "let me look at the code",
		"let me explore", "i'll explore",
	},
	raCatSearch: {
		"let me search", "i'll search", "i will search",
		"let me find", "i'll find", "i will find",
		"let me look for", "i'll look for", "i need to find",
	},
}

// raToolCategory maps tool names to action categories.
func raToolCategory(toolName string) raCategory {
	// File-editing tools are classified via the canonical sourceMutatingTools
	// superset (#738) so the fix category can never drift from the registered
	// tool set.
	if sourceMutatingTools[toolName] {
		return raCatFix
	}
	switch toolName {
	case "run_command", "start_command":
		return raCatVerify
	case "read_file", "multi_file_read", "lsp_hover", "lsp_symbols",
		"lsp_definition", "lsp_references", "lsp_implementation",
		"lsp_document_highlights", "lsp_incoming_calls", "lsp_outgoing_calls",
		"lsp_prepare_call_hierarchy", "dep_graph", "code_health":
		return raCatUnderstand
	case "grep", "search_files", "glob", "code_search", "lsp_workspace_symbols":
		return raCatSearch
	case "git_commit", "git_push", "git_add", "ci_status":
		return raCatDeploy
	default:
		return raCatNone
	}
}

// raAlignmentWindow is the temporal tolerance (in tool-call turns) granted to
// a stated intent before it may escalate to a misalignment warning: an action
// mentioned in reasoning counts as aligned if it lands within this many
// subsequent turns (issue #1162).
const raAlignmentWindow = 3

// raCoordinationMarkers mark explicitly sequenced multi-step plans ("first X,
// then Y"). Text containing any of these, or stating 2+ distinct intent
// categories, gets the multi-intent exemption (issue #1162): such intents are
// fulfilled by executing ANY of the listed intentions across the window and
// are never escalated to a categorical-mismatch warning.
// ";" is deliberately absent (#1176): a bare semicolon is ordinary punctuation
// that appears inside code snippets, URLs, and plain prose far more often
// than in explicitly sequenced plans, so including it exempted nearly every
// turn that contained intent phrases and pushed the detector to systematic
// false negatives. Prose that truly sequences with a semicolon ("verify X;
// then commit") still matches "then".
var raCoordinationMarkers = []string{
	"first", "then", "next", "before", "after that",
	"afterwards", "step 1", "step 2",
}

// raPendingIntent tracks a stated intent whose category did not match any
// action category on the turn it was verbalized. It resolves silently when a
// matching action appears within raAlignmentWindow turns (#1162); otherwise it
// escalates to a warning unless the multi-intent exemption applies.
type raPendingIntent struct {
	cat          raCategory
	phrase       string
	expiresAt    int // turn number at which the tolerance window closes
	conflictCat  raCategory
	conflictTool string
	exempt       bool // multi-intent/sequential plan: expiry drops silently
}

// raMismatch describes a single alignment gap.
type raMismatch struct {
	statedCategory raCategory
	actionCategory raCategory
	intentPhrase   string
	toolName       string
}

// reasonActionState tracks alignment mismatches across the run.
type reasonActionState struct {
	mu         sync.Mutex
	steps      int // tool-call turns observed, drives the tolerance window (#1162)
	warnings   int
	mismatches []raMismatch
	pending    []raPendingIntent
}

func newReasonActionState() *reasonActionState {
	return &reasonActionState{}
}

func (s *reasonActionState) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.steps = 0
	s.warnings = 0
	s.mismatches = nil
	s.pending = nil
}

// raHasCoordinationMarker reports whether the text marks an explicitly
// sequenced multi-step plan (issue #1162 multi-intent exemption).
func raHasCoordinationMarker(lowerText string) bool {
	for _, m := range raCoordinationMarkers {
		if strings.Contains(lowerText, m) {
			return true
		}
	}
	return false
}

// raIsWordByte reports whether b is an ASCII word byte (letter, digit or
// underscore), matching Go regexp \b semantics (issue #1178).
func raIsWordByte(b byte) bool {
	return b == '_' ||
		(b >= 'a' && b <= 'z') ||
		(b >= 'A' && b <= 'Z') ||
		(b >= '0' && b <= '9')
}

// raContainsWordBoundary reports whether phrase occurs in text with word
// boundaries on both sides: the bytes immediately before and after the match
// must not be ASCII word bytes (issue #1178). Non-ASCII bytes (CJK prose,
// accented letters) count as boundaries, keeping matching lenient for
// multilingual text. Callers pass lower-cased text and phrases.
func raContainsWordBoundary(text, phrase string) bool {
	for i := 0; i+len(phrase) <= len(text); i++ {
		if text[i:i+len(phrase)] != phrase {
			continue
		}
		if i > 0 && raIsWordByte(text[i-1]) {
			continue // match starts mid-word
		}
		if end := i + len(phrase); end < len(text) && raIsWordByte(text[end]) {
			continue // match ends mid-word: "fix" inside "fixate"
		}
		return true
	}
	return false
}

// extractRAIntents scans assistant text for reasoning intent statements and
// returns the set of cognitive categories the agent verbalized.
func extractRAIntents(text string) []raCategory {
	lower := strings.ToLower(text)
	found := make(map[raCategory]bool)
	for cat, phrases := range raIntentPatterns {
		for _, p := range phrases {
			// Issue #1178: word-boundary match so "let me fix" cannot hit
			// "let me fixate" and fabricate a category.
			if raContainsWordBoundary(lower, p) {
				found[cat] = true
				break
			}
		}
	}
	if len(found) == 0 {
		return nil
	}
	result := make([]raCategory, 0, len(found))
	for c := range found {
		result = append(result, c)
	}
	return result
}

// findRAIntentPhrase returns the first matching intent phrase for a category.
// Uses word-boundary matching for consistency with extractRAIntents (#1178):
// a category found by extractRAIntents must always yield its phrase here.
func findRAIntentPhrase(text string, cat raCategory) string {
	lower := strings.ToLower(text)
	for _, p := range raIntentPatterns[cat] {
		if raContainsWordBoundary(lower, p) {
			return p
		}
	}
	return ""
}

// checkAlignment compares stated reasoning categories to the actual tool call
// categories. A stated intent whose category has no matching action this turn
// is NOT flagged immediately: it enters a tolerance window of
// raAlignmentWindow turns (issue #1162) during which a matching action from
// any later turn resolves it silently. Only an intent that stays unfulfilled
// for the whole window escalates to a warning, and intents belonging to an
// explicitly sequenced multi-intent plan are exempt from escalation entirely.
func (s *reasonActionState) checkAlignment(assistantText string, toolCalls []toolCallInfo) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.warnings >= raMaxWarnings {
		return ""
	}

	s.steps++

	actionCats := raClassifyActions(toolCalls)
	statedCats := extractRAIntents(assistantText)

	// Earlier-turn intents fulfilled by any matching action category on this
	// turn resolve silently (issue #1162, no immediate-turn requirement).
	s.resolveRAPending(actionCats)

	// Only strictly single intents left unfulfilled for the whole window are
	// reported; exempt ones drop silently (issue #1162).
	if hint := s.escalateExpiredPending(); hint != "" {
		return hint
	}

	// Issue #1177: a statement turn with no (categorized) tool calls must
	// still register its intents in the tolerance window. The previous
	// len(actionCats) == 0 guard dropped pure-text statement turns entirely,
	// so the intent was never pending and a categorically conflicting action
	// in a later turn could never escalate (structural cross-turn miss).
	// The statedCats check stays: recordRAIntents iterates statedCats.
	if len(statedCats) == 0 {
		return ""
	}

	// Multi-intent exemption (issue #1162): 2+ distinct stated categories, or
	// explicit sequencing markers in the text.
	multiIntent := len(statedCats) >= 2 || raHasCoordinationMarker(strings.ToLower(assistantText))

	s.recordRAIntents(assistantText, statedCats, actionCats, multiIntent)
	return ""
}

// raClassifyActions maps this turn's tool calls to their cognitive categories,
// keeping the tool names per category for concrete mismatch reports.
func raClassifyActions(toolCalls []toolCallInfo) map[raCategory][]string {
	actionCats := make(map[raCategory][]string)
	for _, tc := range toolCalls {
		cat := raToolCategory(tc.Name)
		if cat != raCatNone {
			actionCats[cat] = append(actionCats[cat], tc.Name)
		}
	}
	return actionCats
}

// resolveRAPending silently fulfills pending intents whose category matched an
// action this turn (issue #1162 temporal tolerance, no immediate-turn rule).
// Issue #1177: for intents still unfulfilled, this turn may be where a
// categorically conflicting action first materializes; record it on the
// pending so a later expiry can escalate. Cross-turn conflicts previously
// left conflictCat == raCatNone and were dropped silently at expiry.
// Caller must hold s.mu.
func (s *reasonActionState) resolveRAPending(actionCats map[raCategory][]string) {
	kept := s.pending[:0]
	for _, p := range s.pending {
		if _, fulfilled := actionCats[p.cat]; fulfilled {
			continue
		}
		// Issue #1177: capture a cross-turn conflict for an unfulfilled intent
		// that has no recorded conflict yet, so escalateExpiredPending can
		// produce a concrete warning when the tolerance window closes.
		if p.conflictCat == raCatNone {
			for actionCat, tools := range actionCats {
				if isCategoricalMismatch(p.cat, actionCat) {
					p.conflictCat = actionCat
					p.conflictTool = tools[0]
					break
				}
			}
		}
		kept = append(kept, p)
	}
	s.pending = kept
}

// escalateExpiredPending expires intents whose tolerance window closed. An
// exempt (multi-intent/sequential plan) intent or one without a recorded
// categorical conflict drops silently; a strictly single-intent statement that
// never materialized is reported. Returns the first escalated warning, if any.
// Caller must hold s.mu.
func (s *reasonActionState) escalateExpiredPending() string {
	for i := 0; i < len(s.pending); i++ {
		p := s.pending[i]
		if s.steps < p.expiresAt {
			continue
		}
		s.pending = append(s.pending[:i], s.pending[i+1:]...)
		i--
		if p.exempt || p.conflictCat == raCatNone {
			continue
		}
		mm := raMismatch{
			statedCategory: p.cat,
			actionCategory: p.conflictCat,
			intentPhrase:   p.phrase,
			toolName:       p.conflictTool,
		}
		s.mismatches = append(s.mismatches, mm)
		s.warnings++
		return formatRAWarning(mm)
	}
	return ""
}

// recordRAIntents stores newly stated intents that have no matching action
// into the tolerance window instead of firing immediately. Caller must hold
// s.mu.
func (s *reasonActionState) recordRAIntents(assistantText string, statedCats []raCategory, actionCats map[raCategory][]string, multiIntent bool) {
	for _, statedCat := range statedCats {
		if _, hasMatch := actionCats[statedCat]; hasMatch {
			continue
		}
		duplicate := false
		for _, p := range s.pending {
			if p.cat == statedCat {
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}
		pending := raPendingIntent{
			cat:       statedCat,
			phrase:    findRAIntentPhrase(assistantText, statedCat),
			expiresAt: s.steps + raAlignmentWindow,
			exempt:    multiIntent,
		}
		// Remember the first categorically-conflicting action observed while
		// the statement is outstanding so the eventual warning is concrete.
		for actionCat, tools := range actionCats {
			if isCategoricalMismatch(statedCat, actionCat) {
				pending.conflictCat = actionCat
				pending.conflictTool = tools[0]
				break
			}
		}
		s.pending = append(s.pending, pending)
	}
}

// isCategoricalMismatch returns true when the stated reasoning category and the
// actual action category represent a meaningful cognitive divergence.
func isCategoricalMismatch(stated, actual raCategory) bool {
	if stated == raCatVerify && actual == raCatFix {
		return true
	}
	if stated == raCatUnderstand && actual == raCatDeploy {
		return true
	}
	if stated == raCatUnderstand && actual == raCatFix {
		return true
	}
	if stated == raCatVerify && actual == raCatSearch {
		return true
	}
	return false
}

func raCategoryName(c raCategory) string {
	switch c {
	case raCatVerify:
		return "verify (run tests/build/lint)"
	case raCatFix:
		return "fix (edit/modify code)"
	case raCatUnderstand:
		return "understand (read/explore code)"
	case raCatSearch:
		return "search (find patterns/files)"
	case raCatDeploy:
		return "deploy (commit/push/deploy)"
	default:
		return "unknown"
	}
}

func formatRAWarning(mm raMismatch) string {
	var b strings.Builder
	b.WriteString("[Reasoning-Action Alignment] Your stated reasoning intent (\"")
	b.WriteString(mm.intentPhrase)
	b.WriteString("\") falls in the \"")
	b.WriteString(raCategoryName(mm.statedCategory))
	b.WriteString("\" category, but your actual tool call (")
	b.WriteString(mm.toolName)
	b.WriteString(") falls in the \"")
	b.WriteString(raCategoryName(mm.actionCategory))
	b.WriteString("\" category. This categorical misalignment suggests acting on ")
	b.WriteString("autopilot without genuine metacognitive engagement. ")
	b.WriteString("Either follow through on your stated intent, or update your ")
	b.WriteString("reasoning to match the action you are actually taking.")
	return b.String()
}

// toolCallInfo is a minimal representation of a tool call for alignment checking.
type toolCallInfo struct {
	Name string
}

// maybeWarnReasonAction is the entry point called from the agent loop.
func (a *Agent) maybeWarnReasonAction(assistantText string, toolCalls []provider.ToolCallDelta) string {
	if a.reasonAction == nil {
		return ""
	}
	infos := make([]toolCallInfo, 0, len(toolCalls))
	for _, tc := range toolCalls {
		infos = append(infos, toolCallInfo{Name: tc.Name})
	}
	hint := a.reasonAction.checkAlignment(assistantText, infos)
	if hint != "" {
		debug.Log("agent", "Reasoning-action alignment verifier detected categorical mismatch (mismatches=%d)",
			len(a.reasonAction.mismatches))
	}
	return hint
}
