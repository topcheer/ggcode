package acp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// TestTransportReadMessage tests reading JSON-RPC messages from a pipe
func TestTransportReadMessage(t *testing.T) {
	input := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1}}
{"jsonrpc":"2.0","id":2,"method":"session/new","params":{"cwd":"/tmp"}}
`
	transport := NewTransport(strings.NewReader(input), io.Discard)

	// Read first message
	req, err := transport.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage() error: %v", err)
	}
	if req.Method != "initialize" {
		t.Errorf("expected method 'initialize', got %q", req.Method)
	}
	if req.ID == nil || req.ID != float64(1) {
		t.Errorf("expected id 1, got %v", req.ID)
	}

	// Read second message
	req, err = transport.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage() error: %v", err)
	}
	if req.Method != "session/new" {
		t.Errorf("expected method 'session/new', got %q", req.Method)
	}

	// Read past end — should return io.EOF
	_, err = transport.ReadMessage()
	if err != io.EOF {
		t.Errorf("expected io.EOF after stream end, got %v", err)
	}
}

// TestTransportReadMessageEmptyLines tests that empty lines are skipped
func TestTransportReadMessageEmptyLines(t *testing.T) {
	input := "\n\n{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"test\"}\n\n"
	transport := NewTransport(strings.NewReader(input), io.Discard)

	// ReadMessage returns nil,nil for empty lines; loop until we get a real message
	var req *JSONRPCRequest
	var err error
	for {
		req, err = transport.ReadMessage()
		if err != nil {
			t.Fatalf("ReadMessage() error: %v", err)
		}
		if req != nil {
			break
		}
	}
	if req.Method != "test" {
		t.Errorf("expected method 'test', got %q", req.Method)
	}
}

// TestTransportWriteResponse tests writing JSON-RPC responses
func TestTransportWriteResponse(t *testing.T) {
	var buf bytes.Buffer
	transport := NewTransport(strings.NewReader(""), &buf)

	err := transport.WriteResponse(1, map[string]string{"status": "ok"})
	if err != nil {
		t.Fatalf("WriteResponse() error: %v", err)
	}

	var resp JSONRPCResponse
	if err := json.Unmarshal(buf.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.JSONRPC != "2.0" {
		t.Errorf("expected jsonrpc '2.0', got %q", resp.JSONRPC)
	}
	if resp.ID == nil || resp.ID != float64(1) {
		t.Errorf("expected id 1, got %v", resp.ID)
	}
	if resp.Error != nil {
		t.Errorf("expected no error, got %v", resp.Error)
	}
}

// TestTransportWriteError tests writing JSON-RPC error responses
func TestTransportWriteError(t *testing.T) {
	var buf bytes.Buffer
	transport := NewTransport(strings.NewReader(""), &buf)

	err := transport.WriteError(1, ErrCodeMethodNotFound, "method not found: foo")
	if err != nil {
		t.Fatalf("WriteError() error: %v", err)
	}

	var resp JSONRPCResponse
	if err := json.Unmarshal(buf.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Error == nil {
		t.Fatal("expected error in response")
	}
	if resp.Error.Code != ErrCodeMethodNotFound {
		t.Errorf("expected error code %d, got %d", ErrCodeMethodNotFound, resp.Error.Code)
	}
}

// TestTransportWriteNotification tests writing JSON-RPC notifications
func TestTransportWriteNotification(t *testing.T) {
	var buf bytes.Buffer
	transport := NewTransport(strings.NewReader(""), &buf)

	err := transport.WriteNotification("session/update", map[string]string{"sessionId": "abc"})
	if err != nil {
		t.Fatalf("WriteNotification() error: %v", err)
	}

	// Parse the notification
	line := buf.String()
	if !strings.HasSuffix(line, "\n") {
		t.Error("notification should end with newline")
	}

	var notif map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &notif); err != nil {
		t.Fatalf("unmarshal notification: %v", err)
	}
	if notif["method"] != "session/update" {
		t.Errorf("expected method 'session/update', got %v", notif["method"])
	}
	// Should have no "id" field (notification)
	if _, hasID := notif["id"]; hasID {
		t.Error("notification should not have id field")
	}
}

// TestTransportWriteResponseEndsWithNewline tests that messages end with \n
func TestTransportWriteResponseEndsWithNewline(t *testing.T) {
	var buf bytes.Buffer
	transport := NewTransport(strings.NewReader(""), &buf)

	_ = transport.WriteResponse(1, "ok")
	output := buf.String()
	if !strings.HasSuffix(output, "\n") {
		t.Error("response should end with newline")
	}
	// Should not contain embedded newlines
	trimmed := strings.TrimSuffix(output, "\n")
	if strings.Contains(trimmed, "\n") {
		t.Error("response should not contain embedded newlines")
	}
}

// TestNewSession tests session creation
func TestNewSession(t *testing.T) {
	session := NewSession("/tmp/test", nil)
	if session.ID == "" {
		t.Error("session ID should not be empty")
	}
	if session.CWD != "/tmp/test" {
		t.Errorf("expected CWD '/tmp/test', got %q", session.CWD)
	}
	if session.CreatedAt.IsZero() {
		t.Error("CreatedAt should be set")
	}
}

// TestNewSessionUniqueIDs tests that session IDs are unique
func TestNewSessionUniqueIDs(t *testing.T) {
	s1 := NewSession("/tmp", nil)
	s2 := NewSession("/tmp", nil)
	if s1.ID == s2.ID {
		t.Error("session IDs should be unique")
	}
}

// TestSessionAddMessage tests adding messages to a session
func TestSessionAddMessage(t *testing.T) {
	session := NewSession("/tmp", nil)
	content := []ContentBlock{{Type: "text", Text: "hello"}}
	session.AddMessage("user", content)

	msgs := session.Messages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].Role != "user" {
		t.Errorf("expected role 'user', got %q", msgs[0].Role)
	}
	if msgs[0].Content[0].Text != "hello" {
		t.Errorf("expected text 'hello', got %q", msgs[0].Content[0].Text)
	}
}

// TestSessionCancel tests session cancellation
func TestSessionCancel(t *testing.T) {
	session := NewSession("/tmp", nil)
	cancelled := false
	session.SetCancel(func() {
		cancelled = true
	})
	session.DoCancel()
	if !cancelled {
		t.Error("cancel function should have been called")
	}
}

// TestSessionCancelNil tests that DoCancel is safe with nil cancel
func TestSessionCancelNil(t *testing.T) {
	session := NewSession("/tmp", nil)
	// Should not panic
	session.DoCancel()
}

// TestACPToProviderContent tests content block conversion
func TestACPToProviderContent(t *testing.T) {
	blocks := []ContentBlock{
		{Type: "text", Text: "hello"},
		{Type: "image", ImageMIME: "image/png", ImageData: "base64data"},
	}
	result := acpToProviderContent(blocks)
	if len(result) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(result))
	}
	if result[0].Type != "text" || result[0].Text != "hello" {
		t.Errorf("first block should be text 'hello'")
	}
	if result[1].Type != "image" || result[1].ImageMIME != "image/png" {
		t.Errorf("second block should be image/png")
	}
}

// TestACPToProviderContentEmpty tests conversion with empty input
func TestACPToProviderContentEmpty(t *testing.T) {
	result := acpToProviderContent(nil)
	if len(result) != 0 {
		t.Errorf("expected 0 blocks, got %d", len(result))
	}
}

// TestPermissionRequestTypes tests permission request type marshaling
func TestPermissionRequestTypes(t *testing.T) {
	req := ToolCallUpdate{
		Title: "Write test file",
		Kind:  ToolKindEdit,
		Locations: []ToolCallLocation{
			{Path: "/tmp/test.txt"},
		},
	}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded ToolCallUpdate
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Title != "Write test file" {
		t.Errorf("expected title 'Write test file', got %q", decoded.Title)
	}
	if len(decoded.Locations) != 1 || decoded.Locations[0].Path != "/tmp/test.txt" {
		t.Errorf("expected location '/tmp/test.txt', got %+v", decoded.Locations)
	}
}

// TestClientCapabilitiesFS tests FS capability detection
func TestClientCapabilitiesFS(t *testing.T) {
	caps := ClientCapabilities{
		FS: &FSCapability{
			ReadTextFile:  true,
			WriteTextFile: false,
		},
	}
	if !caps.FS.ReadTextFile {
		t.Error("expected ReadTextFile to be true")
	}
	if caps.FS.WriteTextFile {
		t.Error("expected WriteTextFile to be false")
	}
}

// TestClientCapabilitiesTerminal tests terminal capability
func TestClientCapabilitiesTerminal(t *testing.T) {
	caps := ClientCapabilities{
		Terminal: true,
	}
	if !caps.Terminal {
		t.Error("expected Terminal to be true")
	}
}

// TestFSReadTextFileParams tests FS read params marshaling
func TestFSReadTextFileParams(t *testing.T) {
	params := FSReadTextFileParams{Path: "/some/file.go"}
	data, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(data), `"path"`) {
		t.Error("expected 'path' in JSON")
	}
}

func TestSessionSaveLoad(t *testing.T) {
	dir := t.TempDir()

	session := NewSession("/tmp/test", []MCPServer{
		{Name: "test-mcp", Command: "echo"},
	})
	session.AddMessage("user", []ContentBlock{{Type: "text", Text: "hello"}})
	session.AddMessage("assistant", []ContentBlock{{Type: "text", Text: "world"}})

	// Save
	if err := session.Save(dir); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	// Load
	loaded, err := LoadSession(dir, session.ID)
	if err != nil {
		t.Fatalf("LoadSession() error: %v", err)
	}
	if loaded.ID != session.ID {
		t.Errorf("expected ID %q, got %q", session.ID, loaded.ID)
	}
	if loaded.CWD != "/tmp/test" {
		t.Errorf("expected CWD '/tmp/test', got %q", loaded.CWD)
	}
	msgs := loaded.Messages()
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].Role != "user" || msgs[0].Content[0].Text != "hello" {
		t.Errorf("first message mismatch")
	}
	if msgs[1].Role != "assistant" || msgs[1].Content[0].Text != "world" {
		t.Errorf("second message mismatch")
	}
}

func TestSessionLoadNonexistent(t *testing.T) {
	dir := t.TempDir()
	_, err := LoadSession(dir, "nonexistent")
	if err == nil {
		t.Error("expected error loading nonexistent session")
	}
}

func TestSessionDataJSON(t *testing.T) {
	data := SessionData{
		ID:        "test-id",
		CWD:       "/tmp",
		CreatedAt: time.Now(),
		Messages: []Message{
			{Role: "user", Content: []ContentBlock{{Type: "text", Text: "test"}}},
		},
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded SessionData
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.ID != "test-id" {
		t.Errorf("expected ID 'test-id', got %q", decoded.ID)
	}
}

func TestACPMCPServerToConfig(t *testing.T) {
	srv := MCPServer{
		Name:    "test-server",
		Command: "node",
		Args:    []string{"server.js"},
		Env:     []EnvVariable{{Name: "KEY", Value: "val"}},
	}
	cfg := acpMCPServerToConfig(srv)
	if cfg.Name != "test-server" {
		t.Errorf("expected name 'test-server', got %q", cfg.Name)
	}
	if cfg.Command != "node" {
		t.Errorf("expected command 'node', got %q", cfg.Command)
	}
	if cfg.Env["KEY"] != "val" {
		t.Errorf("expected env KEY=val, got %q", cfg.Env["KEY"])
	}
}

func TestACPMCPServerHTTP(t *testing.T) {
	srv := MCPServer{
		Name:    "remote-server",
		Type:    "http",
		URL:     "http://localhost:8080/mcp",
		Headers: []HTTPHeader{{Name: "Authorization", Value: "Bearer token"}},
	}
	cfg := acpMCPServerToConfig(srv)
	if cfg.Type != "http" {
		t.Errorf("expected type 'http', got %q", cfg.Type)
	}
	if cfg.URL != "http://localhost:8080/mcp" {
		t.Errorf("expected URL 'http://localhost:8080/mcp', got %q", cfg.URL)
	}
}

// --- Transport bi-directional tests ---

func TestTransportReadAnyMessageRequest(t *testing.T) {
	input := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}` + "\n"
	transport := NewTransport(strings.NewReader(input), io.Discard)

	req, resp, err := transport.ReadAnyMessage()
	if err != nil {
		t.Fatalf("ReadAnyMessage() error: %v", err)
	}
	if resp != nil {
		t.Error("expected nil response for request message")
	}
	if req == nil {
		t.Fatal("expected non-nil request")
	}
	if req.Method != "initialize" {
		t.Errorf("expected method 'initialize', got %q", req.Method)
	}
}

