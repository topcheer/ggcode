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
	switch toolName {
	case "run_command", "start_command":
		return raCatVerify
	case "edit_file", "write_file", "multi_edit_file", "multi_file_edit",
		"notebook_edit", "batch_replace":
		return raCatFix
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
	warnings   int
	mismatches []raMismatch
}

func newReasonActionState() *reasonActionState {
	return &reasonActionState{}
}

func (s *reasonActionState) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.warnings = 0
	s.mismatches = nil
}

// extractRAIntents scans assistant text for reasoning intent statements and
// returns the set of cognitive categories the agent verbalized.
func extractRAIntents(text string) []raCategory {
	lower := strings.ToLower(text)
	found := make(map[raCategory]bool)
	for cat, phrases := range raIntentPatterns {
		for _, p := range phrases {
			if strings.Contains(lower, p) {
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
func findRAIntentPhrase(text string, cat raCategory) string {
	lower := strings.ToLower(text)
	for _, p := range raIntentPatterns[cat] {
		if strings.Contains(lower, p) {
			return p
		}
	}
	return ""
}

// checkAlignment compares stated reasoning categories to the actual tool call
// categories. Returns a mismatch if the dominant stated intent has no matching
// action, AND the actual action falls in a significantly different category.
func (s *reasonActionState) checkAlignment(assistantText string, toolCalls []toolCallInfo) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.warnings >= raMaxWarnings {
		return ""
	}
	if len(toolCalls) == 0 {
		return ""
	}

	statedCats := extractRAIntents(assistantText)
	if len(statedCats) == 0 {
		return ""
	}

	actionCats := make(map[raCategory][]string)
	for _, tc := range toolCalls {
		cat := raToolCategory(tc.Name)
		if cat != raCatNone {
			actionCats[cat] = append(actionCats[cat], tc.Name)
		}
	}
	if len(actionCats) == 0 {
		return ""
	}

	for _, statedCat := range statedCats {
		if _, hasMatch := actionCats[statedCat]; hasMatch {
			continue
		}
		for actionCat, tools := range actionCats {
			if isCategoricalMismatch(statedCat, actionCat) {
				phrase := findRAIntentPhrase(assistantText, statedCat)
				mm := raMismatch{
					statedCategory: statedCat,
					actionCategory: actionCat,
					intentPhrase:   phrase,
					toolName:       tools[0],
				}
				s.mismatches = append(s.mismatches, mm)
				s.warnings++
				return formatRAWarning(mm)
			}
		}
	}
	return ""
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
