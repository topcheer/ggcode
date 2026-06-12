package tool

import (
	"context"
	"encoding/json"
	"testing"
)

type fakeMCPRuntime struct{}

func (f fakeMCPRuntime) SnapshotMCP() []MCPServerSnapshot {
	return []MCPServerSnapshot{{
		Name:          "web-reader",
		Connected:     true,
		ToolNames:     []string{"mcp__web-reader__fetch"},
		PromptNames:   []string{"summarize"},
		ResourceNames: []string{"docs"},
	}}
}

func (f fakeMCPRuntime) GetPrompt(ctx context.Context, server, name string, args map[string]interface{}) (*MCPPromptResult, error) {
	return &MCPPromptResult{
		Description: "Prompt description",
		Messages: []MCPPromptMessage{{
			Role: "user",
			Text: "Summarize this page",
		}},
	}, nil
}

func (f fakeMCPRuntime) ReadResource(ctx context.Context, server, uri string) (*MCPResourceResult, error) {
	return &MCPResourceResult{
		Contents: []MCPResourceContent{{
			URI:      uri,
			MIMEType: "text/plain",
			Text:     "hello from resource",
		}},
	}, nil
}

func TestMCPRuntimeToolDescriptions(t *testing.T) {
	promptTool := GetMCPPromptTool{Runtime: fakeMCPRuntime{}}
	if !containsAny(promptTool.Description(), "list_mcp_capabilities first") || !containsAny(promptTool.Description(), "does not execute") {
		t.Fatalf("get_mcp_prompt description should clarify discovery and non-execution, got %q", promptTool.Description())
	}

	resourceTool := ReadMCPResourceTool{Runtime: fakeMCPRuntime{}}
	if !containsAny(resourceTool.Description(), "list_mcp_capabilities first") || !containsAny(resourceTool.Description(), "does not summarize") {
		t.Fatalf("read_mcp_resource description should clarify discovery and raw output, got %q", resourceTool.Description())
	}
}

func TestListMCPCapabilitiesTool(t *testing.T) {
	tool := ListMCPCapabilitiesTool{Runtime: fakeMCPRuntime{}}
	result, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", result.Content)
	}
	if result.Content == "" {
		t.Fatal("expected MCP capability summary")
	}
}

func TestGetMCPPromptTool(t *testing.T) {
	tool := GetMCPPromptTool{Runtime: fakeMCPRuntime{}}
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"server":"web-reader","name":"summarize"}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || result.Content == "" {
		t.Fatalf("expected prompt content, got %+v", result)
	}
}

func TestReadMCPResourceTool(t *testing.T) {
	tool := ReadMCPResourceTool{Runtime: fakeMCPRuntime{}}
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"server":"web-reader","uri":"docs"}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || result.Content == "" {
		t.Fatalf("expected resource content, got %+v", result)
	}
}
