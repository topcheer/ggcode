package agent

import (
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

const (
	adaptiveEffortWindow = 6
)

type toolComplexity int

const (
	complexityLow toolComplexity = iota
	complexityMedium
	complexityHigh
)

var effortReadOnlyTools = map[string]bool{
	"read_file":       true,
	"read_file_range": true,
	"multi_file_read": true,
	"search_files":    true,
	"grep":            true,
	"glob":            true,
	"list_directory":  true,
	"list_dir":        true,
	"git_status":      true,
	"git_diff":        true,
	"git_log":         true,
	"git_show":        true,
	"git_blame":       true,
	"git_branch_list": true,
	"git_remote":      true,
	"git_stash_list":  true,
	"lsp_symbols":     true,
	"lsp_hover":       true,
	"lsp_references":  true,
	"lsp_definition":  true,
	"lsp_diagnostics": true,
	"code_search":     true,
	"code_health":     true,
	"web_search":      true,
	"web_fetch":       true,
	"runtime":         true,

	"task_list":                  true,
	"task_get":                   true,
	"debug_log":                  true,
	"lsp_workspace_symbols":      true,
	"lsp_implementation":         true,
	"lsp_code_actions":           true,
	"lsp_prepare_call_hierarchy": true,
	"lsp_incoming_calls":         true,
	"lsp_outgoing_calls":         true,
	"lsp_document_highlights":    true,

	"dep_graph":  true,
	"scan_todos": true,
	"screenshot": true,
}

var editTools = sourceMutatingTools

var errorRecoverySignals = sourceMutatingTools

var paramFormatFailureMarkers = []string{
	"not found",
	"no match",
	"old_text",
	"does not match",
	"failed to plan",
	"invalid input",
	"no edits",
	"nothing to",
}

func isParamFormatEditFailure(toolName, lowerErrText string) bool {
	if !errorRecoverySignals[toolName] || lowerErrText == "" {
		return false
	}
	for _, m := range paramFormatFailureMarkers {
		if strings.Contains(lowerErrText, m) {
			return true
		}
	}
	return false
}

type effortEntry struct {
	toolName string
	isError  bool
	errText  string
}

type adaptiveEffortState struct {
	mu              sync.Mutex
	entries         []effortEntry
	userOverrideSet bool
}

func newAdaptiveEffortState() *adaptiveEffortState {
	return &adaptiveEffortState{}
}

func (s *adaptiveEffortState) recordToolResult(toolName string, isError bool) {
	s.recordToolResultErr(toolName, isError, "")
}

func (s *adaptiveEffortState) recordToolResultErr(toolName string, isError bool, errText string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	et := ""
	if isError {
		et = strings.ToLower(errText)
		if len(et) > 160 {
			et = et[:160]
		}
	}
	s.entries = append(s.entries, effortEntry{toolName: toolName, isError: isError, errText: et})
	if len(s.entries) > adaptiveEffortWindow {
		s.entries = s.entries[len(s.entries)-adaptiveEffortWindow:]
	}
}

func newAdaptiveEffortStateDetectOverride(p provider.Provider) *adaptiveEffortState {
	s := newAdaptiveEffortState()
	if p == nil {
		return s
	}
	if ep, ok := p.(provider.ReasoningEffortProvider); ok {
		if strings.TrimSpace(ep.ReasoningEffort()) != "" {
			s.userOverrideSet = true
		}
	}
	return s
}

func (s *adaptiveEffortState) setUserOverride(set bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.userOverrideSet = set
}

func (s *adaptiveEffortState) hasUserOverride() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.userOverrideSet
}

func (s *adaptiveEffortState) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = s.entries[:0]
}

func (s *adaptiveEffortState) recommendedEffort() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.userOverrideSet {
		return ""
	}
	if len(s.entries) == 0 {
		return ""
	}

	recentErrors := 0
	editCount := 0
	readOnlyCount := 0

	for _, e := range s.entries {
		if e.isError {

			if errorRecoverySignals[e.toolName] && !isParamFormatEditFailure(e.toolName, e.errText) {
				recentErrors++
			}
			continue
		}
		if editTools[e.toolName] {
			editCount++
		} else if effortReadOnlyTools[e.toolName] {
			readOnlyCount++
		}
	}

	if recentErrors > 0 {
		return "high"
	}
	if editCount > 0 {
		return "medium"
	}

	if readOnlyCount == len(s.entries) {
		return "low"
	}
	return ""
}

func (a *Agent) applyAdaptiveEffort() (applied string, previous string) {
	if a.effortAdapter == nil {
		return "", ""
	}

	if a.effortAdapter.hasUserOverride() {
		return "", ""
	}

	recommended := a.effortAdapter.recommendedEffort()
	if recommended == "" {
		return "", ""
	}

	p, ok := a.provider.(interface {
		ReasoningEffort() string
	})
	if !ok {
		return "", ""
	}
	previous = p.ReasoningEffort()

	if recommended == previous {
		return "", previous
	}

	if a.setReasoningEffortInternal(recommended) {
		debug.Log("adaptive-effort", "adjusted effort: %s → %s", previous, recommended)
		return recommended, previous
	}
	return "", ""
}

func (a *Agent) restoreEffort(previous string) {

	if a.effortAdapter != nil && a.effortAdapter.hasUserOverride() {
		debug.Log("adaptive-effort", "skip restore: user override landed during the call")
		return
	}

	a.setReasoningEffortInternal(previous)
	debug.Log("adaptive-effort", "restored effort to: %q", previous)
}

func effortLevelDisplayName(effort string) string {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "low":
		return "Low (fast)"
	case "medium":
		return "Medium (balanced)"
	case "high":
		return "High (thorough)"
	default:
		return "Auto"
	}
}
