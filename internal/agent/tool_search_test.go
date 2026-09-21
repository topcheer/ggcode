package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// fakeMCPTool is a minimal registry-backed MCP-named tool for wiring tests.
type fakeMCPTool struct {
	name string
}

func (f *fakeMCPTool) Name() string        { return f.name }
func (f *fakeMCPTool) Description() string { return "fake MCP tool for integration tests" }
func (f *fakeMCPTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}
func (f *fakeMCPTool) Execute(_ context.Context, _ json.RawMessage) (tool.Result, error) {
	return tool.Result{Content: "ok"}, nil
}

// newToolSearchTestAgent builds an Agent whose registry carries nMCP MCP tools
// and runs the run-start tool search init exactly as agent.go does.
func newToolSearchTestAgent(t *testing.T, nMCP int) (*Agent, *tool.Registry) {
	t.Helper()
	reg := tool.NewRegistry()
	for i := 0; i < nMCP; i++ {
		if err := reg.Register(&fakeMCPTool{name: fmt.Sprintf("mcp__srv__tool_%02d", i)}); err != nil {
			t.Fatalf("register fake tool: %v", err)
		}
	}
	a := NewAgent(&mockProvider{}, reg, "sys", 1)
	if a == nil {
		t.Fatal("NewAgent returned nil")
	}
	a.toolSearch.init(reg.ToDefinitions())
	return a, reg
}

// TestAgentExecuteToolDispatchesToolSearch covers the agent_tool.go dispatch:
// the meta-tool is handled agent-side without a registry lookup, and a
// successful search feeds activation state seen by the next request.
func TestAgentExecuteToolDispatchesToolSearch(t *testing.T) {
	// Deferred disclosure is opt-in only (default-off ruling); enable it
	// for the mechanism tests so they exercise the feature, not the default.
	t.Setenv("GGCODE_TOOL_SEARCH", "on")
	a, reg := newToolSearchTestAgent(t, toolSearchThreshold)
	if !a.toolSearch.enabled {
		t.Fatal("expected tool search enabled with threshold MCP tools")
	}
	res := a.executeTool(context.Background(), provider.ToolCallDelta{
		ID: "call-1", Name: ToolSearchToolName, Arguments: json.RawMessage(`{"query":"tool"}`),
	})
	if res.IsError {
		t.Fatalf("meta-tool dispatch failed: %s", res.Content)
	}
	if !strings.Contains(res.Content, "mcp__srv__tool_") {
		t.Fatalf("expected schemas in result, got: %s", res.Content)
	}
	got := a.toolSearch.activeDefs(reg.ToDefinitions())
	nActivated := 0
	for _, d := range got {
		if strings.HasPrefix(d.Name, mcpToolPrefix) {
			nActivated++
		}
	}
	if nActivated == 0 {
		t.Fatal("search via agent dispatch activated no schemas")
	}
}

// TestAgentExecuteToolAutoActivatesDeferredMCP covers the by-name fallback:
// calling a deferred MCP tool directly executes it AND promotes its schema so
// subsequent requests stay consistent with tools the conversation references.
func TestAgentExecuteToolAutoActivatesDeferredMCP(t *testing.T) {
	a, reg := newToolSearchTestAgent(t, toolSearchThreshold+2)
	target := "mcp__srv__tool_07"
	res := a.executeTool(context.Background(), provider.ToolCallDelta{
		ID: "call-2", Name: target, Arguments: json.RawMessage(`{}`),
	})
	if res.IsError || res.Content != "ok" {
		t.Fatalf("deferred tool execution failed: %+v", res)
	}
	found := false
	for _, d := range a.toolSearch.activeDefs(reg.ToDefinitions()) {
		if d.Name == target {
			found = true
		}
	}
	if !found {
		t.Fatalf("schema for %s was not auto-activated after by-name call", target)
	}
}

// TestAgentBelowThresholdSendsFullList verifies the send-site behavior end to
// end when tool search is disabled: every registered tool (no meta-tool) is
// included, so small setups keep zero-round-trip semantics.
func TestAgentBelowThresholdSendsFullList(t *testing.T) {
	a, reg := newToolSearchTestAgent(t, toolSearchThreshold-1)
	if a.toolSearch.enabled {
		t.Fatal("tool search must stay disabled below threshold")
	}
	defs := a.toolSearch.activeDefs(reg.ToDefinitions())
	if len(defs) != toolSearchThreshold-1 {
		t.Fatalf("expected %d defs, got %d", toolSearchThreshold-1, len(defs))
	}
	for _, d := range defs {
		if d.Name == ToolSearchToolName {
			t.Fatal("meta-tool must be absent when disabled")
		}
	}
}