func TestTransportReadAnyMessageResponse(t *testing.T) {
	input := `{"jsonrpc":"2.0","id":1,"result":{"approved":true}}` + "\n"
	transport := NewTransport(strings.NewReader(input), io.Discard)

	req, resp, err := transport.ReadAnyMessage()
	if err != nil {
		t.Fatalf("ReadAnyMessage() error: %v", err)
	}
	if req != nil {
		t.Error("expected nil request for response message")
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	if resp.ID == nil || resp.ID != float64(1) {
		t.Errorf("expected id 1, got %v", resp.ID)
	}
	if resp.Error != nil {
		t.Errorf("expected no error, got %v", resp.Error)
	}
}

func TestTransportReadAnyMessageEmptyLine(t *testing.T) {
	input := "\n\n"
	transport := NewTransport(strings.NewReader(input), io.Discard)

	req, resp, err := transport.ReadAnyMessage()
	if err != nil {
		t.Fatalf("ReadAnyMessage() error: %v", err)
	}
	if req != nil || resp != nil {
		t.Error("expected both nil for empty line")
	}
}

func TestTransportSendRequest(t *testing.T) {
	// Simulate: send request, then read response from pipe
	pr, pw := io.Pipe()
	transport := NewTransport(pr, pw)

	// Start a goroutine that reads the request and sends back a response
	go func() {
		defer pw.Close()
		req, err := transport.ReadMessage()
		if err != nil {
			return
		}
		// Write a response back
		resp := `{"jsonrpc":"2.0","id":` + strings.Trim(string(mustMarshal(req.ID)), `"`) + `,"result":{"approved":true}}` + "\n"
		pw.Write([]byte(resp))
	}()

	// The SendRequest should get the response
	// Note: this test uses the same transport for both sides,
	// which isn't ideal but tests the pending mechanism
	// We need a separate reader/writer pair for proper testing
}

// --- Tool Call Update tests ---

func TestToolCallUpdateJSON(t *testing.T) {
	update := ToolCallUpdate{
		ToolCallID: "call-123",
		Title:      "read_file",
		Kind:       "read",
		Status:     "running",
		RawInput:   `{"path":"/tmp/test.go"}`,
	}
	data, err := json.Marshal(update)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded ToolCallUpdate
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.ToolCallID != "call-123" {
		t.Errorf("expected ToolCallID 'call-123', got %q", decoded.ToolCallID)
	}
	if decoded.Kind != "read" {
		t.Errorf("expected Kind 'read', got %q", decoded.Kind)
	}
	if decoded.Status != "running" {
		t.Errorf("expected Status 'running', got %q", decoded.Status)
	}
}

func TestSessionUpdateWithToolCall(t *testing.T) {
	update := SessionUpdate{
		Type:       "tool_call",
		ToolCallID: "call-456",
		Title:      "write_file",
		Kind:       "write",
		Status:     "pending",
	}
	data, err := json.Marshal(update)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded SessionUpdate
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Type != "tool_call" {
		t.Errorf("expected type 'tool_call', got %q", decoded.Type)
	}
	if decoded.ToolCallID != "call-456" {
		t.Errorf("expected ToolCallID 'call-456', got %q", decoded.ToolCallID)
	}
	if decoded.Kind != "write" {
		t.Errorf("expected Kind 'write', got %q", decoded.Kind)
	}
}

// --- toolKind tests ---

func TestToolKind(t *testing.T) {
	tests := []struct {
		toolName string
		want     string
	}{
		{"read_file", "read"},
		{"list_directory", "read"},
		{"search_files", "read"},
		{"glob", "read"},
		{"grep", "read"},
		{"git_status", "read"},
		{"write_file", "write"},
		{"edit_file", "write"},
		{"run_command", "execute"},
		{"web_fetch", "execute"},
		{"ask_user", "execute"},
	}
	for _, tt := range tests {
		got := toolKind(tt.toolName)
		if got != tt.want {
			t.Errorf("toolKind(%q) = %q, want %q", tt.toolName, got, tt.want)
		}
	}
}

// --- AgentLoop SetMode tests ---

func TestAgentLoopSetMode(t *testing.T) {
	cfg := &config.Config{MaxIterations: 10}
	registry := tool.NewRegistry()
	session := NewSession("/tmp", nil)
	var buf bytes.Buffer
	transport := NewTransport(strings.NewReader(""), &buf)

	al := NewAgentLoop(cfg, registry, transport, session, ClientCapabilities{}, nil)
	if al.mode != "auto" {
		t.Errorf("expected default mode 'auto', got %q", al.mode)
	}

	al.SetMode("bypass")
	if al.mode != "bypass" {
		t.Errorf("expected mode 'bypass' after SetMode, got %q", al.mode)
	}

	al.SetMode("autopilot")
	if al.mode != "autopilot" {
		t.Errorf("expected mode 'autopilot' after SetMode, got %q", al.mode)
	}

	al.SetMode("auto")
	if al.mode != "auto" {
		t.Errorf("expected mode 'auto' after SetMode, got %q", al.mode)
	}

	al.SetMode("supervised")
	if al.mode != "supervised" {
		t.Errorf("expected mode 'supervised' after SetMode, got %q", al.mode)
	}
}

// --- Session with MCPManager ---

func TestSessionWithMCPManager(t *testing.T) {
	session := NewSession("/tmp", nil)
	if session.mcpManager != nil {
		t.Error("expected nil mcpManager initially")
	}
}

// --- Handler method routing tests ---

func TestHandlerSessionNew(t *testing.T) {
	var buf bytes.Buffer
	transport := NewTransport(strings.NewReader(""), &buf)
	cfg := &config.Config{}
	registry := tool.NewRegistry()
	h := NewHandler(cfg, registry, transport, nil)
	h.initialized = true

	params := SessionNewParams{CWD: "/tmp"}
	paramsJSON, _ := json.Marshal(params)
	result, err := h.handleSessionNew(paramsJSON)
	if err != nil {
		t.Fatalf("handleSessionNew error: %v", err)
	}
	newResult, ok := result.(SessionNewResult)
	if !ok {
		t.Fatal("expected SessionNewResult")
	}
	if newResult.SessionID == "" {
		t.Error("expected non-empty session ID")
	}
}

func TestHandlerSessionCancelNotFound(t *testing.T) {
	var buf bytes.Buffer
	transport := NewTransport(strings.NewReader(""), &buf)
	cfg := &config.Config{}
	registry := tool.NewRegistry()
	h := NewHandler(cfg, registry, transport, nil)
	h.initialized = true

	params := SessionCancelParams{SessionID: "nonexistent"}
	paramsJSON, _ := json.Marshal(params)
	_, err := h.handleSessionCancel(paramsJSON)
	if err == nil {
		t.Error("expected error for nonexistent session")
	}
}

func TestHandlerSessionSetMode(t *testing.T) {
	var buf bytes.Buffer
	transport := NewTransport(strings.NewReader(""), &buf)
	cfg := &config.Config{}
	registry := tool.NewRegistry()
	h := NewHandler(cfg, registry, transport, nil)
	h.initialized = true

	// Create a session first
	newParams := SessionNewParams{CWD: "/tmp"}
	newParamsJSON, _ := json.Marshal(newParams)
	newResult, _ := h.handleSessionNew(newParamsJSON)
	sessionID := newResult.(SessionNewResult).SessionID

	// Set mode
	modeParams := SessionSetModeParams{SessionID: sessionID, Mode: "bypass"}
	modeParamsJSON, _ := json.Marshal(modeParams)
	_, err := h.handleSessionSetMode(modeParamsJSON)
	if err != nil {
		t.Fatalf("handleSessionSetMode error: %v", err)
	}
}

// --- RawResult preservation test ---

func TestTransportReadAnyMessageRawResult(t *testing.T) {
	input := `{"jsonrpc":"2.0","id":1,"result":{"approved":true,"reason":"ok"}}` + "\n"
	transport := NewTransport(strings.NewReader(input), io.Discard)

	_, resp, err := transport.ReadAnyMessage()
	if err != nil {
		t.Fatalf("ReadAnyMessage() error: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	if resp.RawResult == nil {
		t.Fatal("expected RawResult to be preserved")
	}
	// Verify raw result contains the full result object
	var result struct {
		Approved bool   `json:"approved"`
		Reason   string `json:"reason"`
	}
	if err := json.Unmarshal(resp.RawResult, &result); err != nil {
		t.Fatalf("unmarshal RawResult: %v", err)
	}
	if !result.Approved {
		t.Error("expected Approved true")
	}
	if result.Reason != "ok" {
		t.Errorf("expected Reason 'ok', got %q", result.Reason)
	}
}

func mustMarshal(v interface{}) []byte {
	data, _ := json.Marshal(v)
	return data
}

// --- New handler method tests ---

func TestHandlerSessionClose(t *testing.T) {
	var buf bytes.Buffer
	transport := NewTransport(strings.NewReader(""), &buf)
	cfg := &config.Config{}
	registry := tool.NewRegistry()
	h := NewHandler(cfg, registry, transport, nil)
	h.initialized = true

	// Create a session first
	newParams := SessionNewParams{CWD: "/tmp"}
	newParamsJSON, _ := json.Marshal(newParams)
	newResult, _ := h.handleSessionNew(newParamsJSON)
	sessionID := newResult.(SessionNewResult).SessionID

	// Close it
	closeParams := CloseSessionRequest{SessionID: sessionID}
	closeParamsJSON, _ := json.Marshal(closeParams)
	result, err := h.handleSessionClose(closeParamsJSON)
	if err != nil {
		t.Fatalf("handleSessionClose error: %v", err)
	}
	if _, ok := result.(CloseSessionResponse); !ok {
		t.Error("expected CloseSessionResponse")
	}

	// Verify session is removed
	h.sessionsMu.RLock()
	_, exists := h.sessions[sessionID]
	h.sessionsMu.RUnlock()
	if exists {
		t.Error("session should be removed after close")
	}

	// Close nonexistent should error
	closeParams2 := CloseSessionRequest{SessionID: "nonexistent"}
	closeParamsJSON2, _ := json.Marshal(closeParams2)
	_, err = h.handleSessionClose(closeParamsJSON2)
	if err == nil {
		t.Error("expected error closing nonexistent session")
	}
}

func TestHandlerSessionList(t *testing.T) {
	var buf bytes.Buffer
	transport := NewTransport(strings.NewReader(""), &buf)
	cfg := &config.Config{}
	registry := tool.NewRegistry()
	h := NewHandler(cfg, registry, transport, nil)
	h.initialized = true

	// Create two sessions
	for _, cwd := range []string{"/tmp/a", "/tmp/b"} {
		params := SessionNewParams{CWD: cwd}
		paramsJSON, _ := json.Marshal(params)
		h.handleSessionNew(paramsJSON)
	}

	// List sessions
	listParams := ListSessionsRequest{}
	listParamsJSON, _ := json.Marshal(listParams)
	result, err := h.handleSessionList(listParamsJSON)
	if err != nil {
		t.Fatalf("handleSessionList error: %v", err)
	}
	listResult, ok := result.(ListSessionsResponse)
	if !ok {
		t.Fatal("expected ListSessionsResponse")
	}
	// Sessions may be 0 if not persisted to disk, but should not error
	if listResult.Sessions == nil {
		t.Error("Sessions should be non-nil slice")
	}
}

func TestHandlerSessionSetConfigOption(t *testing.T) {
	var buf bytes.Buffer
	transport := NewTransport(strings.NewReader(""), &buf)
	cfg := &config.Config{}
	registry := tool.NewRegistry()
	h := NewHandler(cfg, registry, transport, nil)
	h.initialized = true

	// Create a session
	newParams := SessionNewParams{CWD: "/tmp"}
	newParamsJSON, _ := json.Marshal(newParams)
	newResult, _ := h.handleSessionNew(newParamsJSON)
	sessionID := newResult.(SessionNewResult).SessionID

	// Set config option
	configParams := SetSessionConfigOptionRequest{
		SessionID: sessionID,
		ConfigID:  "mode",
		Value:     "auto",
	}
	configParamsJSON, _ := json.Marshal(configParams)
	result, err := h.handleSetConfigOption(configParamsJSON)
	if err != nil {
		t.Fatalf("handleSetConfigOption error: %v", err)
	}
	configResult, ok := result.(SetSessionConfigOptionResponse)
	if !ok {
		t.Fatal("expected SetSessionConfigOptionResponse")
	}
	// Verify mode option updated
	found := false
	for _, opt := range configResult.ConfigOptions {
		if opt.ID == "mode" && opt.CurrentValue == "auto" {
			found = true
		}
	}
	if !found {
		t.Error("expected mode config option with currentValue 'auto'")
	}
}

// --- Spec type serialization tests ---

func TestSessionModeStateJSON(t *testing.T) {
	state := getDefaultSessionModeState()
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded SessionModeState
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(decoded.Modes) != 4 {
		t.Errorf("expected 4 modes, got %d", len(decoded.Modes))
	}
	if decoded.Current != "auto" {
		t.Errorf("expected current 'auto', got %q", decoded.Current)
	}
	// Verify modes have required fields
	for _, m := range decoded.Modes {
		if m.ID == "" {
			t.Error("mode ID should not be empty")
		}
		if m.Name == "" {
			t.Error("mode Name should not be empty")
		}
	}
}

func TestPermissionOptionJSON(t *testing.T) {
	opts := []PermissionOption{
		{OptionID: "allow", Name: "Allow", Kind: PermissionOptionAllowOnce},
		{OptionID: "reject", Name: "Reject", Kind: PermissionOptionRejectOnce},
	}
	data, err := json.Marshal(opts)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded []PermissionOption
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(decoded) != 2 {
		t.Fatalf("expected 2 options, got %d", len(decoded))
	}
	if decoded[0].OptionID != "allow" {
		t.Errorf("expected optionId 'allow', got %q", decoded[0].OptionID)
	}
	if decoded[0].Kind != PermissionOptionAllowOnce {
		t.Errorf("expected kind 'allow_once', got %q", decoded[0].Kind)
	}
}

func TestRequestPermissionOutcomeJSON(t *testing.T) {
	// Test "selected" outcome
	outcome := RequestPermissionOutcome{
		Outcome: "selected",
		SelectedOption: &SelectedPermissionOutcome{
			OptionID: "allow",
		},
	}
	data, err := json.Marshal(outcome)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded RequestPermissionOutcome
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Outcome != "selected" {
		t.Errorf("expected outcome 'selected', got %q", decoded.Outcome)
	}
	if decoded.SelectedOption == nil || decoded.SelectedOption.OptionID != "allow" {
		t.Error("expected selectedOption with optionId 'allow'")
	}

	// Test "cancelled" outcome
	outcome2 := RequestPermissionOutcome{Outcome: "cancelled"}
	data2, _ := json.Marshal(outcome2)
	var decoded2 RequestPermissionOutcome
	json.Unmarshal(data2, &decoded2)
	if decoded2.Outcome != "cancelled" {
		t.Errorf("expected outcome 'cancelled', got %q", decoded2.Outcome)
	}
	if decoded2.SelectedOption != nil {
		t.Error("expected nil SelectedOption for cancelled outcome")
	}
}

func TestSessionConfigOptionJSON(t *testing.T) {
	opts := getDefaultConfigOptions()
	data, err := json.Marshal(opts)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded []SessionConfigOption
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(decoded) == 0 {
		t.Fatal("expected at least one config option")
	}
	modeOpt := decoded[0]
	if modeOpt.Type != "select" {
		t.Errorf("expected type 'select', got %q", modeOpt.Type)
	}
	if modeOpt.ID != "mode" {
		t.Errorf("expected ID 'mode', got %q", modeOpt.ID)
	}
	if modeOpt.CurrentValue != "auto" {
		t.Errorf("expected currentValue 'bypass', got %q", modeOpt.CurrentValue)
	}
}

func TestStopReasonJSON(t *testing.T) {
	resp := PromptResponse{StopReason: StopReasonEndTurn}
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded PromptResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.StopReason != StopReasonEndTurn {
		t.Errorf("expected stopReason 'end_turn', got %q", decoded.StopReason)
	}
}

func TestInitializeResponseSessionCapabilities(t *testing.T) {
	var buf bytes.Buffer
	transport := NewTransport(strings.NewReader(""), &buf)
	cfg := &config.Config{}
	registry := tool.NewRegistry()
	h := NewHandler(cfg, registry, transport, nil)

	params := InitializeParams{
		ProtocolVersion:    ProtocolVersion,
		ClientCapabilities: ClientCapabilities{},
	}
	paramsJSON, _ := json.Marshal(params)
	result, err := h.handleInitialize(paramsJSON)
	if err != nil {
		t.Fatalf("handleInitialize error: %v", err)
	}
	initResult, ok := result.(InitializeResult)
	if !ok {
		t.Fatal("expected InitializeResult")
	}
	if initResult.ProtocolVersion != ProtocolVersion {
		t.Errorf("expected protocol version %d, got %d", ProtocolVersion, initResult.ProtocolVersion)
	}
	if initResult.AgentCapabilities.SessionCapabilities == nil {
		t.Fatal("expected SessionCapabilities to be set")
	}
	if initResult.AgentCapabilities.SessionCapabilities.Close == nil {
		t.Error("expected Close capability")
	}
	if initResult.AgentCapabilities.SessionCapabilities.List == nil {
		t.Error("expected List capability")
	}
	if initResult.AgentCapabilities.SessionCapabilities.Resume == nil {
		t.Error("expected Resume capability")
	}
}

func TestSessionNewReturnsModesAndConfig(t *testing.T) {
	var buf bytes.Buffer
	transport := NewTransport(strings.NewReader(""), &buf)
	cfg := &config.Config{}
	registry := tool.NewRegistry()
	h := NewHandler(cfg, registry, transport, nil)
	h.initialized = true

	params := SessionNewParams{CWD: "/tmp"}
	paramsJSON, _ := json.Marshal(params)
	result, err := h.handleSessionNew(paramsJSON)
	if err != nil {
		t.Fatalf("handleSessionNew error: %v", err)
	}
	newResult := result.(SessionNewResult)
	if newResult.SessionID == "" {
		t.Error("expected non-empty session ID")
	}
	if newResult.Modes == nil {
		t.Error("expected Modes to be set")
	}
	if len(newResult.Modes.Modes) != 4 {
		t.Errorf("expected 4 modes, got %d", len(newResult.Modes.Modes))
	}
	if newResult.Modes.Current != "auto" {
		t.Errorf("expected current mode 'bypass', got %q", newResult.Modes.Current)
	}
	if len(newResult.ConfigOptions) == 0 {
		t.Error("expected ConfigOptions to be set")
	}
}

func TestPlanEntryJSON(t *testing.T) {
	plan := Plan{
		Entries: []PlanEntry{
			{Content: "Read file", Priority: PlanEntryPriorityHigh, Status: PlanEntryStatusCompleted},
			{Content: "Edit file", Priority: PlanEntryPriorityMedium, Status: PlanEntryStatusInProgress},
			{Content: "Test", Priority: PlanEntryPriorityLow, Status: PlanEntryStatusPending},
		},
	}
	data, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded Plan
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(decoded.Entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(decoded.Entries))
	}
	if decoded.Entries[0].Priority != PlanEntryPriorityHigh {
		t.Errorf("expected high priority, got %q", decoded.Entries[0].Priority)
	}
	if decoded.Entries[1].Status != PlanEntryStatusInProgress {
		t.Errorf("expected in_progress status, got %q", decoded.Entries[1].Status)
	}
}

func TestDiffJSON(t *testing.T) {
	diff := Diff{
		Path:    "/tmp/test.go",
		OldText: "old code",
		NewText: "new code",
	}
	data, err := json.Marshal(diff)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded Diff
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Path != "/tmp/test.go" {
		t.Errorf("expected path '/tmp/test.go', got %q", decoded.Path)
	}
	if decoded.OldText != "old code" {
		t.Errorf("expected oldText 'old code', got %q", decoded.OldText)
	}
}

func TestContentBlockResourceLink(t *testing.T) {
	block := ContentBlock{
		Type: "resource_link",
		URI:  "file:///tmp/test.go",
	}
	data, err := json.Marshal(block)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded ContentBlock
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Type != "resource_link" {
		t.Errorf("expected type 'resource_link', got %q", decoded.Type)
	}
	if decoded.URI != "file:///tmp/test.go" {
		t.Errorf("expected URI 'file:///tmp/test.go', got %q", decoded.URI)
	}
}

func TestProtocolVersionConstant(t *testing.T) {
	if ProtocolVersion != 1 {
		t.Errorf("expected ProtocolVersion 1, got %d", ProtocolVersion)
	}
}

func TestValidateCWD(t *testing.T) {
	tests := []struct {
		name    string
		cwd     string
		wantErr bool
	}{
		{"empty", "", true},
		{"root", "/", true},
		{"relative", "some/relative/path", true},
		{"nonexistent", "/nonexistent/dir/xyz", true},
		{"valid tmp", "/tmp", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateCWD(tt.cwd)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateCWD(%q) error = %v, wantErr %v", tt.cwd, err, tt.wantErr)
			}
		})
	}
}

