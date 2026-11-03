package agent

import (
	"fmt"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	exploreFragWindow = 8

	exploreFragMinCalls = 6

	exploreFragMinUniqueTargets = 5

	exploreFragMaxWarnings = 2
)

var explorationFragToolNames = map[string]bool{
	"read_file":               true,
	"grep":                    true,
	"search_files":            true,
	"glob":                    true,
	"list_directory":          true,
	"code_search":             true,
	"lsp_symbols":             true,
	"lsp_hover":               true,
	"lsp_definition":          true,
	"lsp_references":          true,
	"lsp_workspace_symbols":   true,
	"lsp_implementation":      true,
	"lsp_document_highlights": true,
	"lsp_incoming_calls":      true,
	"lsp_outgoing_calls":      true,
	"multi_file_read":         true,
}

var mutatingToolNamesFrag = derivedEditTools(map[string]bool{
	"run_command":   true,
	"start_command": true,
	"git_commit":    true,
	"git_add":       true,
	"git_checkout":  true,
})

type exploreFragEntry struct {
	tool      string
	target    string
	iteration int
}

type exploreFragState struct {
	mu       sync.Mutex
	entries  []exploreFragEntry
	warnings int
}

func newExploreFragState() *exploreFragState {
	return &exploreFragState{
		entries: make([]exploreFragEntry, 0, exploreFragWindow+2),
	}
}

func (s *exploreFragState) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = s.entries[:0]

}

func extractExploreTarget(toolName string, args []byte) string {
	argsStr := string(args)
	if argsStr == "" {
		return ""
	}

	if val := extractExploreJSONField(argsStr, "path"); val != "" {
		return val
	}

	if val := extractExploreJSONField(argsStr, "file_path"); val != "" {
		return val
	}

	if val := extractExploreJSONField(argsStr, "directory"); val != "" {
		return val
	}

	if val := extractExploreJSONField(argsStr, "pattern"); val != "" {
		return "pattern:" + val
	}

	if val := extractExploreJSONField(argsStr, "query"); val != "" {
		return "query:" + val
	}

	if len(argsStr) > 60 {
		return argsStr[:60]
	}
	return argsStr
}

func extractExploreJSONField(jsonStr, field string) string {
	needle := "\"" + field + "\":\""
	idx := strings.Index(jsonStr, needle)
	if idx < 0 {

		needle = "\"" + field + "\": \""
		idx = strings.Index(jsonStr, needle)
		if idx < 0 {
			return ""
		}
	}
	start := idx + len(needle)
	end := strings.IndexByte(jsonStr[start:], '"')
	if end < 0 {
		return ""
	}
	return jsonStr[start : start+end]
}

func (s *exploreFragState) recordToolCall(toolName string, args []byte, iteration int) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if mutatingToolNamesFrag[toolName] {
		s.entries = s.entries[:0]
		return ""
	}

	if !explorationFragToolNames[toolName] {
		return ""
	}

	if s.warnings >= exploreFragMaxWarnings {
		return ""
	}

	target := extractExploreTarget(toolName, args)

	if p := normalizePath(target); p != "" {
		target = p
	}
	s.entries = append(s.entries, exploreFragEntry{
		tool:      toolName,
		target:    target,
		iteration: iteration,
	})

	if len(s.entries) > exploreFragWindow {
		s.entries = s.entries[len(s.entries)-exploreFragWindow:]
	}

	if len(s.entries) < exploreFragMinCalls {
		return ""
	}

	uniqueTargets := make(map[string]bool, len(s.entries))
	for _, e := range s.entries {
		if e.target != "" {
			uniqueTargets[e.target] = true
		}
	}

	if len(uniqueTargets) < exploreFragMinUniqueTargets {
		return ""
	}

	iterSet := make(map[int]bool, len(s.entries))
	for _, e := range s.entries {
		iterSet[e.iteration] = true
	}
	if len(iterSet) < 2 {
		return ""
	}

	s.warnings++

	callCount := len(s.entries)

	s.entries = nil
	debug.Log("agent", "Iteration %d: exploration fragmentation detected (%d calls, %d unique targets)",
		iteration, callCount, len(uniqueTargets))

	return fmt.Sprintf(
		"[Exploration Fragmentation] %d exploration tool calls across %d+ distinct targets "+
			"in the last %d iterations without any converging action (edit, write, command). "+
			"This scattered foraging pattern suggests the codebase mental model is incomplete. "+
			"Consider: (1) Use code_search or lsp_workspace_symbols for semantic discovery instead of many narrow reads. "+
			"(2) Read a key file fully (without offset/limit) to understand structure before exploring specifics. "+
			"(3) If you have enough context, start acting (edit/build) rather than exploring further.",
		callCount, len(uniqueTargets), exploreFragWindow)
}
