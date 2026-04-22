package acp

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
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
	if req.ID == nil || *req.ID != 1 {
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
	if resp.ID == nil || *resp.ID != 1 {
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
	req := PermissionRequest{
		Type:        "fs_write",
		Path:        "/tmp/test.txt",
		Description: "Write test file",
	}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded PermissionRequest
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Type != "fs_write" {
		t.Errorf("expected type 'fs_write', got %q", decoded.Type)
	}
	if decoded.Path != "/tmp/test.txt" {
		t.Errorf("expected path '/tmp/test.txt', got %q", decoded.Path)
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
	if resp.ID == nil || *resp.ID != 1 {
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
		SessionUpdateType: "tool_call",
		ToolCall: &ToolCallUpdate{
			ToolCallID: "call-456",
			Title:      "write_file",
			Kind:       "write",
			Status:     "pending",
		},
		Content: &ContentBlock{
			Type:     "tool_use",
			ToolName: "write_file",
			ToolID:   "call-456",
		},
	}
	data, err := json.Marshal(update)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded SessionUpdate
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.SessionUpdateType != "tool_call" {
		t.Errorf("expected type 'tool_call', got %q", decoded.SessionUpdateType)
	}
	if decoded.ToolCall == nil {
		t.Fatal("expected ToolCall to be non-nil")
	}
	if decoded.ToolCall.ToolCallID != "call-456" {
		t.Errorf("expected ToolCallID 'call-456', got %q", decoded.ToolCall.ToolCallID)
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
	if al.mode != "supervised" {
		t.Errorf("expected default mode 'supervised', got %q", al.mode)
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

	params := SessionNewParams{CWD: "/tmp/test"}
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