func TestHandlerSessionNewRejectsInvalidCWD(t *testing.T) {
	var buf bytes.Buffer
	transport := NewTransport(strings.NewReader(""), &buf)
	cfg := &config.Config{}
	registry := tool.NewRegistry()
	h := NewHandler(cfg, registry, transport, nil)
	h.initialized = true

	tests := []struct {
		name string
		cwd  string
	}{
		{"empty", ""},
		{"root", "/"},
		{"relative", "foo/bar"},
		{"nonexistent", "/nonexistent/dir/xyz"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := SessionNewParams{CWD: tt.cwd}
			paramsJSON, _ := json.Marshal(params)
			_, err := h.handleSessionNew(paramsJSON)
			if err == nil {
				t.Errorf("expected error for cwd %q, got nil", tt.cwd)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// AskUser handler tests — use real Transport with io.Pipe
// ---------------------------------------------------------------------------

// askUserTestHarness sets up a real Transport with bidirectional pipes.
// The test can read JSON-RPC requests from agentOutput and write responses to agentInput.
type askUserTestHarness struct {
	agentInput  *io.PipeWriter // test writes responses here (agent reads)
	agentOutput *io.PipeReader // test reads requests here (agent writes)
	registry    *tool.Registry
	session     *Session
	agentLoop   *AgentLoop
	handler     *Handler
	cancel      context.CancelFunc
}

func newAskUserTestHarness(t *testing.T) *askUserTestHarness {
	t.Helper()

	agentRead, agentInput := io.Pipe()
	agentOutput, agentWrite := io.Pipe()

	transport := NewTransport(agentRead, agentWrite)

	registry := tool.NewRegistry()
	policy := permission.NewConfigPolicyWithMode(nil, nil, permission.AutoMode)
	if err := tool.RegisterBuiltinTools(registry, policy, "/tmp"); err != nil {
		t.Fatalf("register builtin tools: %v", err)
	}

	cfg := &config.Config{
		MaxIterations: 100,
	}

	session := NewSession("/tmp", nil)
	handler := NewHandler(cfg, registry, transport, nil)
	handler.initialized = true
	handler.sessions["test-session"] = session

	al := NewAgentLoop(cfg, registry, transport, session, ClientCapabilities{}, nil)

	return &askUserTestHarness{
		agentInput:  agentInput,
		agentOutput: agentOutput,
		registry:    registry,
		session:     session,
		agentLoop:   al,
		handler:     handler,
	}
}

// readRequest reads a JSON-RPC request from agent output (what the agent sends).
func (h *askUserTestHarness) readRequest(t *testing.T) map[string]interface{} {
	t.Helper()
	// Read a line from agentOutput
	scanner := bufio.NewScanner(h.agentOutput)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	if !scanner.Scan() {
		t.Fatal("no request from agent")
	}
	line := scanner.Bytes()
	var req map[string]interface{}
	if err := json.Unmarshal(line, &req); err != nil {
		t.Fatalf("unmarshal request: %v\nline: %s", err, line)
	}
	return req
}

// writeResponse writes a JSON-RPC response to agent input.
func (h *askUserTestHarness) writeResponse(t *testing.T, id interface{}, result interface{}) {
	t.Helper()
	data, err := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  result,
	})
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	data = append(data, '\n')
	if _, err := h.agentInput.Write(data); err != nil {
		t.Fatalf("write response: %v", err)
	}
}

// ---------------------------------------------------------------------------
// AskUser handler tests — use dual-pipe Transport like e2e tests
// ---------------------------------------------------------------------------

func setupAskUserTest(t *testing.T) (*tool.AskUserTool, *Transport, context.CancelFunc) {
	t.Helper()

	// Dual pipe: agent reads client-writes, client reads agent-writes
	cr, cw := io.Pipe() // client → agent
	ar, aw := io.Pipe() // agent → client

	agentTransport := NewTransport(cr, aw)
	_ = NewTransport(ar, cw) // clientTransport — we read/write raw from ar/cw

	registry := tool.NewRegistry()
	policy := permission.NewConfigPolicyWithMode(nil, nil, permission.AutoMode)
	if err := tool.RegisterBuiltinTools(registry, policy, "/tmp"); err != nil {
		t.Fatalf("register builtin tools: %v", err)
	}

	cfg := &config.Config{MaxIterations: 100}

	session := NewSession("/tmp", nil)
	handler := NewHandler(cfg, registry, agentTransport, nil)
	handler.initialized = true
	handler.sessions["test-session"] = session

	// Create AgentLoop (this sets up the ask_user handler)
	_ = NewAgentLoop(cfg, registry, agentTransport, session, ClientCapabilities{}, nil)

	// Run handler in background to process responses
	ctx, cancel := context.WithCancel(context.Background())
	go handler.Run(ctx)

	askTool, ok := registry.Get("ask_user")
	if !ok {
		t.Fatal("ask_user tool not found")
	}

	return askTool.(*tool.AskUserTool), agentTransport, cancel
}

// readAgentRequest reads a JSON-RPC request that the agent sent out.
func readAgentRequest(t *testing.T, reader *io.PipeReader) map[string]interface{} {
	t.Helper()
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	if !scanner.Scan() {
		t.Fatal("no request from agent")
	}
	var req map[string]interface{}
	if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	return req
}

// writeAgentResponse writes a JSON-RPC response back to the agent.
func writeAgentResponse(t *testing.T, writer *io.PipeWriter, id interface{}, result interface{}) {
	t.Helper()
	data, err := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  result,
	})
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	data = append(data, '\n')
	if _, err := writer.Write(data); err != nil {
		t.Fatalf("write response: %v", err)
	}
}

