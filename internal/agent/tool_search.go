package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

const (
	mcpToolPrefix = "mcp__"

	ToolSearchToolName = "tool_search"

	toolSearchEnabledDefault = false

	toolSearchThreshold = 20

	toolSearchMaxResults = 10
)

type toolSearchState struct {
	mu        sync.Mutex
	enabled   bool
	deferred  map[string]provider.ToolDefinition
	activated map[string]bool
}

func newToolSearchState() *toolSearchState {
	return &toolSearchState{
		deferred:  make(map[string]provider.ToolDefinition),
		activated: make(map[string]bool),
	}
}

func (s *toolSearchState) init(defs []provider.ToolDefinition) {
	deferred := make(map[string]provider.ToolDefinition)
	for _, d := range defs {
		if strings.HasPrefix(d.Name, mcpToolPrefix) {
			deferred[d.Name] = d
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deferred = deferred
	for name := range s.activated {
		if _, ok := deferred[name]; !ok {
			delete(s.activated, name)
		}
	}

	s.enabled = (toolSearchEnabledDefault || toolSearchEnvEnabled()) &&
		len(deferred) >= toolSearchThreshold && !toolSearchEnvDisabled()
}

func (s *toolSearchState) disable() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.enabled = false
}

func markServerDeferred(defs []provider.ToolDefinition) {
	for i := range defs {
		if strings.HasPrefix(defs[i].Name, mcpToolPrefix) {
			defs[i].DeferLoading = true
		}
	}
}

func toolSearchEnvEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("GGCODE_TOOL_SEARCH"))) {
	case "1", "on", "true", "yes":
		return true
	}
	return false
}

func toolSearchEnvDisabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("GGCODE_TOOL_SEARCH"))) {
	case "0", "off", "false", "no":
		return true
	}
	return false
}

func (s *toolSearchState) activeDefs(all []provider.ToolDefinition) []provider.ToolDefinition {
	if s == nil || !s.enabled {
		return all
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]provider.ToolDefinition, 0, len(all)+1)
	for _, d := range all {
		if !strings.HasPrefix(d.Name, mcpToolPrefix) {
			out = append(out, d)
		}
	}
	out = append(out, toolSearchDefinition())
	activated := make([]string, 0, len(s.activated))
	for name := range s.activated {
		activated = append(activated, name)
	}
	sort.Strings(activated)
	for _, name := range activated {
		out = append(out, s.deferred[name])
	}
	return out
}

func (s *toolSearchState) maybeAutoActivate(name string) bool {
	if s == nil || !s.enabled || !strings.HasPrefix(name, mcpToolPrefix) {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.activated[name] {
		return false
	}
	if _, ok := s.deferred[name]; !ok {
		return false
	}
	s.activated[name] = true
	return true
}

func (s *toolSearchState) search(query string, limit int) []provider.ToolDefinition {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit <= 0 {
		limit = toolSearchMaxResults
	}
	if limit > toolSearchMaxResults {
		limit = toolSearchMaxResults
	}
	tokens := strings.Fields(strings.ToLower(query))
	names := make([]string, 0, len(s.deferred))
	for name, d := range s.deferred {
		if s.activated[name] {
			continue
		}
		hay := strings.ToLower(name + " " + d.Description)
		match := true
		for _, tok := range tokens {
			if !strings.Contains(hay, tok) {
				match = false
				break
			}
		}
		if match {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if len(names) > limit {
		names = names[:limit]
	}
	out := make([]provider.ToolDefinition, 0, len(names))
	for _, name := range names {
		out = append(out, s.deferred[name])
		s.activated[name] = true
	}
	return out
}

func (s *toolSearchState) executeResult(args json.RawMessage) tool.Result {
	if s == nil || !s.enabled {
		return tool.Result{Content: "tool_search is not active.", IsError: true}
	}
	var params struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &params); err != nil {
			return tool.Result{Content: fmt.Sprintf("invalid tool_search arguments: %v", err), IsError: true}
		}
	}
	if strings.TrimSpace(params.Query) == "" && params.Limit <= 0 {
		return tool.Result{Content: "tool_search requires a non-empty \"query\" (keywords to match against tool names and descriptions). Use limit<=0 only with a browse intent: {\"query\":\"\",\"limit\":10}.", IsError: true}
	}
	matches := s.search(params.Query, params.Limit)
	if len(matches) == 0 {
		s.mu.Lock()
		total := len(s.deferred) - len(s.activated)
		s.mu.Unlock()
		return tool.Result{Content: fmt.Sprintf("No deferred tools match %q. %d hidden tool(s) remain. Try broader keywords, or different server/tool name fragments (tools are named mcp__<server>__<tool>).", params.Query, total)}
	}
	type matchJSON struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
	}
	list := make([]matchJSON, 0, len(matches))
	names := make([]string, 0, len(matches))
	for _, m := range matches {
		list = append(list, matchJSON{Name: m.Name, Description: m.Description, Parameters: m.Parameters})
		names = append(names, m.Name)
	}
	body, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return tool.Result{Content: fmt.Sprintf("tool_search failed to format results: %v", err), IsError: true}
	}
	debug.Log("agent", "tool_search %q activated %d schemas: %v", params.Query, len(matches), names)
	return tool.Result{Content: fmt.Sprintf(
		"Activated %d tool(s). Their full schemas are below and will be included in subsequent requests — call them directly by name:\n\n%s\n\nUse Tool Effectiveness responsibly: prefer the narrowest matching tool; search again if none of these fit.",
		len(matches), body,
	)}
}

func toolSearchDefinition() provider.ToolDefinition {
	params := `{
  "type": "object",
  "properties": {
    "query": {
      "type": "string",
      "description": "Space-separated keywords matched against MCP tool names and descriptions (all keywords must match). E.g. 'github pull request' or 'railway deploy'."
    },
    "limit": {
      "type": "integer",
      "description": "Maximum number of tools to activate per search (1-10, default 10)."
    }
  },
  "required": ["query"]
}`
	return provider.ToolDefinition{
		Name:        ToolSearchToolName,
		Description: "Search and activate deferred MCP tool schemas. This mode is opt-in (large registries whose schemas do not fit upfront); keyword search returns full schemas, then call the tool directly by name (e.g. mcp__github__create_issue) with no re-search. Calling an MCP tool you already know by name also works: it is activated automatically.",
		Parameters:  json.RawMessage(params),
	}
}
