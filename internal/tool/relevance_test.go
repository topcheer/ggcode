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

// Regression: '_' and '-' must be treated as token separators. Treating them
// as token characters collapsed "mcp__github__search_commits" into a single
// opaque token, killing the nameMatched signal for every MCP tool.
func TestRelevanceTokenize_SplitsUnderscoreAndHyphen(t *testing.T) {
	tokens := relevanceTokenize("mcp__github__search_commits")
	want := map[string]bool{"github": true, "search": true, "commits": true}
	for _, tok := range tokens {
		if want[tok] {
			delete(want, tok)
		}
	}
	if len(want) > 0 {
		t.Errorf("missing expected tokens %v, got %v", want, tokens)
	}
	for _, tok := range tokens {
		if strings.Contains(tok, "_") {
			t.Errorf("token %q still contains underscore", tok)
		}
	}
	tokens = relevanceTokenize("web-reader")
	if len(tokens) != 2 || tokens[0] != "web" || tokens[1] != "reader" {
		t.Errorf("hyphen should split tokens, got %v", tokens)
	}
}

// Regression: "mcp" is a stop word. Otherwise any user message mentioning
// "MCP" matches every MCP tool's name tokens and keeps them all alive.
func TestRelevanceTokenize_MCPIsStopWord(t *testing.T) {
	tokens := relevanceTokenize("ggcode mcp 误触发")
	for _, tok := range tokens {
		if tok == "mcp" {
			t.Errorf("token %q should be filtered as a stop word", tok)
		}
	}
}

// Regression: a casual message with one generic English word must not keep
// irrelevant MCP tools with large descriptions alive (the old 0.05 threshold
// let a single generic description word pass).
func TestRelevanceFilter_GenericWordDoesNotKeepIrrelevantMCP(t *testing.T) {
	defs := make([]provider.ToolDefinition, 0, 34)
	// 31 builtins: total tool count must exceed minToolsToActivate (30)
	// for filtering to engage.
	for i := 0; i < 31; i++ {
		defs = append(defs, makeToolDef("builtin_"+string(rune('a'+i)), "A builtin tool"))
	}
	defs = append(defs, makeToolDef(
		"mcp__weather__get_forecast",
		"Get the weather forecast for a city including temperature humidity wind and precipitation data",
	))
	defs = append(defs, makeToolDef(
		"mcp__calendar__create_event",
		"Create a calendar event with title start time end time and attendee list",
	))
	defs = append(defs, makeToolDef(
		"mcp__music__play_track",
		"Play a music track by artist name album or playlist",
	))

	f := NewRelevanceFilter()
	// Casual chat mentioning only generic API words.
	result := f.Filter(defs, "帮我解析一下这个 file 的 create 逻辑")

	names := make(map[string]bool, len(result))
	for _, d := range result {
		names[d.Name] = true
	}
	for _, name := range []string{"mcp__weather__get_forecast", "mcp__calendar__create_event", "mcp__music__play_track"} {
		if names[name] {
			t.Errorf("irrelevant tool %q should be pruned by generic-word-only context", name)
		}
	}
}

// Regression: name-token matches keep the RIGHT tool. With '_' now a
// separator, "github issue" in the context matches the name tokens of the
// github tool even though its description shares no words.
func TestRelevanceFilter_NameMatchKeepsRightServer(t *testing.T) {
	defs := make([]provider.ToolDefinition, 0, 34)
	// 30 builtins: total tool count must exceed minToolsToActivate (30)
	// for filtering to engage.
	for i := 0; i < 30; i++ {
		defs = append(defs, makeToolDef("builtin_"+string(rune('a'+i)), "A builtin tool"))
	}
	defs = append(defs, makeToolDef(
		"mcp__github__search_issues",
		"Search issues using natural-language semantic matching",
	))
	defs = append(defs, makeToolDef(
		"mcp__railway__list_services",
		"List all services in the deployment environment",
	))

	f := NewRelevanceFilter()
	result := f.Filter(defs, "find the github issue about login failure")

	names := make(map[string]bool, len(result))
	for _, d := range result {
		names[d.Name] = true
	}
	if !names["mcp__github__search_issues"] {
		t.Error("github tool should be kept via name-token match")
	}
	if names["mcp__railway__list_services"] {
		t.Error("railway tool is irrelevant to a github query and should be pruned")
	}
}