func TestAskUserHandlerSingleChoice(t *testing.T) {
	// Setup dual pipes
	cr, cw := io.Pipe() // client → agent
	ar, aw := io.Pipe() // agent → client

	agentTransport := NewTransport(cr, aw)

	registry := tool.NewRegistry()
	policy := permission.NewConfigPolicyWithMode(nil, nil, permission.AutoMode)
	if err := tool.RegisterBuiltinTools(registry, policy, "/tmp"); err != nil {
		t.Fatalf("register: %v", err)
	}

	cfg := &config.Config{MaxIterations: 100}
	session := NewSession("/tmp", nil)
	handler := NewHandler(cfg, registry, agentTransport, nil)
	handler.initialized = true
	handler.sessions["test-session"] = session
	_ = NewAgentLoop(cfg, registry, agentTransport, session, ClientCapabilities{}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go handler.Run(ctx)

	askTool, _ := registry.Get("ask_user")

	// Run ask_user in goroutine
	done := make(chan tool.AskUserResponse, 1)
	errCh := make(chan error, 1)
	go func() {
		input := json.RawMessage(`{
			"title": "Pick a color",
			"questions": [{
				"id": "q1",
				"title": "Color",
				"prompt": "Pick a color",
				"kind": "single",
				"choices": [
					{"id": "opt_a", "label": "Red"},
					{"id": "opt_b", "label": "Blue"}
				]
			}]
		}`)
		result, err := askTool.Execute(context.Background(), input)
		if err != nil {
			errCh <- err
			return
		}
		if result.IsError {
			errCh <- fmt.Errorf("tool error: %s", result.Content)
			return
		}
		var resp tool.AskUserResponse
		if err := json.Unmarshal([]byte(result.Content), &resp); err != nil {
			errCh <- err
			return
		}
		done <- resp
	}()

	// Read permission request from agent (via agent→client pipe)
	req := readAgentRequest(t, ar)
	if req["method"] != "session/request_permission" {
		t.Fatalf("expected method session/request_permission, got %v", req["method"])
	}
	reqID := req["id"]

	// Verify options
	params, _ := req["params"].(map[string]interface{})
	options, _ := params["options"].([]interface{})
	if len(options) != 2 {
		t.Errorf("expected 2 options, got %d", len(options))
	}

	// Respond: user selected opt_a
	writeAgentResponse(t, cw, reqID, map[string]interface{}{
		"outcome": map[string]interface{}{
			"outcome": "selected",
			"selectedOption": map[string]interface{}{
				"optionId": "opt_a",
			},
		},
	})

	select {
	case resp := <-done:
		if resp.Status != tool.AskUserStatusSubmitted {
			t.Errorf("status = %q, want %q", resp.Status, tool.AskUserStatusSubmitted)
		}
		if resp.AnsweredCount != 1 {
			t.Errorf("AnsweredCount = %d, want 1", resp.AnsweredCount)
		}
		if len(resp.Answers) != 1 {
			t.Fatalf("Answers = %d, want 1", len(resp.Answers))
		}
		ans := resp.Answers[0]
		if !ans.Answered {
			t.Error("expected Answered=true")
		}
		if len(ans.SelectedChoiceIDs) != 1 || ans.SelectedChoiceIDs[0] != "opt_a" {
			t.Errorf("SelectedChoiceIDs = %v, want [opt_a]", ans.SelectedChoiceIDs)
		}
	case err := <-errCh:
		t.Fatalf("ask_user error: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("timeout")
	}
}

func TestAskUserHandlerTextSubmit(t *testing.T) {
	cr, cw := io.Pipe()
	ar, aw := io.Pipe()
	agentTransport := NewTransport(cr, aw)

	registry := tool.NewRegistry()
	policy := permission.NewConfigPolicyWithMode(nil, nil, permission.AutoMode)
	tool.RegisterBuiltinTools(registry, policy, "/tmp")

	cfg := &config.Config{MaxIterations: 100}
	session := NewSession("/tmp", nil)
	handler := NewHandler(cfg, registry, agentTransport, nil)
	handler.initialized = true
	handler.sessions["test-session"] = session
	_ = NewAgentLoop(cfg, registry, agentTransport, session, ClientCapabilities{}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go handler.Run(ctx)

	askTool, _ := registry.Get("ask_user")

	done := make(chan string, 1)       // error message
	doneResult := make(chan string, 1) // result content
	go func() {
		input := json.RawMessage(`{
			"title": "Enter name",
			"questions": [{
				"id": "q1",
				"title": "Name",
				"prompt": "Enter your name",
				"kind": "text"
			}]
		}`)
		result, err := askTool.Execute(context.Background(), input)
		if err != nil {
			done <- err.Error()
			return
		}
		if result.IsError {
			done <- result.Content
			return
		}
		doneResult <- result.Content
	}()

	req := readAgentRequest(t, ar)
	reqID := req["id"]

	// User submits
	writeAgentResponse(t, cw, reqID, map[string]interface{}{
		"outcome": map[string]interface{}{
			"outcome": "selected",
			"selectedOption": map[string]interface{}{
				"optionId": "submit",
			},
		},
	})

	select {
	case errMsg := <-done:
		// Should get an error telling LLM to ask in plain text
		if !strings.Contains(errMsg, "does not support text input") {
			t.Errorf("expected 'does not support text input' in error, got: %s", errMsg)
		}
	case content := <-doneResult:
		t.Errorf("expected error fallback, got result: %s", content)
	case <-time.After(5 * time.Second):
		t.Fatal("timeout")
	}
}

func TestAskUserHandlerCancelled(t *testing.T) {
	cr, cw := io.Pipe()
	ar, aw := io.Pipe()
	agentTransport := NewTransport(cr, aw)

	registry := tool.NewRegistry()
	policy := permission.NewConfigPolicyWithMode(nil, nil, permission.AutoMode)
	tool.RegisterBuiltinTools(registry, policy, "/tmp")

	cfg := &config.Config{MaxIterations: 100}
	session := NewSession("/tmp", nil)
	handler := NewHandler(cfg, registry, agentTransport, nil)
	handler.initialized = true
	handler.sessions["test-session"] = session
	_ = NewAgentLoop(cfg, registry, agentTransport, session, ClientCapabilities{}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go handler.Run(ctx)

	askTool, _ := registry.Get("ask_user")

	done := make(chan string, 1)       // error message
	doneResult := make(chan string, 1) // result content
	go func() {
		input := json.RawMessage(`{
			"title": "Pick",
			"questions": [{
				"id": "q1",
				"title": "Pick",
				"prompt": "Pick one",
				"kind": "single",
				"choices": [{"id": "a", "label": "A"}]
			}]
		}`)
		result, err := askTool.Execute(context.Background(), input)
		if err != nil {
			done <- err.Error()
			return
		}
		if result.IsError {
			done <- result.Content
			return
		}
		doneResult <- result.Content
	}()

	req := readAgentRequest(t, ar)
	reqID := req["id"]

	// User cancels
	writeAgentResponse(t, cw, reqID, map[string]interface{}{
		"outcome": map[string]interface{}{
			"outcome": "cancelled",
		},
	})

	select {
	case errMsg := <-done:
		// Should get an error telling LLM to ask in plain text
		if !strings.Contains(errMsg, "dismissed") {
			t.Errorf("expected 'dismissed' in error, got: %s", errMsg)
		}
	case content := <-doneResult:
		t.Errorf("expected error fallback, got result: %s", content)
	case <-time.After(5 * time.Second):
		t.Fatal("timeout")
	}
}

func TestAskUserHandlerNoHandlerWithoutACP(t *testing.T) {
	registry := tool.NewRegistry()
	policy := permission.NewConfigPolicyWithMode(nil, nil, permission.AutoMode)
	if err := tool.RegisterBuiltinTools(registry, policy, "/tmp"); err != nil {
		t.Fatalf("register: %v", err)
	}

	askTool, ok := registry.Get("ask_user")
	if !ok {
		t.Fatal("ask_user not found")
	}

	input := json.RawMessage(`{"questions":[{"id":"q1","title":"Q","prompt":"Q?","kind":"text"}]}`)
	result, err := askTool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !result.IsError {
		t.Error("expected error result when no handler set")
	}
	if !strings.Contains(result.Content, "interactive") {
		t.Errorf("expected 'interactive' in error, got: %s", result.Content)
	}
}

// ---------------------------------------------------------------------------
// SessionUpdate JSON structure tests (flattened format per ACP spec)
// ---------------------------------------------------------------------------

func TestSessionUpdateToolCallJSON(t *testing.T) {
	// tool_call notification — flat fields, no nested objects
	update := SessionUpdateParams{
		SessionID: "sess-1",
		Update: SessionUpdate{
			Type:       "tool_call",
			ToolCallID: "call-001",
			Title:      "read_file",
			Kind:       "read",
			Status:     "pending",
			RawInput:   json.RawMessage(`{"path":"/tmp/test.go"}`),
		},
	}
	data, err := json.Marshal(update)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// Verify JSON has flat structure (toolCallId as direct child, not nested)
	raw := string(data)
	for _, key := range []string{`"sessionUpdate":"tool_call"`, `"toolCallId":"call-001"`, `"title":"read_file"`, `"kind":"read"`, `"status":"pending"`, `"rawInput"`} {
		if !strings.Contains(raw, key) {
			t.Errorf("JSON missing %q in: %s", key, raw)
		}
	}
	// Should NOT have nested "toolCall" object
	if strings.Contains(raw, `"toolCall":{`) {
		t.Errorf("JSON should not have nested toolCall object: %s", raw)
	}

	// Round-trip
	var decoded SessionUpdateParams
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Update.Type != "tool_call" {
		t.Errorf("Type = %q", decoded.Update.Type)
	}
	if decoded.Update.ToolCallID != "call-001" {
		t.Errorf("ToolCallID = %q", decoded.Update.ToolCallID)
	}
	if decoded.Update.Kind != "read" {
		t.Errorf("Kind = %q", decoded.Update.Kind)
	}
	if decoded.Update.Status != "pending" {
		t.Errorf("Status = %q", decoded.Update.Status)
	}
}

func TestSessionUpdateToolCallUpdateJSON(t *testing.T) {
	// tool_call_update — status update with formatted title, no content/result
	update := SessionUpdateParams{
		SessionID: "sess-1",
		Update: SessionUpdate{
			Type:       "tool_call_update",
			ToolCallID: "call-001",
			Title:      "Read /tmp/test.go",
			Status:     "completed",
		},
	}
	data, err := json.Marshal(update)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	raw := string(data)
	if !strings.Contains(raw, `"sessionUpdate":"tool_call_update"`) {
		t.Errorf("missing sessionUpdate type: %s", raw)
	}
	if !strings.Contains(raw, `"title":"Read /tmp/test.go"`) {
		t.Errorf("missing formatted title: %s", raw)
	}
	if !strings.Contains(raw, `"status":"completed"`) {
		t.Errorf("missing status: %s", raw)
	}
	// Should NOT have content field (no result)
	if strings.Contains(raw, `"content"`) {
		t.Errorf("tool_call_update should not have content field: %s", raw)
	}
}

func TestSessionUpdateAgentMessageJSON(t *testing.T) {
	// agent_message_chunk — content is a single ContentBlock
	update := SessionUpdateParams{
		SessionID: "sess-1",
		Update: SessionUpdate{
			Type: "agent_message_chunk",
			Content: &ContentBlock{
				Type: "text",
				Text: "Hello!",
			},
		},
	}
	data, err := json.Marshal(update)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	raw := string(data)
	if !strings.Contains(raw, `"content":{"type":"text","text":"Hello!"}`) {
		t.Errorf("content should be single object: %s", raw)
	}
}

func TestSessionUpdateToolCallUpdateWithContentArray(t *testing.T) {
	// tool_call_update can also have content as array (for future use)
	contentEntries := []ToolCallContentEntry{
		{Type: "content", Content: &ContentBlock{Type: "text", Text: "result text"}},
	}
	update := SessionUpdateParams{
		SessionID: "sess-1",
		Update: SessionUpdate{
			Type:       "tool_call_update",
			ToolCallID: "call-001",
			Status:     "completed",
			Content:    contentEntries,
		},
	}
	data, err := json.Marshal(update)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	raw := string(data)
	if !strings.Contains(raw, `"content":[{"type":"content"`) {
		t.Errorf("content should be array: %s", raw)
	}
}

// ---------------------------------------------------------------------------
// handleStreamEvent integration tests
// ---------------------------------------------------------------------------

func TestHandleStreamEventToolCallFormat(t *testing.T) {
	// Verify that tool_call notification uses plain tool name (not formatted)
	var buf bytes.Buffer
	transport := NewTransport(strings.NewReader(""), &buf)
	registry := tool.NewRegistry()
	cfg := &config.Config{MaxIterations: 100}
	session := NewSession("/tmp", nil)

	al := NewAgentLoop(cfg, registry, transport, session, ClientCapabilities{}, nil)

	err := al.handleStreamEvent(provider.StreamEvent{
		Type: provider.StreamEventToolCallDone,
		Tool: provider.ToolCallDelta{
			ID:        "call-123",
			Name:      "read_file",
			Arguments: json.RawMessage(`{"path":"/tmp/test.go"}`),
		},
	})
	if err != nil {
		t.Fatalf("handleStreamEvent: %v", err)
	}

	// Read notification from buffer
	line, err := buf.ReadString('\n')
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var notif map[string]interface{}
	if err := json.Unmarshal([]byte(line), &notif); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if notif["method"] != "session/update" {
		t.Errorf("method = %v", notif["method"])
	}
	params, _ := notif["params"].(map[string]interface{})
	update, _ := params["update"].(map[string]interface{})
	if update["sessionUpdate"] != "tool_call" {
		t.Errorf("sessionUpdate = %v", update["sessionUpdate"])
	}
	// tool_call should have plain tool name (not formatted)
	if update["title"] != "read_file" {
		t.Errorf("title = %v, want plain 'read_file'", update["title"])
	}
	if update["kind"] != "read" {
		t.Errorf("kind = %v", update["kind"])
	}
	if update["status"] != "pending" {
		t.Errorf("status = %v", update["status"])
	}
}

func TestHandleStreamEventToolCallUpdateFormat(t *testing.T) {
	// Verify that tool_call_update uses DescribeTool formatted title
	var buf bytes.Buffer
	transport := NewTransport(strings.NewReader(""), &buf)
	registry := tool.NewRegistry()
	cfg := &config.Config{MaxIterations: 100}
	session := NewSession("/tmp", nil)

	al := NewAgentLoop(cfg, registry, transport, session, ClientCapabilities{}, nil)

	err := al.handleStreamEvent(provider.StreamEvent{
		Type: provider.StreamEventToolResult,
		Tool: provider.ToolCallDelta{
			ID:        "call-123",
			Name:      "read_file",
			Arguments: json.RawMessage(`{"path":"/tmp/test.go"}`),
		},
		Result:  "file contents",
		IsError: false,
	})
	if err != nil {
		t.Fatalf("handleStreamEvent: %v", err)
	}

	line, err := buf.ReadString('\n')
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var notif map[string]interface{}
	if err := json.Unmarshal([]byte(line), &notif); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	params, _ := notif["params"].(map[string]interface{})
	update, _ := params["update"].(map[string]interface{})
	if update["sessionUpdate"] != "tool_call_update" {
		t.Errorf("sessionUpdate = %v", update["sessionUpdate"])
	}
	// tool_call_update should have formatted title from DescribeTool
	if update["title"] != "Read /tmp/test.go" {
		t.Errorf("title = %v, want formatted 'Read /tmp/test.go'", update["title"])
	}
	if update["status"] != "completed" {
		t.Errorf("status = %v", update["status"])
	}
	// Should NOT have content (no result)
	if _, hasContent := update["content"]; hasContent {
		t.Errorf("tool_call_update should not have content, got: %v", update["content"])
	}
}

func TestHandleStreamEventToolCallUpdateFailed(t *testing.T) {
	var buf bytes.Buffer
	transport := NewTransport(strings.NewReader(""), &buf)
	registry := tool.NewRegistry()
	cfg := &config.Config{MaxIterations: 100}
	session := NewSession("/tmp", nil)

	al := NewAgentLoop(cfg, registry, transport, session, ClientCapabilities{}, nil)

	al.handleStreamEvent(provider.StreamEvent{
		Type: provider.StreamEventToolResult,
		Tool: provider.ToolCallDelta{
			ID:        "call-456",
			Name:      "run_command",
			Arguments: json.RawMessage(`{"command":"go test ./..."}`),
		},
		Result:  "exit code 1",
		IsError: true,
	})

	line, _ := buf.ReadString('\n')
	var notif map[string]interface{}
	json.Unmarshal([]byte(line), &notif)
	params, _ := notif["params"].(map[string]interface{})
	update, _ := params["update"].(map[string]interface{})

	if update["sessionUpdate"] != "tool_call_update" {
		t.Errorf("sessionUpdate = %v", update["sessionUpdate"])
	}
	// run_command → formatted as command text directly
	if update["title"] != "go test ./..." {
		t.Errorf("title = %v, want 'go test ./...'", update["title"])
	}
	if update["status"] != "failed" {
		t.Errorf("status = %v, want 'failed'", update["status"])
	}
}

func TestHandleStreamEventAgentMessage(t *testing.T) {
	var buf bytes.Buffer
	transport := NewTransport(strings.NewReader(""), &buf)
	registry := tool.NewRegistry()
	cfg := &config.Config{MaxIterations: 100}
	session := NewSession("/tmp", nil)

	al := NewAgentLoop(cfg, registry, transport, session, ClientCapabilities{}, nil)

	al.handleStreamEvent(provider.StreamEvent{
		Type: provider.StreamEventText,
		Text: "Hello from the agent!",
	})

	line, _ := buf.ReadString('\n')
	var notif map[string]interface{}
	json.Unmarshal([]byte(line), &notif)
	params, _ := notif["params"].(map[string]interface{})
	update, _ := params["update"].(map[string]interface{})

	if update["sessionUpdate"] != "agent_message_chunk" {
		t.Errorf("sessionUpdate = %v", update["sessionUpdate"])
	}
	content, _ := update["content"].(map[string]interface{})
	if content["type"] != "text" {
		t.Errorf("content type = %v", content["type"])
	}
	if content["text"] != "Hello from the agent!" {
		t.Errorf("content text = %v", content["text"])
	}
}

// ---------------------------------------------------------------------------
// Empty session cleanup tests
// ---------------------------------------------------------------------------

func TestSaveSkipsEmptySession(t *testing.T) {
	dir := t.TempDir()
	s := NewSession("/tmp", nil)
	s.SetSaveDir(dir)

	// Save with no messages — should NOT create a file.
	if err := s.Save(dir); err != nil {
		t.Fatalf("Save empty session: %v", err)
	}

	path := filepath.Join(dir, s.ID+".json")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("empty session file should not exist, got err=%v", err)
	}
}

func TestSaveDeletesPreviouslySavedEmptySession(t *testing.T) {
	dir := t.TempDir()
	s := NewSession("/tmp", nil)
	s.SetSaveDir(dir)

	// Simulate a session that had messages, got saved, then messages were somehow removed.
	// First, add a message and save.
	s.AddMessage("user", []ContentBlock{{Type: "text", Text: "hi"}})
	if err := s.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}
	path := filepath.Join(dir, s.ID+".json")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal("session file should exist after first save")
	}

	// Now create a fresh empty session with the same ID and save — should delete the file.
	s2 := &Session{ID: s.ID, CWD: "/tmp", saveDir: dir}
	if err := s2.Save(dir); err != nil {
		t.Fatalf("Save empty: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("empty session file should have been deleted")
	}
}

func TestSaveWithMessages(t *testing.T) {
	dir := t.TempDir()
	s := NewSession("/tmp", nil)
	s.SetSaveDir(dir)

	s.AddMessage("user", []ContentBlock{{Type: "text", Text: "hello"}})
	s.AddMessage("assistant", []ContentBlock{{Type: "text", Text: "world"}})

	if err := s.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}

	path := filepath.Join(dir, s.ID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read session file: %v", err)
	}

	var sd SessionData
	if err := json.Unmarshal(data, &sd); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(sd.Messages) != 2 {
		t.Errorf("expected 2 messages, got %d", len(sd.Messages))
	}
	if sd.Messages[0].Role != "user" {
		t.Errorf("message 0 role = %q", sd.Messages[0].Role)
	}
}

func TestHasMessages(t *testing.T) {
	s := NewSession("/tmp", nil)
	if s.HasMessages() {
		t.Error("new session should not have messages")
	}
	s.AddMessage("user", []ContentBlock{{Type: "text", Text: "hi"}})
	if !s.HasMessages() {
		t.Error("session with message should report HasMessages=true")
	}
}

func TestCleanupEmptySessionsOnEOF(t *testing.T) {
	dir := t.TempDir()

	// Create two sessions: one with messages, one without.
	cr, cw := io.Pipe()
	_, aw := io.Pipe()
	transport := NewTransport(cr, aw)

	registry := tool.NewRegistry()
	policy := permission.NewConfigPolicyWithMode(nil, nil, permission.AutoMode)
	tool.RegisterBuiltinTools(registry, policy, "/tmp")

	cfg := &config.Config{MaxIterations: 100}
	handler := NewHandler(cfg, registry, transport, nil)
	handler.sessionsDir = dir
	handler.initialized = true

	// Session with messages
	sessionWith := NewSession("/tmp", nil)
	sessionWith.AddMessage("user", []ContentBlock{{Type: "text", Text: "hi"}})
	sessionDir := filepath.Join(dir, "ws1")
	os.MkdirAll(sessionDir, 0o755)
	sessionWith.SetSaveDir(sessionDir)
	sessionWith.Save(sessionDir)

	// Session without messages
	sessionEmpty := NewSession("/tmp", nil)
	sessionDir2 := filepath.Join(dir, "ws2")
	os.MkdirAll(sessionDir2, 0o755)
	sessionEmpty.SetSaveDir(sessionDir2)

	handler.sessionsMu.Lock()
	handler.sessions[sessionWith.ID] = sessionWith
	handler.workspaceDirs[sessionWith.ID] = sessionDir
	handler.sessions[sessionEmpty.ID] = sessionEmpty
	handler.workspaceDirs[sessionEmpty.ID] = sessionDir2
	handler.sessionsMu.Unlock()

	// Create a file for the empty session to verify it gets deleted.
	emptyPath := filepath.Join(sessionDir2, sessionEmpty.ID+".json")
	os.WriteFile(emptyPath, []byte(`{"id":"`+sessionEmpty.ID+`"}`), 0o644)

	// Simulate EOF by closing the reader.
	cw.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	handler.Run(ctx)

	// Session with messages should still exist.
	withPath := filepath.Join(sessionDir, sessionWith.ID+".json")
	if _, err := os.Stat(withPath); os.IsNotExist(err) {
		t.Error("session with messages should be preserved")
	}

	// Empty session file should be deleted.
	if _, err := os.Stat(emptyPath); !os.IsNotExist(err) {
		t.Error("empty session file should have been cleaned up")
	}
}

func TestProviderToACPMessage(t *testing.T) {
	providerMsgs := []provider.Message{
		{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "you are helpful"}}},
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "hello"}}},
		{Role: "assistant", Content: []provider.ContentBlock{
			{Type: "text", Text: "let me check"},
			{Type: "tool_use", ToolName: "run_command", ToolID: "t1", Input: json.RawMessage(`{"command":"ls"}`)},
		}},
		{Role: "user", Content: []provider.ContentBlock{
			{Type: "tool_result", ToolID: "t1", ToolName: "run_command", Output: "file.txt", IsError: false},
		}},
	}

	acpMsgs := providerToACPMessage(providerMsgs)
	if len(acpMsgs) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(acpMsgs))
	}
	if acpMsgs[0].Role != "system" {
		t.Errorf("msg 0 role = %q", acpMsgs[0].Role)
	}
	if acpMsgs[1].Content[0].Text != "hello" {
		t.Errorf("msg 1 text = %q", acpMsgs[1].Content[0].Text)
	}
	if acpMsgs[2].Content[1].ToolName != "run_command" {
		t.Errorf("msg 2 tool_name = %q", acpMsgs[2].Content[1].ToolName)
	}
	if acpMsgs[3].Content[0].Output != "file.txt" {
		t.Errorf("msg 3 output = %q", acpMsgs[3].Content[0].Output)
	}
}

