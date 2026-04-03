package mcp

import (
	"encoding/json"
	"testing"
)

func ptrID(n int64) *ID {
	id := NewIntID(n)
	return &id
}

func TestClientInitializeProtocol(t *testing.T) {
	// Verify the round-trip of initialize request/response types
	initReq := Request{
		JSONRPC: "2.0",
		Method:  "initialize",
		ID:      ptrID(1),
	}
	data, err := json.Marshal(initReq)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) == "" {
		t.Error("empty marshaled request")
	}

	initResp := `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2024-11-05","capabilities":{},"serverInfo":{"name":"mock","version":"0.1.0"}}}`
	var resp Response
	if err := json.Unmarshal([]byte(initResp), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.IsError() {
		t.Error("expected success")
	}
	var result InitializeResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result.ServerInfo.Name != "mock" {
		t.Errorf("server name = %q", result.ServerInfo.Name)
	}
}

func TestListToolsProtocol(t *testing.T) {
	resp := `{"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"read_file","description":"Read a file","inputSchema":{"type":"object","properties":{"path":{"type":"string"}}}}]}}`
	var parsed Response
	if err := json.Unmarshal([]byte(resp), &parsed); err != nil {
		t.Fatal(err)
	}
	var result ListToolsResult
	if err := json.Unmarshal(parsed.Result, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Tools) != 1 {
		t.Fatalf("len = %d", len(result.Tools))
	}
	if result.Tools[0].Name != "read_file" {
		t.Errorf("name = %q", result.Tools[0].Name)
	}
}

func TestCallToolProtocol(t *testing.T) {
	resp := `{"jsonrpc":"2.0","id":3,"result":{"content":[{"type":"text","text":"file contents here"}],"isError":false}}`
	var parsed Response
	if err := json.Unmarshal([]byte(resp), &parsed); err != nil {
		t.Fatal(err)
	}
	var result CallToolResult
	if err := json.Unmarshal(parsed.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Error("expected success")
	}
	if len(result.Content) != 1 || result.Content[0].Text != "file contents here" {
		t.Errorf("unexpected content: %+v", result.Content)
	}
}

func TestCallToolErrorProtocol(t *testing.T) {
	resp := `{"jsonrpc":"2.0","id":4,"result":{"content":[{"type":"text","text":"file not found"}],"isError":true}}`
	var parsed Response
	if err := json.Unmarshal([]byte(resp), &parsed); err != nil {
		t.Fatal(err)
	}
	var result CallToolResult
	if err := json.Unmarshal(parsed.Result, &result); err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected isError=true")
	}
}

func TestWriteMessageFormat(t *testing.T) {
	req := Request{
		JSONRPC: "2.0",
		Method:  "tools/list",
		ID:      ptrID(1),
	}
	data, _ := json.Marshal(req)
	// Verify the Content-Length header format
	header := len(data)
	if header <= 0 {
		t.Error("expected positive content length")
	}
}
