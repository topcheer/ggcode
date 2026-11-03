package agent

import (
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

const (
	raMaxWarnings = 2
)

type raCategory int

const (
	raCatNone       raCategory = 0
	raCatUnderstand raCategory = 1
	raCatVerify     raCategory = 2
	raCatFix        raCategory = 3
	raCatSearch     raCategory = 4
	raCatDeploy     raCategory = 5
)

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

func raToolCategory(toolName string) raCategory {

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

const raAlignmentWindow = 3

var raCoordinationMarkers = []string{
	"first", "then", "next", "before", "after that",
	"afterwards", "step 1", "step 2",
}

type raPendingIntent struct {
	cat          raCategory
	phrase       string
	expiresAt    int
	conflictCat  raCategory
	conflictTool string
	exempt       bool
}

type raMismatch struct {
	statedCategory raCategory
	actionCategory raCategory
	intentPhrase   string
	toolName       string
}

type reasonActionState struct {
	mu         sync.Mutex
	steps      int
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

func raHasCoordinationMarker(lowerText string) bool {
	for _, m := range raCoordinationMarkers {
		if strings.Contains(lowerText, m) {
			return true
		}
	}
	return false
}

func raIsWordByte(b byte) bool {
	return b == '_' ||
		(b >= 'a' && b <= 'z') ||
		(b >= 'A' && b <= 'Z') ||
		(b >= '0' && b <= '9')
}

func raContainsWordBoundary(text, phrase string) bool {
	for i := 0; i+len(phrase) <= len(text); i++ {
		if text[i:i+len(phrase)] != phrase {
			continue
		}
		if i > 0 && raIsWordByte(text[i-1]) {
			continue
		}
		if end := i + len(phrase); end < len(text) && raIsWordByte(text[end]) {
			continue
		}
		return true
	}
	return false
}

func extractRAIntents(text string) []raCategory {
	lower := strings.ToLower(text)
	found := make(map[raCategory]bool)
	for cat, phrases := range raIntentPatterns {
		for _, p := range phrases {

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

func findRAIntentPhrase(text string, cat raCategory) string {
	lower := strings.ToLower(text)
	for _, p := range raIntentPatterns[cat] {
		if raContainsWordBoundary(lower, p) {
			return p
		}
	}
	return ""
}

func (s *reasonActionState) checkAlignment(assistantText string, toolCalls []toolCallInfo) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.warnings >= raMaxWarnings {
		return ""
	}

	s.steps++

	actionCats := raClassifyActions(toolCalls)
	statedCats := extractRAIntents(assistantText)

	s.resolveRAPending(actionCats)

	if hint := s.escalateExpiredPending(); hint != "" {
		return hint
	}

	if len(statedCats) == 0 {
		return ""
	}

	multiIntent := len(statedCats) >= 2 || raHasCoordinationMarker(strings.ToLower(assistantText))

	s.recordRAIntents(assistantText, statedCats, actionCats, multiIntent)
	return ""
}

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

func (s *reasonActionState) resolveRAPending(actionCats map[raCategory][]string) {
	kept := s.pending[:0]
	for _, p := range s.pending {
		if _, fulfilled := actionCats[p.cat]; fulfilled {
			continue
		}

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

type toolCallInfo struct {
	Name string
}

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
