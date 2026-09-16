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

// MCP Tool Search: deferred disclosure of MCP tool schemas.
//
// Inspired by Anthropic's "Tool Search Tool" (Advanced Tool Use beta,
// advanced-tool-use-2025-11-20; docs.en/agents-and-tools/tool-use/tool-search-tool):
// instead of paying the context cost of every connected MCP server's tool
// schemas on every request, MCP tools (mcp__server__tool naming) are hidden
// behind a single meta-tool. The model searches on demand; matched schemas
// are returned in the tool result AND activated, so the next request carries
// core tools + the meta-tool + every activated schema. Activation is strictly
// monotonic: schemas are only ever ADDED mid-run, never removed — the failure
// mode that got dynamic tool pruning reverted (see the comment at the
// activeToolDefs site in agent.go) cannot recur.
//
// Built-in tools are never deferred: file edit/read/search/run stay fully
// available from the first turn, so the extra discovery round-trip only ever
// applies to optional MCP integrations. The feature activates automatically
// when the registry carries >= toolSearchThreshold MCP tools (smaller setups
// keep the zero-round-trip behavior) and can be disabled with
// GGCODE_TOOL_SEARCH=off.

const (
	// mcpToolPrefix is the registry naming convention for MCP-provided tools.
	mcpToolPrefix = "mcp__"

	// ToolSearchToolName is the synthetic meta-tool name. It is handled
	// agent-side (not registry-backed) so it never appears in
	// Registry.ToDefinitions and activation state stays per-agent.
	ToolSearchToolName = "tool_search"

	// toolSearchThreshold is the minimum number of MCP tools before schemas
	// are deferred. Below this the full list is sent (fewer round-trips beats
	// token savings on small registries).
	toolSearchThreshold = 20

	// toolSearchMaxResults caps schemas returned per search to bound the
	// tool_result size.
	toolSearchMaxResults = 10
)

// toolSearchState tracks deferred MCP tool schemas and their activation.
// Safe for concurrent use: parallel tool execution may activate tools while
// the main loop builds the next request's tool list.
type toolSearchState struct {
	mu        sync.Mutex
	enabled   bool
	deferred  map[string]provider.ToolDefinition // all MCP tool defs from the registry
	activated map[string]bool                    // schemas included in requests (monotonic within a conversation)
}

func newToolSearchState() *toolSearchState {
	return &toolSearchState{
		deferred:  make(map[string]provider.ToolDefinition),
		activated: make(map[string]bool),
	}
}

// init refreshes the deferred set from the current registry snapshot.
// Previously activated names survive re-init (a new run in the same
// conversation keeps history consistent with the schemas it references);
// names that disappeared from the registry are pruned.
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
	s.enabled = len(deferred) >= toolSearchThreshold && !toolSearchEnvDisabled()
}

func toolSearchEnvDisabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("GGCODE_TOOL_SEARCH"))) {
	case "0", "off", "false", "no":
		return true
	}
	return false
}

// activeDefs returns the tool definition list for the next request. With the
// feature disabled (or nil receiver) it returns the input unchanged. When
// enabled it returns core tools + the tool_search meta-tool + activated MCP
// schemas, in deterministic order.
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

// maybeAutoActivate promotes a deferred MCP tool whose schema was never sent
// but which the model is calling anyway (history carry-over or outside
// knowledge). Returns true when an activation happened.
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

// search finds deferred tools whose name+description contain every query
// token (case-insensitive). Empty query returns the deferred catalog head so
// the model can browse without guessing keywords. Matches are activated as a
// side effect. Already-activated tools are omitted (their schemas are already
// in the request).
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

// executeResult runs the tool_search meta-tool: parse arguments, search, and
// format matched schemas for the model.
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

// toolSearchDefinition builds the synthetic meta-tool definition.
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
		Name: ToolSearchToolName,
		Description: fmt.Sprintf(
			"Search and activate deferred MCP tool schemas. MCP server tools beyond the first %d are not loaded into context upfront to save tokens; this tool discovers them by keyword and returns their full schemas. After a match, call the tool directly by its name (e.g. mcp__github__create_issue) — no re-search needed. If you call an MCP tool you already know by name, that works too: it is activated automatically.",
			toolSearchThreshold,
		),
		Parameters: json.RawMessage(params),
	}
}
