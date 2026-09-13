package mcp

import (
	"context"
	"testing"
)

// #1644 case 6: a spec-legal 2025-06-18 server returning ONLY
// structuredContent (no content blocks) must surface the structured
// payload instead of an empty success.
type structuredOnlyCaller struct{}

func (structuredOnlyCaller) CallTool(ctx context.Context, name string, args map[string]interface{}) (*CallToolResult, error) {
	return &CallToolResult{
		Content:           nil, // zero content blocks
		StructuredContent: []byte(`{"rows":3,"total":42}`),
	}, nil
}

func TestIssue1644StructuredOnlyServerSurfacesPayload(t *testing.T) {
	tool := &mcpTool{name: "query", caller: structuredOnlyCaller{}, toolName: "query", srvName: "srv"}
	res, err := tool.Execute(context.Background(), []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("structured-only result must not be an error: %s", res.Content)
	}
	if res.Content == "" {
		t.Fatal("structured-only server produced an empty success - payload dropped")
	}
	if !contains(res.Content, `"total":42`) && !contains(res.Content, `"total": 42`) {
		t.Fatalf("structured payload not surfaced: %s", res.Content)
	}
}

// Regression: text blocks still win when present alongside structured data.
type mixedCaller struct{}

func (mixedCaller) CallTool(ctx context.Context, name string, args map[string]interface{}) (*CallToolResult, error) {
	return &CallToolResult{
		Content:           []ToolContent{{Type: "text", Text: "3 rows found"}},
		StructuredContent: []byte(`{"rows":3}`),
	}, nil
}

func TestIssue1644TextBlocksPreferredOverStructured(t *testing.T) {
	tool := &mcpTool{name: "query", caller: mixedCaller{}, toolName: "query", srvName: "srv"}
	res, err := tool.Execute(context.Background(), []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError || !contains(res.Content, "3 rows found") {
		t.Fatalf("text content must take precedence: %s", res.Content)
	}
	if contains(res.Content, `"rows"`) {
		t.Fatalf("structured payload must not be appended when text exists: %s", res.Content)
	}
}

// Regression: truly empty results (no blocks, no structured) stay empty -
// the placeholder logic must not fabricate content.
func TestIssue1644TrulyEmptyStillEmpty(t *testing.T) {
	tool := &mcpTool{name: "noop", caller: emptyCaller{}, toolName: "noop", srvName: "srv"}
	res, err := tool.Execute(context.Background(), []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.Content != "" || res.IsError {
		t.Fatalf("empty result shape changed: %q err=%v", res.Content, res.IsError)
	}
}

type emptyCaller struct{}

func (emptyCaller) CallTool(ctx context.Context, name string, args map[string]interface{}) (*CallToolResult, error) {
	return &CallToolResult{}, nil
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
