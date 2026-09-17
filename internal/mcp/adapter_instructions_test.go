package mcp

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/topcheer/ggcode/internal/tool"
)

// sa-44: the MCP initialize result's optional `instructions` field must be
// parsed (spec: 2025-03-26+) and surfaced by the adapter on tool
// descriptions.

func TestInitializeResultParsesInstructions(t *testing.T) {
	raw := `{
		"protocolVersion": "2025-06-18",
		"capabilities": {},
		"serverInfo": {"name": "demo", "version": "1.0"},
		"instructions": "Call list_items before get_item. IDs are stable."
	}`
	var res InitializeResult
	if err := json.Unmarshal([]byte(raw), &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if res.Instructions == "" {
		t.Fatal("instructions field not parsed from initialize result")
	}
	if !strings.Contains(res.Instructions, "list_items") {
		t.Fatalf("instructions content mismatch: %q", res.Instructions)
	}
}

func TestAdapterSurfacesServerInstructions(t *testing.T) {
	notes := "IDs are stable across sessions. Always call list_items first."
	adapter := NewAdapter("demo", nil, []ToolDefinition{
		{Name: "get_item", Description: "Get an item.", InputSchema: json.RawMessage(`{"type":"object"}`)},
	})
	adapter.SetServerInstructions(notes)
	registry := tool.NewRegistry()
	if err := adapter.RegisterTools(registry); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}
	tl, ok := registry.Get("mcp__demo__get_item")
	if !ok {
		t.Fatal("tool not registered")
	}
	desc := tl.Description()
	want := "\n\nServer usage notes: " + notes
	if !strings.Contains(desc, want) {
		t.Fatalf("description %q missing instructions suffix %q", desc, want)
	}
	if !utf8.ValidString(desc) {
		t.Fatal("description is not valid UTF-8")
	}
}

func TestAdapterInstructionsTruncatedAndRuneSafe(t *testing.T) {
	big := strings.Repeat("π", serverInstructionsMaxRunes+500)
	adapter := NewAdapter("demo", nil, []ToolDefinition{
		{Name: "op", Description: "Do an operation.", InputSchema: json.RawMessage(`{"type":"object"}`)},
	})
	adapter.SetServerInstructions(big)
	registry := tool.NewRegistry()
	if err := adapter.RegisterTools(registry); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}
	tl, _ := registry.Get("mcp__demo__op")
	desc := tl.Description()
	marker := "Server usage notes: "
	idx := strings.Index(desc, marker)
	if idx < 0 {
		t.Fatal("instructions not surfaced")
	}
	notes := desc[idx+len(marker):]
	if got := utf8.RuneCountInString(notes); got != serverInstructionsMaxRunes {
		t.Fatalf("notes length = %d runes, want %d (truncation must be rune-bounded)", got, serverInstructionsMaxRunes)
	}
	if !utf8.ValidString(notes) {
		t.Fatal("truncated notes broke UTF-8")
	}
}

func TestAdapterNoInstructionsKeepsDescUntouched(t *testing.T) {
	adapter := NewAdapter("demo", nil, []ToolDefinition{
		{Name: "op", Description: "Do an operation.", InputSchema: json.RawMessage(`{"type":"object"}`)},
	})
	registry := tool.NewRegistry()
	if err := adapter.RegisterTools(registry); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}
	tl, _ := registry.Get("mcp__demo__op")
	if got := tl.Description(); got != "Do an operation." {
		t.Fatalf("desc = %q, want untouched base description", got)
	}
}

func TestClientInstructionsAccessorDefaultsEmpty(t *testing.T) {
	c := &Client{name: "t"}
	if got := c.Instructions(); got != "" {
		t.Fatalf("Instructions() = %q, want empty before initialize", got)
	}
}
