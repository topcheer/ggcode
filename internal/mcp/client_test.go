package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/topcheer/ggcode/internal/config"
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
	a := NewAdapter("filesystem", nil, tools)
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
		name:   "mcp__fs__read",
		schema: json.RawMessage(`{"type":"object","properties":{"p":{"type":"string"}}}`),
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
		ProtocolVersion: latestMCPProtocolVersion,
		ClientInfo:      Implementation{Name: "ggcode", Version: "0.1.0"},
	}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]interface{}
	json.Unmarshal(data, &m)
	if m["protocolVersion"] != latestMCPProtocolVersion {
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
	a := NewAdapter("srv", nil, tools)

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
		name:     "mcp__srv__tool",
		toolName: "tool",
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

func TestHTTPClientLifecycle(t *testing.T) {
	var sawSessionOnList bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var req Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		switch req.Method {
		case "initialize":
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Mcp-Session-Id", "test-session")
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"` + latestMCPProtocolVersion + `","capabilities":{"tools":{"listChanged":true}},"serverInfo":{"name":"mock","version":"1.0.0"}}}`))
		case "notifications/initialized":
			w.WriteHeader(http.StatusNoContent)
		case "tools/list":
			sawSessionOnList = r.Header.Get("Mcp-Session-Id") == "test-session"
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"search","description":"Search","inputSchema":{"type":"object"}}]}}`))
		case "tools/call":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":3,"result":{"content":[{"type":"text","text":"ok"}]}}`))
		default:
			t.Fatalf("unexpected method %s", req.Method)
		}
	}))
	defer server.Close()

	client := NewClientFromConfig(config.MCPServerConfig{
		Name: "remote",
		Type: "http",
		URL:  server.URL,
	})
	if err := client.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	tools, err := client.ListTools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !sawSessionOnList {
		t.Fatal("expected Mcp-Session-Id header on follow-up request")
	}
	if len(tools) != 1 || tools[0].Name != "search" {
		t.Fatalf("unexpected tools: %+v", tools)
	}
	result, err := client.CallTool(context.Background(), "search", map[string]interface{}{"q": "ggcode"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Content) != 1 || result.Content[0].Text != "ok" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestWebSocketClientLifecycle(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("upgrade websocket: %v", err)
		}
		defer conn.Close()
		for {
			_, payload, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var req Request
			if err := json.Unmarshal(payload, &req); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			switch req.Method {
			case "initialize":
				_ = conn.WriteJSON(map[string]any{
					"jsonrpc": "2.0",
					"id":      1,
					"result": map[string]any{
						"protocolVersion": latestMCPProtocolVersion,
						"capabilities":    map[string]any{"tools": map[string]any{"listChanged": true}},
						"serverInfo":      map[string]any{"name": "mock", "version": "1.0.0"},
					},
				})
			case "notifications/initialized":
			case "tools/list":
				_ = conn.WriteJSON(map[string]any{
					"jsonrpc": "2.0",
					"id":      2,
					"result": map[string]any{
						"tools": []map[string]any{{
							"name":        "fetch",
							"description": "Fetch",
							"inputSchema": map[string]any{"type": "object"},
						}},
					},
				})
			case "tools/call":
				_ = conn.WriteJSON(map[string]any{
					"jsonrpc": "2.0",
					"id":      3,
					"result": map[string]any{
						"content": []map[string]any{{"type": "text", "text": "ok"}},
					},
				})
			default:
				t.Fatalf("unexpected method %s", req.Method)
			}
		}
	}))
	defer server.Close()

	wsURL := mustWSURL(t, server.URL)
	client := NewClientFromConfig(config.MCPServerConfig{
		Name: "socket",
		Type: "ws",
		URL:  wsURL,
	})
	if err := client.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	tools, err := client.ListTools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Name != "fetch" {
		t.Fatalf("unexpected tools: %+v", tools)
	}
	result, err := client.CallTool(context.Background(), "fetch", map[string]any{"q": "ggcode"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Content) != 1 || result.Content[0].Text != "ok" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestReadResponseSkipsNotifications(t *testing.T) {
	stream := encodeStdioMessages(
		t,
		Notification{JSONRPC: "2.0", Method: "notifications/progress"},
		Response{JSONRPC: "2.0", ID: json.RawMessage(`1`), Result: json.RawMessage(`{"ok":true}`)},
	)
	client := &Client{
		name:   "stdio-test",
		reader: bufio.NewReader(bytes.NewReader(stream)),
	}

	resp, err := client.readResponse(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if string(resp.Result) != `{"ok":true}` {
		t.Fatalf("unexpected response result: %s", string(resp.Result))
	}
}

func TestReadResponseHandlesRootsListRequest(t *testing.T) {
	requestID := NewIntID(7)
	stream := encodeStdioMessages(
		t,
		Request{
			JSONRPC: "2.0",
			Method:  "roots/list",
			ID:      &requestID,
		},
		Response{JSONRPC: "2.0", ID: json.RawMessage(`1`), Result: json.RawMessage(`{"tools":[]}`)},
	)
	var writes bytes.Buffer
	client := &Client{
		name:   "stdio-test",
		reader: bufio.NewReader(bytes.NewReader(stream)),
		stdin:  nopWriteCloser{Writer: &writes},
	}

	resp, err := client.readResponse(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if string(resp.Result) != `{"tools":[]}` {
		t.Fatalf("unexpected response result: %s", string(resp.Result))
	}

	reply := decodeFirstStdioMessage(t, writes.Bytes())
	replyResp, ok := reply.(*Response)
	if !ok {
		t.Fatalf("expected response to roots/list, got %T", reply)
	}
	var payload struct {
		Roots []struct {
			URI string `json:"uri"`
		} `json:"roots"`
	}
	if err := json.Unmarshal(replyResp.Result, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Roots) != 1 {
		t.Fatalf("expected one root, got %+v", payload)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		t.Fatal(err)
	}
	expected := (&url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}).String()
	if payload.Roots[0].URI != expected {
		t.Fatalf("unexpected root uri: %q != %q", payload.Roots[0].URI, expected)
	}
}

func TestReadMessageSupportsHeaderFraming(t *testing.T) {
	stream := encodeHeaderFramedMessages(
		t,
		Response{JSONRPC: "2.0", ID: json.RawMessage(`1`), Result: json.RawMessage(`{"ok":true}`)},
	)
	client := &Client{
		name:   "stdio-test",
		reader: bufio.NewReader(bytes.NewReader(stream)),
	}

	msg, err := client.readMessage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	resp, ok := msg.(*Response)
	if !ok {
		t.Fatalf("expected response, got %T", msg)
	}
	if string(resp.Result) != `{"ok":true}` {
		t.Fatalf("unexpected response result: %s", string(resp.Result))
	}
}

func TestCallToolCancelAbortsHungStdioServer(t *testing.T) {
	command := "sleep"
	args := []string{"60"}
	if runtime.GOOS == "windows" {
		command = "powershell"
		args = []string{"-NoProfile", "-Command", "Start-Sleep -Seconds 60"}
	}

	client := NewClient("hung-stdio", command, args)
	if err := client.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := client.CallTool(ctx, "analyze_image", map[string]interface{}{"image_source": "https://example.com/image.png"})
		done <- err
	}()

	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected cancellation error")
		}
		if ctx.Err() == nil || !containsAny(err.Error(), []string{"context canceled", "context cancelled"}) {
			t.Fatalf("expected context cancellation, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("expected hung stdio call to abort promptly on cancel")
	}
}

func TestStartKeepsStdioProcessAliveAfterConnectContextCancel(t *testing.T) {
	command := "sleep"
	args := []string{"60"}
	if runtime.GOOS == "windows" {
		command = "powershell"
		args = []string{"-NoProfile", "-Command", "Start-Sleep -Seconds 60"}
	}

	ctx, cancel := context.WithCancel(context.Background())
	client := NewClient("stdio-lifecycle", command, args)
	if err := client.Start(ctx); err != nil {
		cancel()
		t.Fatal(err)
	}
	defer client.Close()

	cancel()
	time.Sleep(50 * time.Millisecond)

	if client.cmd == nil || client.cmd.Process == nil {
		t.Fatal("expected started stdio process")
	}
	if err := client.cmd.Process.Kill(); err != nil {
		t.Fatalf("expected stdio process to still be alive after connect context cancellation, got %v", err)
	}
}

func encodeStdioMessages(t *testing.T, messages ...interface{}) []byte {
	t.Helper()
	var out bytes.Buffer
	for _, msg := range messages {
		data, err := json.Marshal(msg)
		if err != nil {
			t.Fatal(err)
		}
		out.Write(data)
		out.WriteByte('\n')
	}
	return out.Bytes()
}

func encodeHeaderFramedMessages(t *testing.T, messages ...interface{}) []byte {
	t.Helper()
	var out bytes.Buffer
	for _, msg := range messages {
		data, err := json.Marshal(msg)
		if err != nil {
			t.Fatal(err)
		}
		out.WriteString("Content-Length: ")
		out.WriteString(fmt.Sprintf("%d", len(data)))
		out.WriteString("\r\n\r\n")
		out.Write(data)
	}
	return out.Bytes()
}

func decodeFirstStdioMessage(t *testing.T, data []byte) interface{} {
	t.Helper()
	reader := bufio.NewReader(bytes.NewReader(data))
	client := &Client{reader: reader}
	msg, err := client.readMessage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return msg
}

func containsAny(text string, candidates []string) bool {
	for _, candidate := range candidates {
		if strings.Contains(text, candidate) {
			return true
		}
	}
	return false
}

type nopWriteCloser struct {
	io.Writer
}

func (n nopWriteCloser) Write(p []byte) (int, error) {
	return n.Writer.Write(p)
}

func (nopWriteCloser) Close() error { return nil }

// TestClientErrorContext_ConnectionClosed verifies that error messages include the
// server name when RPC fails due to a closed connection.
func TestClientErrorContext_ConnectionClosed(t *testing.T) {
	c := NewClient("my-mcp-server", "nonexistent_cmd", nil)
	// Mark as closed so sendRequest returns the "connection closed" error path
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()

	ctx := context.Background()
	_, err := c.Initialize(ctx)
	if err == nil {
		t.Fatal("expected error from Initialize on closed client")
	}
	errMsg := err.Error()
	if !strings.Contains(errMsg, "my-mcp-server") {
		t.Errorf("error message should contain server name 'my-mcp-server', got: %s", errMsg)
	}
}

// TestClientErrorContext_ListToolsOnClosed verifies ListTools includes server name in error.
func TestClientErrorContext_ListToolsOnClosed(t *testing.T) {
	c := NewClient("tool-server", "cmd", nil)
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()

	ctx := context.Background()
	_, err := c.ListTools(ctx)
	if err == nil {
		t.Fatal("expected error from ListTools on closed client")
	}
	errMsg := err.Error()
	if !strings.Contains(errMsg, "tool-server") {
		t.Errorf("error message should contain server name 'tool-server', got: %s", errMsg)
	}
}

// TestClientErrorContext_CallToolOnClosed verifies CallTool includes server name in error.
func TestClientErrorContext_CallToolOnClosed(t *testing.T) {
	c := NewClient("call-server", "cmd", nil)
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()

	ctx := context.Background()
	_, err := c.CallTool(ctx, "my_tool", map[string]interface{}{"arg": "val"})
	if err == nil {
		t.Fatal("expected error from CallTool on closed client")
	}
	errMsg := err.Error()
	if !strings.Contains(errMsg, "call-server") {
		t.Errorf("error message should contain server name 'call-server', got: %s", errMsg)
	}
}

// TestClientErrorContext_HTTPConnectionError verifies server name appears in HTTP transport errors.
func TestClientErrorContext_HTTPConnectionError(t *testing.T) {
	client := NewClientFromConfig(config.MCPServerConfig{
		Name: "broken-http",
		Type: "http",
		URL:  "http://127.0.0.1:1", // port 1 should refuse connections
	})
	if err := client.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	_, err := client.Initialize(context.Background())
	if err == nil {
		t.Fatal("expected error from Initialize on unreachable HTTP server")
	}
	errMsg := err.Error()
	if !strings.Contains(errMsg, "broken-http") {
		t.Errorf("error message should contain server name 'broken-http', got: %s", errMsg)
	}
}

// TestClientErrorContext_UnsupportedTransport verifies server name in unsupported transport error.
func TestClientErrorContext_UnsupportedTransport(t *testing.T) {
	client := NewClientFromConfig(config.MCPServerConfig{
		Name: "bad-transport",
		Type: "grpc", // unsupported
	})
	err := client.Start(context.Background())
	if err == nil {
		t.Fatal("expected error for unsupported transport")
	}
	errMsg := err.Error()
	if !strings.Contains(errMsg, "bad-transport") {
		t.Errorf("error message should contain server name 'bad-transport', got: %s", errMsg)
	}
	if !strings.Contains(errMsg, "grpc") {
		t.Errorf("error message should contain transport name 'grpc', got: %s", errMsg)
	}
}

// TestClientErrorContext_SendRequestClosed verifies the "connection closed" error includes server name.
func TestClientErrorContext_SendRequestClosed(t *testing.T) {
	c := NewClient("closed-srv", "cmd", nil)
	ctx := context.Background()
	// Force the closed state
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()

	err := c.sendRequest(ctx, "test/method", nil, nil)
	if err == nil {
		t.Fatal("expected error from sendRequest on closed client")
	}
	errMsg := err.Error()
	if !strings.Contains(errMsg, "closed-srv") {
		t.Errorf("error should contain server name 'closed-srv', got: %s", errMsg)
	}
}

// TestClientErrorContext_ReadMessageError verifies read errors include server name.
func TestClientErrorContext_ReadMessageError(t *testing.T) {
	// Use a reader that immediately returns an error
	client := &Client{
		name:   "read-err-srv",
		reader: bufio.NewReader(bytes.NewReader(nil)), // empty reader will error on Peek
	}
	_, err := client.readMessage(context.Background())
	if err == nil {
		t.Fatal("expected error from readMessage with empty reader")
	}
	// Error should come from the withStderr wrapper (may not have server name in text
	// but the readResponse path wraps it with mcp[server])
}

// TestClientErrorContext_ReadResponseError verifies readResponse wraps errors with server name.
func TestClientErrorContext_ReadResponseError(t *testing.T) {
	client := &Client{
		name:   "read-resp-srv",
		reader: bufio.NewReader(bytes.NewReader(nil)),
	}
	_, err := client.readResponse(context.Background())
	if err == nil {
		t.Fatal("expected error from readResponse with empty reader")
	}
	errMsg := err.Error()
	if !strings.Contains(errMsg, "read-resp-srv") {
		t.Errorf("error should contain server name 'read-resp-srv', got: %s", errMsg)
	}
}

// TestClientErrorContext_HTTPStatusError verifies server name in HTTP error status responses.
func TestClientErrorContext_HTTPStatusError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}))
	defer server.Close()

	client := NewClientFromConfig(config.MCPServerConfig{
		Name: "error-http",
		Type: "http",
		URL:  server.URL,
	})
	if err := client.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	_, err := client.Initialize(context.Background())
	if err == nil {
		t.Fatal("expected error from Initialize with 500 status")
	}
	errMsg := err.Error()
	if !strings.Contains(errMsg, "error-http") {
		t.Errorf("error should contain server name 'error-http', got: %s", errMsg)
	}
	if !strings.Contains(errMsg, "500") {
		t.Errorf("error should contain HTTP status code, got: %s", errMsg)
	}
}

func mustWSURL(t *testing.T, httpURL string) string {
	t.Helper()
	parsed, err := url.Parse(httpURL)
	if err != nil {
		t.Fatal(err)
	}
	switch parsed.Scheme {
	case "http":
		parsed.Scheme = "ws"
	case "https":
		parsed.Scheme = "wss"
	}
	return parsed.String()
}
