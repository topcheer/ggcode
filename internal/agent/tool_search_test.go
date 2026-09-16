package agent

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

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
