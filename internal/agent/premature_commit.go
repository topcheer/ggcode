package agent

import (
	"strconv"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	pcMinExplorationCalls = 3

	pcMinSearchCalls = 1
)

var pcExploratoryTools = map[string]bool{
	"read_file":               true,
	"multi_file_read":         true,
	"grep":                    true,
	"search_files":            true,
	"code_search":             true,
	"glob":                    true,
	"list_directory":          true,
	"lsp_symbols":             true,
	"lsp_workspace_symbols":   true,
	"lsp_definition":          true,
	"lsp_references":          true,
	"lsp_hover":               true,
	"lsp_implementation":      true,
	"lsp_document_highlights": true,
	"lsp_incoming_calls":      true,
	"lsp_outgoing_calls":      true,
	"lsp_call_hierarchy":      true,
}

var pcSearchTools = map[string]bool{
	"grep":                  true,
	"search_files":          true,
	"code_search":           true,
	"glob":                  true,
	"lsp_symbols":           true,
	"lsp_workspace_symbols": true,
	"lsp_references":        true,
	"lsp_implementation":    true,
	"lsp_incoming_calls":    true,
	"lsp_outgoing_calls":    true,
	"list_directory":        true,
}

type prematureCommitState struct {
	explorationCount int

	searchCount int

	filesRead map[string]bool

	warned bool

	firstEditDone bool
}

func newPrematureCommitState() *prematureCommitState {
	return &prematureCommitState{
		filesRead: make(map[string]bool),
	}
}

func (s *prematureCommitState) reset() {
	s.explorationCount = 0
	s.searchCount = 0
	s.filesRead = make(map[string]bool)
	s.warned = false
	s.firstEditDone = false
}

func (s *prematureCommitState) recordExploration(toolName string, fileHints []string) {
	if s.firstEditDone || s.warned {
		return
	}
	if pcExploratoryTools[toolName] {
		s.explorationCount++
	}
	if pcSearchTools[toolName] {
		s.searchCount++
	}
	if toolName == "read_file" || toolName == "multi_file_read" {
		for _, hint := range fileHints {
			if hint != "" {
				s.filesRead[normalizeFilePath(hint)] = true
			}
		}
	}
}

func (s *prematureCommitState) checkFirstEdit(editPaths []string) string {
	if s.firstEditDone || s.warned {
		return ""
	}
	s.firstEditDone = true

	if s.explorationCount >= pcMinExplorationCalls && s.searchCount >= pcMinSearchCalls {
		return ""
	}

	editSet := make(map[string]bool)
	for _, ep := range editPaths {
		editSet[normalizeFilePath(ep)] = true
	}
	countBeyond := 0
	for fr := range s.filesRead {
		if !editSet[fr] {
			countBeyond++
		}
	}

	if countBeyond >= 2 {
		return ""
	}

	s.warned = true

	debug.Log("agent", "premature-commit: first edit after %d exploration calls, %d search calls", s.explorationCount, s.searchCount)

	var reasons []string
	if s.explorationCount < pcMinExplorationCalls {
		reasons = append(reasons, strings.TrimSpace(strings.Repeat(" ", 0)+
			"only "+strconv.Itoa(s.explorationCount)+" exploratory action(s) before this edit"))
	}
	if s.searchCount < pcMinSearchCalls {
		reasons = append(reasons, "no repository-wide search performed (grep/code_search/lsp) to find related code, callers, or tests")
	}

	msg := "[premature-commitment] First edit attempted after insufficient evidence gathering (" +
		strings.Join(reasons, "; ") +
		"). ECLoop research (arXiv:2607.28815) shows that editing before examining callers, related implementations, and tests leads to incorrect patches in 20-27% of cases. " +
		"Consider: (1) search for callers/references of the symbols you're modifying, " +
		"(2) check existing tests related to this code, " +
		"(3) verify no related implementations or interfaces are affected before committing to this edit approach."

	return msg
}