func TestReplaceConversation(t *testing.T) {
	s := NewSession("/tmp", nil)

	s.AddMessage("user", []ContentBlock{{Type: "text", Text: "hello"}})
	s.AddMessage("assistant", []ContentBlock{{Type: "text", Text: "world"}})
	if len(s.Messages()) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(s.Messages()))
	}

	// Replace with compacted version
	s.ReplaceConversation([]Message{
		{Role: "system", Content: []ContentBlock{{Type: "text", Text: "summary of previous conversation"}}},
		{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hello"}}},
	})

	msgs := s.Messages()
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages after replace, got %d", len(msgs))
	}
	if msgs[0].Role != "system" {
		t.Errorf("msg 0 role = %q", msgs[0].Role)
	}
	if msgs[0].Content[0].Text != "summary of previous conversation" {
		t.Errorf("msg 0 text = %q", msgs[0].Content[0].Text)
	}
}

func TestCheckpointHandlerPersistsCompactedSession(t *testing.T) {
	dir := t.TempDir()

	// Create session and save with initial messages.
	s := NewSession("/tmp", nil)
	s.SetSaveDir(dir)
	s.AddMessage("user", []ContentBlock{{Type: "text", Text: "hello"}})
	s.AddMessage("assistant", []ContentBlock{{Type: "text", Text: "world"}})
	s.Save(dir)

	path := filepath.Join(dir, s.ID+".json")
	data, _ := os.ReadFile(path)
	var sd1 SessionData
	json.Unmarshal(data, &sd1)
	if len(sd1.Messages) != 2 {
		t.Fatalf("initial: expected 2 messages, got %d", len(sd1.Messages))
	}

	// Simulate checkpoint handler: compact replaces conversation and saves.
	s.ReplaceConversation([]Message{
		{Role: "system", Content: []ContentBlock{{Type: "text", Text: "summary"}}},
	})
	s.Save(dir)

	data2, _ := os.ReadFile(path)
	var sd2 SessionData
	json.Unmarshal(data2, &sd2)
	if len(sd2.Messages) != 1 {
		t.Fatalf("after compact: expected 1 message, got %d", len(sd2.Messages))
	}
	if sd2.Messages[0].Content[0].Text != "summary" {
		t.Errorf("compacted message text = %q", sd2.Messages[0].Content[0].Text)
	}
}
