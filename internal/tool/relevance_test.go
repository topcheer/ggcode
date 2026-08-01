package tool

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func makeToolDef(name, desc string) provider.ToolDefinition {
	return provider.ToolDefinition{
		Name:        name,
		Description: desc,
		Parameters:  json.RawMessage(`{"type":"object"}`),
	}
}

func TestRelevanceFilter_BelowThreshold_NoFiltering(t *testing.T) {
	defs := []provider.ToolDefinition{
		makeToolDef("read_file", "Read a file"),
		makeToolDef("edit_file", "Edit a file"),
		makeToolDef("mcp__test__foo", "Do foo things"),
	}
	f := NewRelevanceFilter()
	result := f.Filter(defs, "irrelevant query")
	if len(result) != len(defs) {
		t.Errorf("expected %d tools (below activation threshold), got %d", len(defs), len(result))
	}
	if f.activated {
		t.Error("filter should not activate below threshold")
	}
}

func TestRelevanceFilter_KeepsBuiltinTools(t *testing.T) {
	defs := make([]provider.ToolDefinition, 0, 35)
	// 25 builtin tools
	for i := 0; i < 25; i++ {
		defs = append(defs, makeToolDef("builtin_"+string(rune('a'+i)), "A builtin tool"))
	}
	// 10 MCP tools
	for i := 0; i < 10; i++ {
		defs = append(defs, makeToolDef("mcp__srv__tool_"+string(rune('a'+i)), "An MCP tool"))
	}

	f := NewRelevanceFilter()
	result := f.Filter(defs, "write some code")

	// All 25 builtins should be present.
	if len(result) < 25 {
		t.Errorf("expected at least 25 builtin tools, got %d", len(result))
	}

	// Verify all builtins are kept.
	resultNames := make(map[string]bool)
	for _, d := range result {
		resultNames[d.Name] = true
	}
	for i := 0; i < 25; i++ {
		name := "builtin_" + string(rune('a'+i))
		if !resultNames[name] {
			t.Errorf("builtin tool %q was pruned", name)
		}
	}
}

func TestRelevanceFilter_PrunesIrrelevantMCPTools(t *testing.T) {
	defs := make([]provider.ToolDefinition, 0, 35)
	// 20 builtin tools
	for i := 0; i < 20; i++ {
		defs = append(defs, makeToolDef("builtin_"+string(rune('a'+i)), "A builtin tool"))
	}
	// 15 MCP tools from "railway" server - irrelevant to this query
	for i := 0; i < 15; i++ {
		defs = append(defs, makeToolDef(
			"mcp__railway__deploy_"+string(rune('a'+i)),
			"Deploy and manage railway services and deployments",
		))
	}

	f := NewRelevanceFilter()
	result := f.Filter(defs, "search for TODO comments in the codebase")

	// Should have pruned some railway tools since the query is about code search.
	if len(result) >= len(defs) {
		t.Errorf("expected some pruning, got %d/%d tools", len(result), len(defs))
	}
}

func TestRelevanceFilter_KeepsRelevantMCPTools(t *testing.T) {
	defs := make([]provider.ToolDefinition, 0, 35)
	// 20 builtin tools
	for i := 0; i < 20; i++ {
		defs = append(defs, makeToolDef("builtin_"+string(rune('a'+i)), "A builtin tool"))
	}
	// Relevant MCP tool
	defs = append(defs, makeToolDef(
		"mcp__railway__list_projects",
		"List all Railway projects accessible to the authenticated user",
	))
	// Irrelevant MCP tools
	for i := 0; i < 14; i++ {
		defs = append(defs, makeToolDef(
			"mcp__unrelated__tool_"+string(rune('a'+i)),
			"Some unrelated weather and calendar functionality",
		))
	}

	f := NewRelevanceFilter()
	result := f.Filter(defs, "deploy my project to railway")

	resultNames := make(map[string]bool)
	for _, d := range result {
		resultNames[d.Name] = true
	}

	if !resultNames["mcp__railway__list_projects"] {
		t.Error("relevant railway tool should be kept")
	}
}

func TestRelevanceFilter_ServerNameMatching(t *testing.T) {
	defs := make([]provider.ToolDefinition, 0, 35)
	// 20 builtin tools
	for i := 0; i < 20; i++ {
		defs = append(defs, makeToolDef("builtin_"+string(rune('a'+i)), "A builtin tool"))
	}
	// Cloudflare tools
	defs = append(defs, makeToolDef("mcp__cf__execute", "Execute Cloudflare API operations"))
	defs = append(defs, makeToolDef("mcp__cf__search", "Search Cloudflare docs"))
	// Unrelated tools
	for i := 0; i < 13; i++ {
		defs = append(defs, makeToolDef(
			"mcp__weather__tool_"+string(rune('a'+i)),
			"Weather forecast and climate data tools",
		))
	}

	f := NewRelevanceFilter()
	result := f.Filter(defs, "help me configure my cloudflare worker")

	resultNames := make(map[string]bool)
	for _, d := range result {
		resultNames[d.Name] = true
	}

	// At least one cf tool should be included.
	cfFound := false
	for _, d := range result {
		if strings.Contains(d.Name, "mcp__cf__") {
			cfFound = true
			break
		}
	}
	if !cfFound {
		t.Error("expected at least one cloudflare tool to be kept for cloudflare query")
	}
}

func TestRelevanceFilter_ContextFromMessages(t *testing.T) {
	msgs := []provider.Message{}
	// Build messages with text content.
	msgs = append(msgs, provider.Message{
		Role: "user",
		Content: []provider.ContentBlock{
			{Type: "text", Text: "deploy to railway"},
		},
	})
	msgs = append(msgs, provider.Message{
		Role: "assistant",
		Content: []provider.ContentBlock{
			{Type: "text", Text: "I'll help you deploy."},
		},
	})
	msgs = append(msgs, provider.Message{
		Role: "user",
		Content: []provider.ContentBlock{
			{Type: "text", Text: "which project?"},
		},
	})

	ctx := ExtractContextFromMessages(msgs, 6)
	if !strings.Contains(ctx, "deploy") || !strings.Contains(ctx, "railway") {
		t.Errorf("context should contain key terms, got: %s", ctx)
	}
}

func TestRelevanceTokenize_StopWords(t *testing.T) {
	tokens := relevanceTokenize("the quick brown fox jumps over the lazy dog")
	for _, tok := range tokens {
		switch tok {
		case "the", "over":
			t.Errorf("stop word %q should be filtered", tok)
		}
	}
	if len(tokens) != 6 { // quick, brown, fox, jumps, lazy, dog
		t.Errorf("expected 6 tokens, got %d: %v", len(tokens), tokens)
	}
}

func TestIsExtTool(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"read_file", false},
		{"edit_file", false},
		{"mcp__railway__deploy", true},
		{"mcp__cf__search", true},
		{"list_mcp_capabilities", false}, // builtin MCP capability tool
	}
	for _, tt := range tests {
		if got := isExtTool(tt.name); got != tt.want {
			t.Errorf("isExtTool(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestServerFromName(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"mcp__railway__list_projects", "railway"},
		{"mcp__cf__execute", "cf"},
		{"mcp__ggid__create_user", "ggid"},
	}
	for _, tt := range tests {
		if got := serverFromName(tt.name); got != tt.want {
			t.Errorf("serverFromName(%q) = %q, want %q", tt.name, got, tt.want)
		}
	}
}
