package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/tool"
)

func boolPtr(b bool) *bool { return &b }

func okCaller() *mockCaller {
	return &mockCaller{result: &CallToolResult{Content: []ToolContent{{Type: "text", Text: "ok"}}}}
}

// The 2025-06-18 annotations object must survive tools/list JSON decoding.
func TestToolDefinitionAnnotationsJSON(t *testing.T) {
	var td ToolDefinition
	in := `{"name":"query","description":"q","inputSchema":{"type":"object"},
		"annotations":{"title":"Query","readOnlyHint":true,"idempotentHint":true,"openWorldHint":false}}`
	if err := json.Unmarshal([]byte(in), &td); err != nil {
		t.Fatal(err)
	}
	if td.Annotations == nil {
		t.Fatal("annotations not parsed")
	}
	if !td.DeclaredReadOnly() {
		t.Fatal("readOnlyHint=true should be detected")
	}
	if td.DeclaredDestructive() {
		t.Fatal("read-only-declared tool must not report destructive")
	}
	if td.ContradictoryHints() {
		t.Fatal("clean declarations must not be contradictory")
	}

	// Absent annotations: defaults (not read-only, destructive per spec).
	var bare ToolDefinition
	if err := json.Unmarshal([]byte(`{"name":"x","inputSchema":{}}`), &bare); err != nil {
		t.Fatal(err)
	}
	if bare.DeclaredReadOnly() {
		t.Fatal("absent readOnlyHint must not read-only")
	}
	if !bare.DeclaredDestructive() {
		t.Fatal("absent destructiveHint defaults to true per spec")
	}
}

// #998 follow-up: a read-only server whose tool carries an explicit
// readOnlyHint=true is allowed even though the name heuristic would block it.
func TestReadOnlyAdapter_AnnotationOverridesWriteName(t *testing.T) {
	tools := []ToolDefinition{
		{Name: "SET_CONFIG_CACHE", Annotations: &ToolAnnotations{ReadOnlyHint: boolPtr(true)}},
		{Name: "delete_file", Annotations: &ToolAnnotations{ReadOnlyHint: boolPtr(true)}}, // lying server: allowed by declaration
	}
	adapter := NewReadOnlyAdapter("srv", okCaller(), tools)
	registry := tool.NewRegistry()
	if err := adapter.RegisterTools(registry); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"mcp__srv__SET_CONFIG_CACHE", "mcp__srv__delete_file"} {
		tl, ok := registry.Get(n)
		if !ok {
			t.Fatalf("tool %q not registered", n)
		}
		res, err := tl.Execute(t.Context(), json.RawMessage(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		if res.IsError {
			t.Fatalf("annotated read-only tool %q unexpectedly blocked: %s", n, res.Content)
		}
	}
}

// Contradictory declarations (readOnlyHint=true + destructiveHint=true):
// the more dangerous hint wins; the name heuristic stays in force.
func TestReadOnlyAdapter_ContradictoryHintsStayBlocked(t *testing.T) {
	tools := []ToolDefinition{
		{Name: "delete_file", Annotations: &ToolAnnotations{ReadOnlyHint: boolPtr(true), DestructiveHint: boolPtr(true)}},
	}
	adapter := NewReadOnlyAdapter("srv", okCaller(), tools)
	registry := tool.NewRegistry()
	if err := adapter.RegisterTools(registry); err != nil {
		t.Fatal(err)
	}
	tl, ok := registry.Get("mcp__srv__delete_file")
	if !ok {
		t.Fatal("tool not registered")
	}
	res, _ := tl.Execute(t.Context(), json.RawMessage(`{}`))
	if !res.IsError || !strings.Contains(res.Content, "not allowed") {
		t.Fatalf("contradictory-hint write tool must stay blocked, got: %+v", res)
	}
}

// Without annotations, read-only blocking behaves exactly as before (#996/#998).
func TestReadOnlyAdapter_NoAnnotationsUnchanged(t *testing.T) {
	tools := []ToolDefinition{
		{Name: "write_file"},
		{Name: "get_dataset"},
	}
	adapter := NewReadOnlyAdapter("srv", okCaller(), tools)
	registry := tool.NewRegistry()
	if err := adapter.RegisterTools(registry); err != nil {
		t.Fatal(err)
	}
	if res, _ := mustTool(t, registry, "mcp__srv__write_file").Execute(t.Context(), json.RawMessage(`{}`)); !res.IsError {
		t.Fatal("unannotated write tool must stay blocked")
	}
	if res, _ := mustTool(t, registry, "mcp__srv__get_dataset").Execute(t.Context(), json.RawMessage(`{}`)); res.IsError {
		t.Fatalf("read tool must stay allowed: %+v", res)
	}
}

// On a NON read-only server, annotations must not change execution, but the
// model-facing description carries the server's read-only declaration.
func TestAdapter_AnnotationDescriptionOnly(t *testing.T) {
	tools := []ToolDefinition{
		{Name: "query", Description: "run a query", Annotations: &ToolAnnotations{ReadOnlyHint: boolPtr(true)}},
	}
	adapter := NewAdapter("srv", okCaller(), tools)
	registry := tool.NewRegistry()
	if err := adapter.RegisterTools(registry); err != nil {
		t.Fatal(err)
	}
	tl := mustTool(t, registry, "mcp__srv__query")
	if got := tl.Description(); !strings.Contains(got, "server declares read-only") {
		t.Fatalf("description missing annotation hint: %q", got)
	}
}

func mustTool(t *testing.T, registry *tool.Registry, name string) tool.Tool {
	t.Helper()
	tl, ok := registry.Get(name)
	if !ok {
		t.Fatalf("tool %q not registered", name)
	}
	return tl
}
