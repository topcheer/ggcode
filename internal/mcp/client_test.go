package mcp

import (
	"context"
	"encoding/json"
	"testing"
)

// mockServerProcess simulates an MCP server for testing.
// It writes a response for each request on stdout and reads from stdin.
// We test client by verifying it can parse responses and build requests.
func TestClientRequestBuild(t *testing.T) {
	c := NewClient("test", "echo", nil)
	id1 := c.nextRequestID()
	id2 := c.nextRequestID()
	if id1 == nil || id2 == nil {
		t.Fatal("expected non-nil IDs")
	}
	// IDs should be different
	b1, _ := json.Marshal(id1)
	b2, _ := json.Marshal(id2)
	if string(b1) == string(b2) {
		t.Error("expected different IDs")
	}
}

func TestToolDefinitionJSON(t *testing.T) {
	td := ToolDefinition{
		Name:        "read_file",
		Description: "Read a file",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`),
	}
	data, err := json.Marshal(td)
	if err != nil {
		t.Fatal(err)
	}
	var td2 ToolDefinition
	if err := json.Unmarshal(data, &td2); err != nil {
		t.Fatal(err)
	}
	if td2.Name != "read_file" {
		t.Errorf("name = %q", td2.Name)
	}
	if td2.Description != "Read a file" {
		t.Errorf("description = %q", td2.Description)
	}
}

func TestAdapterToolNames(t *testing.T) {
	tools := []ToolDefinition{
		{Name: "read_file", Description: "Read a file", InputSchema: json.RawMessage(`{}`)},
		{Name: "write_file", Description: "Write a file", InputSchema: json.RawMessage(`{}`)},
	}
	a := NewAdapter("filesystem", "echo", nil, tools)
	names := a.ToolNames()
	if len(names) != 2 {
		t.Fatalf("len = %d, want 2", len(names))
	}
	if names[0] != "mcp__filesystem__read_file" {
		t.Errorf("name[0] = %q", names[0])
	}
	if names[1] != "mcp__filesystem__write_file" {
		t.Errorf("name[1] = %q", names[1])
	}
	if a.ServerName() != "filesystem" {
		t.Errorf("server name = %q", a.ServerName())
	}
	if a.ToolCount() != 2 {
		t.Errorf("tool count = %d", a.ToolCount())
	}
}

func TestMCPToolParameters(t *testing.T) {
	mt := &mcpTool{
		name:     "mcp__fs__read",
		schema:   json.RawMessage(`{"type":"object","properties":{"p":{"type":"string"}}}`),
	}
	params := mt.Parameters()
	var m map[string]interface{}
	if err := json.Unmarshal(params, &m); err != nil {
		t.Fatal(err)
	}
	if m["type"] != "object" {
		t.Errorf("type = %v", m["type"])
	}
}

func TestMCPToolParametersDefault(t *testing.T) {
	mt := &mcpTool{
		name:   "mcp__fs__read",
		schema: nil,
	}
	params := mt.Parameters()
	var m map[string]interface{}
	if err := json.Unmarshal(params, &m); err != nil {
		t.Fatal(err)
	}
	if m["type"] != "object" {
		t.Errorf("expected default schema with type=object")
	}
}

func TestCallToolResultFields(t *testing.T) {
	r := &CallToolResult{
		Content: []ToolContent{
			{Type: "text", Text: "hello"},
		},
		IsError: false,
	}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var r2 CallToolResult
	if err := json.Unmarshal(data, &r2); err != nil {
		t.Fatal(err)
	}
	if len(r2.Content) != 1 || r2.Content[0].Text != "hello" {
		t.Error("round-trip failed")
	}
}

func TestInitializeParams(t *testing.T) {
	p := InitializeParams{
		ProtocolVersion: "2024-11-05",
		ClientInfo:      Implementation{Name: "ggcode", Version: "0.1.0"},
	}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]interface{}
	json.Unmarshal(data, &m)
	if m["protocolVersion"] != "2024-11-05" {
		t.Errorf("protocolVersion = %v", m["protocolVersion"])
	}
}

func TestClientCloseIdempotent(t *testing.T) {
	c := NewClient("test", "echo", nil)
	// Close without start should not panic
	if err := c.Close(); err != nil {
		t.Error("first close:", err)
	}
	if err := c.Close(); err != nil {
		t.Error("second close:", err)
	}
}

func TestClientName(t *testing.T) {
	c := NewClient("myserver", "cmd", nil)
	if c.Name() != "myserver" {
		t.Errorf("Name() = %q", c.Name())
	}
}

func TestAdapterRegisterTools(t *testing.T) {
	tools := []ToolDefinition{
		{Name: "tool1", Description: "desc1", InputSchema: json.RawMessage(`{"type":"object"}`)},
	}
	a := NewAdapter("srv", "echo", nil, tools)

	// We can't easily test without a real registry import in this package,
	// so we test the tool names generation
	names := a.ToolNames()
	if len(names) != 1 || names[0] != "mcp__srv__tool1" {
		t.Errorf("unexpected names: %v", names)
	}
}

// Test MCP tool Execute with invalid JSON input
func TestMCPToolExecuteInvalidJSON(t *testing.T) {
	mt := &mcpTool{
		name:       "mcp__srv__tool",
		serverName: "srv",
		command:    "echo",
		args:       nil,
		toolName:   "tool",
	}
	// Execute with invalid JSON — should fail at process spawn since "echo" isn't a real MCP server
	// but the JSON parse happens before that
	ctx := context.Background()
	_, err := mt.Execute(ctx, json.RawMessage(`{invalid`))
	// The error could be from JSON parse or from process spawn
	// Either way it should return an error
	if err == nil {
		t.Error("expected error for invalid JSON input")
	}
}