func toolSearchTestDefs(nMCP int, nCore int) []provider.ToolDefinition {
	defs := make([]provider.ToolDefinition, 0, nMCP+nCore)
	for i := 0; i < nCore; i++ {
		defs = append(defs, provider.ToolDefinition{
			Name:        "core_" + strings.Repeat("x", i+1),
			Description: "built-in core tool",
			Parameters:  json.RawMessage(`{"type":"object"}`),
		})
	}
	servers := []string{"github", "railway", "slack"}
	for i := 0; i < nMCP; i++ {
		defs = append(defs, provider.ToolDefinition{
			Name:        "mcp__" + servers[i%len(servers)] + "__tool" + strings.Repeat("y", i+1),
			Description: "MCP tool for pull requests and deploys",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}}}`),
		})
	}
	return defs
}

func TestToolSearchBelowThresholdKeepsFullList(t *testing.T) {
	s := newToolSearchState()
	defs := toolSearchTestDefs(toolSearchThreshold-1, 3)
	s.init(defs)
	if s.enabled {
		t.Fatalf("expected disabled below threshold (%d MCP tools)", toolSearchThreshold-1)
	}
	got := s.activeDefs(defs)
	if len(got) != len(defs) {
		t.Fatalf("expected unchanged list, got %d of %d", len(got), len(defs))
	}
}

func TestToolSearchDefersAndActivates(t *testing.T) {
	// Deferred disclosure is opt-in only (default-off ruling); enable it
	// for the mechanism tests so they exercise the feature, not the default.
	t.Setenv("GGCODE_TOOL_SEARCH", "on")
	s := newToolSearchState()
	defs := toolSearchTestDefs(toolSearchThreshold+5, 3)
	s.init(defs)
	if !s.enabled {
		t.Fatal("expected enabled at/above threshold")
	}
	got := s.activeDefs(defs)
	// core + tool_search meta-tool only; MCP schemas deferred.
	if want := toolSearchThreshold + 5 - (toolSearchThreshold + 5) + 3 + 1; len(got) != want {
		t.Fatalf("expected %d defs (3 core + meta), got %d", want, len(got))
	}
	found := false
	for _, d := range got {
		if d.Name == ToolSearchToolName {
			found = true
		}
		if strings.HasPrefix(d.Name, mcpToolPrefix) {
			t.Fatalf("deferred MCP tool %s leaked into active list", d.Name)
		}
	}
	if !found {
		t.Fatal("tool_search meta-tool missing from active list")
	}

	res := s.executeResult(json.RawMessage(`{"query":"github pull"}`))
	if res.IsError {
		t.Fatalf("unexpected error result: %s", res.Content)
	}
	if !strings.Contains(res.Content, "mcp__github__") {
		t.Fatalf("expected github tools in result, got: %s", res.Content)
	}
	// Activated schemas now appear in the next request.
	got2 := s.activeDefs(defs)
	nActivated := 0
	for _, d := range got2 {
		if strings.HasPrefix(d.Name, mcpToolPrefix) {
			nActivated++
		}
	}
	if nActivated == 0 {
		t.Fatal("search did not activate any schema")
	}
	// Second search for the same scope returns nothing (already activated).
	res2 := s.executeResult(json.RawMessage(`{"query":"github pull"}`))
	if res2.IsError {
		t.Fatalf("unexpected error result: %s", res2.Content)
	}
	if !strings.Contains(res2.Content, "No deferred tools match") {
		t.Fatalf("expected no-match on re-search of activated tools, got: %s", res2.Content)
	}
}

func TestToolSearchAutoActivateByName(t *testing.T) {
	// Deferred disclosure is opt-in only (default-off ruling); enable it
	// for the mechanism tests so they exercise the feature, not the default.
	t.Setenv("GGCODE_TOOL_SEARCH", "on")
	s := newToolSearchState()
	defs := toolSearchTestDefs(toolSearchThreshold, 0)
	s.init(defs)
	real := ""
	for _, d := range defs {
		if strings.HasPrefix(d.Name, mcpToolPrefix) {
			real = d.Name
			break
		}
	}
	if real == "" {
		t.Fatal("no MCP fixtures")
	}
	if !s.maybeAutoActivate(real) {
		t.Fatalf("expected activation for %s", real)
	}
	if s.maybeAutoActivate(real) {
		t.Fatal("second activation should be a no-op")
	}
	if s.maybeAutoActivate("mcp__missing__nope") {
		t.Fatal("unknown tool must not activate")
	}
	if s.maybeAutoActivate("core_x") {
		t.Fatal("non-MCP tool must not activate")
	}
}

func TestToolSearchEnvDisabled(t *testing.T) {
	t.Setenv("GGCODE_TOOL_SEARCH", "off")
	s := newToolSearchState()
	defs := toolSearchTestDefs(toolSearchThreshold+10, 1)
	s.init(defs)
	if s.enabled {
		t.Fatal("GGCODE_TOOL_SEARCH=off must disable the feature")
	}
}

func TestToolSearchDeterministicOrder(t *testing.T) {
	s := newToolSearchState()
	defs := toolSearchTestDefs(toolSearchThreshold+8, 2)
	s.init(defs)
	s.executeResult(json.RawMessage(`{"query":"","limit":5}`))
	a := s.activeDefs(defs)
	b := s.activeDefs(defs)
	if len(a) != len(b) {
		t.Fatalf("unstable list length: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i].Name != b[i].Name {
			t.Fatalf("unstable order at %d: %s vs %s", i, a[i].Name, b[i].Name)
		}
	}
}

func TestToolSearchConcurrentActivation(t *testing.T) {
	s := newToolSearchState()
	defs := toolSearchTestDefs(toolSearchThreshold+12, 1)
	s.init(defs)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.search("tool", 3)
			s.activeDefs(defs)
			s.maybeAutoActivate("mcp__github__tooly")
		}()
	}
	wg.Wait()
	if got := s.activeDefs(defs); len(got) < 1+1 {
		t.Fatalf("unexpectedly small active list: %d", len(got))
	}
}

func TestToolSearchRequiresQuery(t *testing.T) {
	s := newToolSearchState()
	s.init(toolSearchTestDefs(toolSearchThreshold, 0))
	res := s.executeResult(json.RawMessage(`{}`))
	if !res.IsError {
		t.Fatal("empty arguments must produce an error result")
	}
}

// TestServerToolSearchHandoff verifies the server-side Tool Search Tool
// handoff (Anthropic advanced-tool-use beta): disable() yields the client
// meta-tool so activeDefs returns the full registry, and markServerDeferred
// flags exactly the MCP schemas for defer_loading while built-ins stay
// non-deferred (the API requires >=1 non-deferred tool).
func TestServerToolSearchHandoff(t *testing.T) {
	// Client-side deferred disclosure is opt-in only (default-off ruling
	// 2026-09-21); this handoff test still exercises the client->server
	// transition mechanics, so enable the client side explicitly.
	t.Setenv("GGCODE_TOOL_SEARCH", "on")
	a, reg := newToolSearchTestAgent(t, toolSearchThreshold)
	if !a.toolSearch.enabled {
		t.Fatal("expected client tool search enabled at threshold")
	}
	a.toolSearch.disable()
	a.serverToolSearch = true
	if a.toolSearch.enabled {
		t.Fatal("disable() must turn off the client meta-tool")
	}
	got := a.toolSearch.activeDefs(reg.ToDefinitions())
	if len(got) != toolSearchThreshold {
		t.Fatalf("disabled client search must return all defs, got %d/%d", len(got), toolSearchThreshold)
	}
	markServerDeferred(got)
	nDeferred := 0
	for _, d := range got {
		if strings.HasPrefix(d.Name, mcpToolPrefix) {
			if !d.DeferLoading {
				t.Fatalf("MCP tool %q must be deferred", d.Name)
			}
			nDeferred++
		} else if d.DeferLoading {
			t.Fatalf("built-in tool %q must never be deferred", d.Name)
		}
	}
	if nDeferred != toolSearchThreshold {
		t.Fatalf("expected %d deferred MCP schemas, got %d", toolSearchThreshold, nDeferred)
	}
}

// TestToolSearchDefaultOff pins the 2026-09-21 ruling: deferred MCP schema
// disclosure must NOT ship by default - each tool_search lookup is a full
// LLM round-trip, which costs far more than the upfront schemas it saves.
// A registry above the threshold with no env opt-in must send the full list.
func TestToolSearchDefaultOff(t *testing.T) {
	a, reg := newToolSearchTestAgent(t, toolSearchThreshold+5)
	if a.toolSearch.enabled {
		t.Fatal("deferred disclosure must be off by default (ruling 2026-09-21)")
	}
	all := reg.ToDefinitions()
	got := a.toolSearch.activeDefs(all)
	if len(got) != len(all) {
		t.Fatalf("default path must send the full list: got %d defs, want %d", len(got), len(all))
	}
}
