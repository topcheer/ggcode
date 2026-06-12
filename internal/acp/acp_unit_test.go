//go:build !integration

package acp

import (
	"bytes"
	"context"
	"encoding/json"
	// "fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/tool"
)

// ---------------------------------------------------------------------------
// Session: SaveDir / SetSaveDir
// ---------------------------------------------------------------------------

func TestSessionSaveDirRoundTrip(t *testing.T) {
	s := NewSession("/tmp", nil)
	if s.SaveDir() != "" {
		t.Errorf("expected empty SaveDir initially, got %q", s.SaveDir())
	}
	s.SetSaveDir("/some/path")
	if s.SaveDir() != "/some/path" {
		t.Errorf("expected '/some/path', got %q", s.SaveDir())
	}
}

// ---------------------------------------------------------------------------
// Session: Save with multiple messages round-trip
// ---------------------------------------------------------------------------

func TestSessionSaveLoadMultiMsg(t *testing.T) {
	dir := t.TempDir()
	s := NewSession("/tmp/project", []MCPServer{
		{Name: "srv1", Command: "node", Args: []string{"a.js"}},
	})
	s.AddMessage("user", []ContentBlock{
		{Type: "text", Text: "first"},
		{Type: "image", ImageMIME: "image/png", ImageData: "data"},
	})
	s.AddMessage("assistant", []ContentBlock{
		{Type: "text", Text: "reply"},
		{Type: "tool_use", ToolName: "read_file", ToolID: "t1", Input: json.RawMessage(`{"path":"/x"}`)},
	})
	s.AddMessage("user", []ContentBlock{
		{Type: "tool_result", ToolID: "t1", Output: "content", IsError: false},
	})

	if err := s.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := LoadSession(dir, s.ID)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if loaded.CWD != "/tmp/project" {
		t.Errorf("CWD mismatch: %q", loaded.CWD)
	}
	msgs := loaded.Messages()
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(msgs))
	}
	if msgs[0].Role != "user" || len(msgs[0].Content) != 2 {
		t.Errorf("first message mismatch: %+v", msgs[0])
	}
	if msgs[1].Content[1].ToolName != "read_file" {
		t.Errorf("tool_use block mismatch: %+v", msgs[1].Content[1])
	}
	if len(loaded.MCPServers) != 1 || loaded.MCPServers[0].Name != "srv1" {
		t.Errorf("MCP servers mismatch: %+v", loaded.MCPServers)
	}
}

// ---------------------------------------------------------------------------
// generateSessionID format
// ---------------------------------------------------------------------------

func TestGenerateSessionIDFormat(t *testing.T) {
	id := generateSessionID()
	if len(id) != 32 {
		t.Errorf("expected 32-char hex ID, got %d chars: %q", len(id), id)
	}
	for _, c := range id {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("unexpected char %c in session ID %q", c, id)
			break
		}
	}
}

// ---------------------------------------------------------------------------
// acpToProviderContent: unknown type fallback
// ---------------------------------------------------------------------------

func TestACPToProviderContentUnknownType(t *testing.T) {
	blocks := []ContentBlock{
		{Type: "audio", Text: "fallback-text"},
		{Type: "unknown_type"},
	}
	result := acpToProviderContent(blocks)
	if len(result) != 1 {
		t.Fatalf("expected 1 block (unknown without text skipped), got %d", len(result))
	}
	if result[0].Type != "text" || result[0].Text != "fallback-text" {
		t.Errorf("expected fallback text block, got %+v", result[0])
	}
}

// ---------------------------------------------------------------------------
// workspaceSessionsDir
// ---------------------------------------------------------------------------

func TestWorkspaceSessionsDirEmptyCWD(t *testing.T) {
	result := workspaceSessionsDir("/base", "")
	if result != "/base" {
		t.Errorf("expected '/base', got %q", result)
	}
}

func TestWorkspaceSessionsDirWithCWD(t *testing.T) {
	result := workspaceSessionsDir("/base", "/home/user/project")
	if result == "/base" {
		t.Error("expected a subdirectory, got base dir")
	}
	if !strings.HasPrefix(result, "/base/") {
		t.Errorf("expected result under /base/, got %q", result)
	}
	// Should be deterministic
	result2 := workspaceSessionsDir("/base", "/home/user/project")
	if result != result2 {
		t.Errorf("expected deterministic result, got %q then %q", result, result2)
	}
	// Different CWDs should produce different dirs
	result3 := workspaceSessionsDir("/base", "/home/user/other")
	if result == result3 {
		t.Error("expected different CWDs to produce different session dirs")
	}
}

// ---------------------------------------------------------------------------
// getDefaultSessionModeState / getDefaultConfigOptions
// ---------------------------------------------------------------------------

func TestGetDefaultSessionModeStateValues(t *testing.T) {
	modes := getDefaultSessionModeState()
	if modes.Current != "auto" {
		t.Errorf("expected current 'auto', got %q", modes.Current)
	}
	if len(modes.Modes) != 4 {
		t.Fatalf("expected 4 modes, got %d", len(modes.Modes))
	}
	expected := []string{"supervised", "auto", "bypass", "autopilot"}
	for i, m := range modes.Modes {
		if m.ID != expected[i] {
			t.Errorf("mode[%d].ID = %q, want %q", i, m.ID, expected[i])
		}
	}
}

func TestGetDefaultSessionModeStatePtrNotNil(t *testing.T) {
	ptr := getDefaultSessionModeStatePtr()
	if ptr == nil {
		t.Fatal("expected non-nil pointer")
	}
	if ptr.Current != "auto" {
		t.Errorf("expected current 'auto', got %q", ptr.Current)
	}
}

func TestGetDefaultConfigOptionsValues(t *testing.T) {
	opts := getDefaultConfigOptions()
	if len(opts) == 0 {
		t.Fatal("expected at least one config option")
	}
	modeOpt := opts[0]
	if modeOpt.ID != "mode" {
		t.Errorf("expected ID 'mode', got %q", modeOpt.ID)
	}
	if modeOpt.Type != "select" {
		t.Errorf("expected type 'select', got %q", modeOpt.Type)
	}
	if modeOpt.CurrentValue != "auto" {
		t.Errorf("expected currentValue 'auto', got %q", modeOpt.CurrentValue)
	}
	if len(modeOpt.Options) != 4 {
		t.Errorf("expected 4 options, got %d", len(modeOpt.Options))
	}
}

// ---------------------------------------------------------------------------
// Transport: WriteRaw / ReadRaw
// ---------------------------------------------------------------------------

func TestTransportWriteRawAppendsNewline(t *testing.T) {
	var buf bytes.Buffer
	transport := NewTransport(strings.NewReader(""), &buf)
	data := []byte(`{"test":true}`)
	if err := transport.WriteRaw(data); err != nil {
		t.Fatalf("WriteRaw error: %v", err)
	}
	if !bytes.HasSuffix(buf.Bytes(), []byte("\n")) {
		t.Error("WriteRaw should append newline")
	}
	if !bytes.HasPrefix(buf.Bytes(), data) {
		t.Error("WriteRaw should write data")
	}
}

func TestTransportReadRawLines(t *testing.T) {
	input := "line1\nline2\n"
	transport := NewTransport(strings.NewReader(input), io.Discard)

	raw, err := transport.ReadRaw()
	if err != nil {
		t.Fatalf("ReadRaw error: %v", err)
	}
	if string(raw) != "line1" {
		t.Errorf("expected 'line1', got %q", string(raw))
	}

	raw, err = transport.ReadRaw()
	if err != nil {
		t.Fatalf("ReadRaw error: %v", err)
	}
	if string(raw) != "line2" {
		t.Errorf("expected 'line2', got %q", string(raw))
	}

	_, err = transport.ReadRaw()
	if err == nil {
		t.Error("expected error at EOF")
	}
}

// ---------------------------------------------------------------------------
// Transport: CloseWriter
// ---------------------------------------------------------------------------

func TestTransportCloseWriterNonCloser(t *testing.T) {
	var buf bytes.Buffer
	transport := NewTransport(strings.NewReader(""), &buf)
	if err := transport.CloseWriter(); err != nil {
		t.Errorf("CloseWriter on non-closer should return nil, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Transport: WriteErrorNilID
// ---------------------------------------------------------------------------

func TestTransportWriteErrorNilIDResponse(t *testing.T) {
	var buf bytes.Buffer
	transport := NewTransport(strings.NewReader(""), &buf)

	if err := transport.WriteErrorNilID(ErrCodeParseError, "parse error"); err != nil {
		t.Fatalf("WriteErrorNilID error: %v", err)
	}

	var resp JSONRPCResponse
	if err := json.Unmarshal(buf.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.ID != nil {
		t.Errorf("expected nil ID, got %v", resp.ID)
	}
	if resp.Error == nil || resp.Error.Code != ErrCodeParseError {
		t.Errorf("expected error code %d, got %+v", ErrCodeParseError, resp.Error)
	}
}

// ---------------------------------------------------------------------------
// Transport: DeliverResponse
// ---------------------------------------------------------------------------

func TestTransportDeliverResponseNilID(t *testing.T) {
	transport := NewTransport(strings.NewReader(""), io.Discard)
	// Should not panic with nil ID
	transport.DeliverResponse(&JSONRPCResponse{ID: nil})
}

func TestTransportDeliverResponseUnknownType(t *testing.T) {
	transport := NewTransport(strings.NewReader(""), io.Discard)
	// ID as string — not float64/int/int64 — should be ignored silently
	transport.DeliverResponse(&JSONRPCResponse{ID: "string-id"})
}

func TestTransportDeliverResponseInt64ID(t *testing.T) {
	transport := NewTransport(strings.NewReader(""), io.Discard)
	ch := make(chan *JSONRPCResponse, 1)
	transport.pendingMu.Lock()
	transport.pending[42] = ch
	transport.pendingMu.Unlock()

	transport.DeliverResponse(&JSONRPCResponse{ID: float64(42), Result: "ok"})

	select {
	case resp := <-ch:
		if resp.Result != "ok" {
			t.Errorf("expected 'ok', got %v", resp.Result)
		}
	default:
		t.Error("expected response to be delivered")
	}
}

// ---------------------------------------------------------------------------
// Transport: ReadMessage invalid JSON
// ---------------------------------------------------------------------------

func TestTransportReadMessageInvalidJSON(t *testing.T) {
	input := "not-json\n"
	transport := NewTransport(strings.NewReader(input), io.Discard)
	_, err := transport.ReadMessage()
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestTransportReadAnyMessageInvalidJSON(t *testing.T) {
	input := "not-json\n"
	transport := NewTransport(strings.NewReader(input), io.Discard)
	_, _, err := transport.ReadAnyMessage()
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

// ---------------------------------------------------------------------------
// Transport: SendRequest timeout
// ---------------------------------------------------------------------------

func TestTransportSendRequestTimeout(t *testing.T) {
	r, _ := io.Pipe()
	transport := NewTransport(r, io.Discard)
	defer r.Close()

	_, err := transport.SendRequest("test/method", nil, 50*time.Millisecond)
	if err == nil {
		t.Error("expected timeout error")
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Errorf("expected timeout error, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Transport: SendRequest with error response
// ---------------------------------------------------------------------------

// Transport: SendRequest with nil params (no params field)
func TestTransportSendRequestNilParams(t *testing.T) {
	var buf bytes.Buffer
	transport := NewTransport(strings.NewReader(""), &buf)

	// Send with nil params — should not include "params" field
	go func() {
		time.Sleep(10 * time.Millisecond)
		transport.DeliverResponse(&JSONRPCResponse{ID: float64(1), RawResult: json.RawMessage(`"ok"`)})
	}()

	result, err := transport.SendRequest("test/method", nil, 2*time.Second)
	if err != nil {
		t.Fatalf("SendRequest error: %v", err)
	}
	if string(result) != `"ok"` {
		t.Errorf("expected '\"ok\"', got %s", string(result))
	}

	// Verify no "params" in the written JSON
	var req map[string]json.RawMessage
	json.Unmarshal(buf.Bytes(), &req)
	if _, ok := req["params"]; ok {
		t.Error("expected no params field when params is nil")
	}
}

// ---------------------------------------------------------------------------
// Handler: handleInitialize
// ---------------------------------------------------------------------------

func TestHandlerInitializeComplete(t *testing.T) {
	var buf bytes.Buffer
	transport := NewTransport(strings.NewReader(""), &buf)
	h := NewHandler(&config.Config{}, tool.NewRegistry(), transport, nil)

	params := InitializeParams{
		ProtocolVersion: 1,
		ClientCapabilities: ClientCapabilities{
			Terminal: true,
			FS:       &FSCapability{ReadTextFile: true},
		},
		ClientInfo: &ImplementationInfo{Name: "test-client", Version: "1.0"},
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
	if initResult.ProtocolVersion != 1 {
		t.Errorf("expected protocol version 1, got %d", initResult.ProtocolVersion)
	}
	if initResult.AgentInfo.Name != "ggcode" {
		t.Errorf("expected agent name 'ggcode', got %q", initResult.AgentInfo.Name)
	}
	if len(initResult.AuthMethods) == 0 {
		t.Error("expected auth methods")
	}
	if !initResult.AgentCapabilities.LoadSession {
		t.Error("expected LoadSession capability")
	}
	if initResult.AgentCapabilities.PromptCapabilities == nil {
		t.Error("expected PromptCapabilities")
	}
	if initResult.AgentCapabilities.SessionCapabilities == nil {
		t.Error("expected SessionCapabilities")
	}
}

// ---------------------------------------------------------------------------
// Handler: handleSessionNew not initialized
// ---------------------------------------------------------------------------

func TestHandlerSessionNewNotInitialized(t *testing.T) {
	var buf bytes.Buffer
	transport := NewTransport(strings.NewReader(""), &buf)
	h := NewHandler(&config.Config{}, tool.NewRegistry(), transport, nil)

	params := SessionNewParams{CWD: "/tmp"}
	paramsJSON, _ := json.Marshal(params)
	_, err := h.handleSessionNew(paramsJSON)
	if err == nil {
		t.Error("expected error when not initialized")
	}
}

// ---------------------------------------------------------------------------
// Handler: handleAuthenticate unknown method
// ---------------------------------------------------------------------------

func TestHandlerAuthenticateUnknownMethod(t *testing.T) {
	var buf bytes.Buffer
	transport := NewTransport(strings.NewReader(""), &buf)
	h := NewHandler(&config.Config{}, tool.NewRegistry(), transport, nil)

	params := AuthenticateParams{AuthMethodID: "unknown_method"}
	paramsJSON, _ := json.Marshal(params)
	_, err := h.handleAuthenticate(paramsJSON)
	if err == nil {
		t.Error("expected error for unknown auth method")
	}
}

// ---------------------------------------------------------------------------
// Handler: dispatch with notification (nil ID)
// ---------------------------------------------------------------------------

func TestHandlerDispatchNotification(t *testing.T) {
	var buf bytes.Buffer
	transport := NewTransport(strings.NewReader(""), &buf)
	h := NewHandler(&config.Config{}, tool.NewRegistry(), transport, nil)

	called := false
	h.dispatch(&JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      nil,
		Method:  "test",
		Params:  json.RawMessage(`{}`),
	}, func(params json.RawMessage) (interface{}, error) {
		called = true
		return map[string]string{"ok": "true"}, nil
	})
	if !called {
		t.Error("expected handler to be called")
	}
	if buf.Len() != 0 {
		t.Errorf("expected no output for notification, got %d bytes", buf.Len())
	}
}

// ---------------------------------------------------------------------------
// Handler: handleSessionSetMode not initialized
// ---------------------------------------------------------------------------

func TestHandlerSessionSetModeNotInit(t *testing.T) {
	var buf bytes.Buffer
	transport := NewTransport(strings.NewReader(""), &buf)
	h := NewHandler(&config.Config{}, tool.NewRegistry(), transport, nil)

	params := SessionSetModeParams{SessionID: "x", Mode: "auto"}
	paramsJSON, _ := json.Marshal(params)
	_, err := h.handleSessionSetMode(paramsJSON)
	if err == nil {
		t.Error("expected error when not initialized")
	}
}

// ---------------------------------------------------------------------------
// Handler: handleSessionLoad not initialized
// ---------------------------------------------------------------------------

func TestHandlerSessionLoadNotInit(t *testing.T) {
	var buf bytes.Buffer
	transport := NewTransport(strings.NewReader(""), &buf)
	h := NewHandler(&config.Config{}, tool.NewRegistry(), transport, nil)

	params := SessionLoadParams{SessionID: "x"}
	paramsJSON, _ := json.Marshal(params)
	_, err := h.handleSessionLoad(paramsJSON)
	if err == nil {
		t.Error("expected error when not initialized")
	}
}

// ---------------------------------------------------------------------------
// Handler: handleSetConfigOption
// ---------------------------------------------------------------------------

func TestHandlerSetConfigOptionUpdatesValue(t *testing.T) {
	var buf bytes.Buffer
	transport := NewTransport(strings.NewReader(""), &buf)
	h := NewHandler(&config.Config{}, tool.NewRegistry(), transport, nil)
	h.initialized = true

	newParams := SessionNewParams{CWD: t.TempDir()}
	newParamsJSON, _ := json.Marshal(newParams)
	newResult, _ := h.handleSessionNew(newParamsJSON)
	sessionID := newResult.(SessionNewResult).SessionID

	params := SetSessionConfigOptionRequest{
		SessionID: sessionID,
		ConfigID:  "mode",
		Value:     "bypass",
	}
	paramsJSON, _ := json.Marshal(params)
	result, err := h.handleSetConfigOption(paramsJSON)
	if err != nil {
		t.Fatalf("handleSetConfigOption error: %v", err)
	}
	resp, ok := result.(SetSessionConfigOptionResponse)
	if !ok {
		t.Fatal("expected SetSessionConfigOptionResponse")
	}
	found := false
	for _, opt := range resp.ConfigOptions {
		if opt.ID == "mode" && opt.CurrentValue == "bypass" {
			found = true
		}
	}
	if !found {
		t.Error("expected mode to be updated to 'bypass'")
	}
}

// ---------------------------------------------------------------------------
// Handler: handleSessionCancel with active session
// ---------------------------------------------------------------------------

func TestHandlerSessionCancelActive(t *testing.T) {
	var buf bytes.Buffer
	transport := NewTransport(strings.NewReader(""), &buf)
	h := NewHandler(&config.Config{}, tool.NewRegistry(), transport, nil)
	h.initialized = true

	newParams := SessionNewParams{CWD: t.TempDir()}
	newParamsJSON, _ := json.Marshal(newParams)
	newResult, _ := h.handleSessionNew(newParamsJSON)
	sessionID := newResult.(SessionNewResult).SessionID

	cancelled := false
	h.sessionsMu.RLock()
	s := h.sessions[sessionID]
	h.sessionsMu.RUnlock()
	s.SetCancel(func() { cancelled = true })

	params := SessionCancelParams{SessionID: sessionID}
	paramsJSON, _ := json.Marshal(params)
	_, err := h.handleSessionCancel(paramsJSON)
	if err != nil {
		t.Fatalf("handleSessionCancel error: %v", err)
	}
	if !cancelled {
		t.Error("expected cancel function to be called")
	}
}

// ---------------------------------------------------------------------------
// Handler: handleRequest method not found
// ---------------------------------------------------------------------------

func TestHandlerHandleRequestMethodNotFound(t *testing.T) {
	var buf bytes.Buffer
	transport := NewTransport(strings.NewReader(""), &buf)
	h := NewHandler(&config.Config{}, tool.NewRegistry(), transport, nil)

	req := &JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      float64(1),
		Method:  "nonexistent/method",
		Params:  json.RawMessage(`{}`),
	}
	h.handleRequest(context.Background(), req)

	var resp JSONRPCResponse
	if err := json.Unmarshal(buf.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Error == nil || resp.Error.Code != ErrCodeMethodNotFound {
		t.Errorf("expected method not found error, got %+v", resp.Error)
	}
}

// ---------------------------------------------------------------------------
// Handler: cleanupEmptySessions
// ---------------------------------------------------------------------------

func TestHandlerCleanupEmptySessionsRemovesFiles(t *testing.T) {
	var buf bytes.Buffer
	transport := NewTransport(strings.NewReader(""), &buf)
	h := NewHandler(&config.Config{}, tool.NewRegistry(), transport, nil)
	h.initialized = true

	dir := t.TempDir()

	newParams := SessionNewParams{CWD: t.TempDir()}
	newParamsJSON, _ := json.Marshal(newParams)
	newResult, _ := h.handleSessionNew(newParamsJSON)
	sessionID := newResult.(SessionNewResult).SessionID

	h.sessionsMu.Lock()
	h.workspaceDirs[sessionID] = dir
	h.sessionsMu.Unlock()

	stalePath := filepath.Join(dir, sessionID+".json")
	os.WriteFile(stalePath, []byte(`{}`), 0o644)

	h.cleanupEmptySessions()

	if _, err := os.Stat(stalePath); !os.IsNotExist(err) {
		t.Error("expected empty session file to be cleaned up")
	}
}

// ---------------------------------------------------------------------------
// Handler: handleAuthenticate with api-key (env var check)
// ---------------------------------------------------------------------------

func TestHandlerAuthenticateAPIKeyMissingEnvVar(t *testing.T) {
	var buf bytes.Buffer
	transport := NewTransport(strings.NewReader(""), &buf)
	h := NewHandler(&config.Config{}, tool.NewRegistry(), transport, nil)

	os.Unsetenv("GGCODE_API_KEY")

	params := AuthenticateParams{AuthMethodID: "api-key"}
	paramsJSON, _ := json.Marshal(params)
	_, err := h.handleAuthenticate(paramsJSON)
	if err == nil {
		t.Error("expected error when GGCODE_API_KEY is not set")
	}
}

func TestHandlerAuthenticateAPIKeyEnvVarSet(t *testing.T) {
	var buf bytes.Buffer
	transport := NewTransport(strings.NewReader(""), &buf)
	h := NewHandler(&config.Config{}, tool.NewRegistry(), transport, nil)

	os.Setenv("GGCODE_API_KEY", "test-key-123")
	defer os.Unsetenv("GGCODE_API_KEY")

	params := AuthenticateParams{AuthMethodID: "api-key"}
	paramsJSON, _ := json.Marshal(params)
	_, err := h.handleAuthenticate(paramsJSON)
	if err != nil {
		t.Fatalf("expected no error with GGCODE_API_KEY set, got: %v", err)
	}
	if !h.authenticated {
		t.Error("expected handler to be authenticated")
	}
}

// ---------------------------------------------------------------------------
// Handler: getAuthMethods
// ---------------------------------------------------------------------------

func TestHandlerGetAuthMethodsReturnsExpected(t *testing.T) {
	var buf bytes.Buffer
	transport := NewTransport(strings.NewReader(""), &buf)
	h := NewHandler(&config.Config{}, tool.NewRegistry(), transport, nil)

	methods := h.getAuthMethods()
	if len(methods) < 2 {
		t.Fatalf("expected at least 2 auth methods, got %d", len(methods))
	}
	found := map[string]bool{}
	for _, m := range methods {
		found[m.ID] = true
	}
	if !found["agent"] {
		t.Error("expected 'agent' auth method")
	}
	if !found["api-key"] {
		t.Error("expected 'api-key' auth method")
	}
}

// ---------------------------------------------------------------------------
// AuthHandler: HandleEnvVarAuth
// ---------------------------------------------------------------------------

func TestAuthHandlerHandleEnvVarAuthRequiredMissing(t *testing.T) {
	ah := NewAuthHandler(nil, "session-1")
	os.Unsetenv("MY_REQUIRED_VAR")

	err := ah.HandleEnvVarAuth([]AuthEnvVar{
		{Name: "MY_REQUIRED_VAR"},
	})
	if err == nil {
		t.Error("expected error for missing required env var")
	}
}

func TestAuthHandlerHandleEnvVarAuthOptionalMissing(t *testing.T) {
	ah := NewAuthHandler(nil, "session-1")
	os.Unsetenv("MY_OPTIONAL_VAR")

	optional := true
	err := ah.HandleEnvVarAuth([]AuthEnvVar{
		{Name: "MY_OPTIONAL_VAR", Optional: &optional},
	})
	if err != nil {
		t.Errorf("expected no error for optional missing env var, got: %v", err)
	}
}

func TestAuthHandlerHandleEnvVarAuthPresent(t *testing.T) {
	ah := NewAuthHandler(nil, "session-1")
	os.Setenv("MY_PRESENT_VAR", "value")
	defer os.Unsetenv("MY_PRESENT_VAR")

	err := ah.HandleEnvVarAuth([]AuthEnvVar{
		{Name: "MY_PRESENT_VAR"},
	})
	if err != nil {
		t.Errorf("expected no error for present env var, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// MCP Bridge: acpMCPServerToConfig edge cases
// ---------------------------------------------------------------------------

func TestACPMCPServerToConfigDefaultType(t *testing.T) {
	srv := MCPServer{Name: "default-type", Command: "npx", Args: []string{"mcp-server"}}
	cfg := acpMCPServerToConfig(srv)
	if cfg.Type != "stdio" {
		t.Errorf("expected type 'stdio' when Command is set, got %q", cfg.Type)
	}
}

func TestACPMCPServerToConfigExplicitHTTP(t *testing.T) {
	srv := MCPServer{Name: "explicit", Type: "http", URL: "http://localhost/mcp"}
	cfg := acpMCPServerToConfig(srv)
	if cfg.Type != "http" {
		t.Errorf("expected type 'http', got %q", cfg.Type)
	}
}

func TestACPMCPServerToConfigNoCommandNoType(t *testing.T) {
	srv := MCPServer{Name: "empty"}
	cfg := acpMCPServerToConfig(srv)
	if cfg.Type != "" {
		t.Errorf("expected empty type, got %q", cfg.Type)
	}
}

// ---------------------------------------------------------------------------
// MCPManager: Close / ConnectServers
// ---------------------------------------------------------------------------

func TestMCPManagerCloseNoClients(t *testing.T) {
	mgr := NewMCPManager(nil)
	if err := mgr.Close(); err != nil {
		t.Errorf("Close with no clients should return nil, got %v", err)
	}
}

func TestMCPManagerConnectServersEmpty(t *testing.T) {
	mgr := NewMCPManager(nil)
	if err := mgr.ConnectServers(context.Background(), nil); err != nil {
		t.Errorf("ConnectServers with nil should return nil, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// JSON-RPC type serialization: notification with locations
// ---------------------------------------------------------------------------

func TestSessionNotificationWithLocations(t *testing.T) {
	line := 42
	update := SessionUpdate{
		Type:       "tool_call",
		ToolCallID: "tc-1",
		Locations: []ToolCallLocation{
			{Path: "/tmp/a.go", Line: &line},
		},
	}
	notif := SessionNotification{
		SessionID: "sess-1",
		Update:    update,
	}
	data, err := json.Marshal(notif)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded SessionNotification
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.SessionID != "sess-1" {
		t.Errorf("expected 'sess-1', got %q", decoded.SessionID)
	}
	if decoded.Update.ToolCallID != "tc-1" {
		t.Errorf("expected 'tc-1', got %q", decoded.Update.ToolCallID)
	}
	if len(decoded.Update.Locations) != 1 {
		t.Fatalf("expected 1 location, got %d", len(decoded.Update.Locations))
	}
	if decoded.Update.Locations[0].Path != "/tmp/a.go" {
		t.Errorf("location path mismatch")
	}
	if decoded.Update.Locations[0].Line == nil || *decoded.Update.Locations[0].Line != 42 {
		t.Errorf("location line mismatch")
	}
}

// ---------------------------------------------------------------------------
// PermissionOutcome JSON round-trip
// ---------------------------------------------------------------------------

func TestPermissionOutcomeRoundTrip(t *testing.T) {
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
		t.Errorf("expected 'selected', got %q", decoded.Outcome)
	}
	if decoded.SelectedOption == nil || decoded.SelectedOption.OptionID != "allow" {
		t.Errorf("selected option mismatch")
	}
	if string(data) != `{"outcome":"selected","optionId":"allow"}` {
		t.Fatalf("expected flat selected outcome JSON, got %s", string(data))
	}
}

// ---------------------------------------------------------------------------
// ContentBlock tool_result round-trip
// ---------------------------------------------------------------------------

func TestContentBlockToolResultRoundTrip(t *testing.T) {
	block := ContentBlock{
		Type:     "tool_result",
		ToolID:   "call-1",
		ToolName: "read_file",
		Output:   "file contents",
		IsError:  false,
	}
	data, err := json.Marshal(block)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded ContentBlock
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Type != "tool_result" {
		t.Errorf("expected 'tool_result', got %q", decoded.Type)
	}
	if decoded.IsError {
		t.Error("expected IsError=false")
	}
}

// ---------------------------------------------------------------------------
// EmbeddedResource JSON
// ---------------------------------------------------------------------------

func TestEmbeddedResourceRoundTrip(t *testing.T) {
	res := EmbeddedResource{
		Text: &TextResourceContents{
			URI:  "file:///tmp/test.go",
			Text: "package main",
		},
	}
	data, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded EmbeddedResource
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Text == nil || decoded.Text.URI != "file:///tmp/test.go" {
		t.Error("text resource mismatch")
	}
}

// ---------------------------------------------------------------------------
// NewSessionRequest JSON round-trip
// ---------------------------------------------------------------------------

func TestNewSessionRequestRoundTrip(t *testing.T) {
	req := NewSessionRequest{
		CWD: "/home/user/project",
		MCPServers: []MCPServer{
			{Name: "test", Command: "node", Args: []string{"s.js"}},
		},
	}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded NewSessionRequest
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.CWD != "/home/user/project" {
		t.Errorf("CWD mismatch")
	}
	if len(decoded.MCPServers) != 1 {
		t.Error("expected 1 MCP server")
	}
}

func TestNewSessionRequestMarshalIncludesEmptyMCPServers(t *testing.T) {
	req := NewSessionRequest{CWD: "/home/user/project"}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(data) != `{"cwd":"/home/user/project","mcpServers":[]}` {
		t.Fatalf("expected empty mcpServers array, got %s", data)
	}
}

// ---------------------------------------------------------------------------
// StopReason constants
// ---------------------------------------------------------------------------

func TestStopReasonValues(t *testing.T) {
	tests := []struct {
		val StopReason
		str string
	}{
		{StopReasonEndTurn, "end_turn"},
		{StopReasonMaxTokens, "max_tokens"},
		{StopReasonMaxTurns, "max_turns"},
		{StopReasonCancelled, "cancelled"},
		{StopReasonError, "error"},
		{StopReasonToolUse, "tool_use"},
	}
	for _, tt := range tests {
		if string(tt.val) != tt.str {
			t.Errorf("expected %q, got %q", tt.str, tt.val)
		}
	}
}

// ---------------------------------------------------------------------------
// ToolCallStatus constants
// ---------------------------------------------------------------------------

func TestToolCallStatusValues(t *testing.T) {
	if ToolCallStatusPending != "pending" {
		t.Errorf("ToolCallStatusPending = %q", ToolCallStatusPending)
	}
	if ToolCallStatusInProgress != "in_progress" {
		t.Errorf("ToolCallStatusInProgress = %q", ToolCallStatusInProgress)
	}
	if ToolCallStatusCompleted != "completed" {
		t.Errorf("ToolCallStatusCompleted = %q", ToolCallStatusCompleted)
	}
	if ToolCallStatusFailed != "failed" {
		t.Errorf("ToolCallStatusFailed = %q", ToolCallStatusFailed)
	}
}

// ---------------------------------------------------------------------------
// ToolKind constants
// ---------------------------------------------------------------------------

func TestToolKindConstantsValues(t *testing.T) {
	if ToolKindRead != "read" {
		t.Errorf("ToolKindRead = %q", ToolKindRead)
	}
	if ToolKindEdit != "edit" {
		t.Errorf("ToolKindEdit = %q", ToolKindEdit)
	}
	if ToolKindExecute != "execute" {
		t.Errorf("ToolKindExecute = %q", ToolKindExecute)
	}
	if ToolKindSearch != "search" {
		t.Errorf("ToolKindSearch = %q", ToolKindSearch)
	}
}

// ---------------------------------------------------------------------------
// Handler: loadSessionFromWorkspaces (nonexistent)
// ---------------------------------------------------------------------------

func TestHandlerLoadSessionFromWorkspacesNotFound(t *testing.T) {
	var buf bytes.Buffer
	transport := NewTransport(strings.NewReader(""), &buf)
	h := NewHandler(&config.Config{}, tool.NewRegistry(), transport, nil)

	_, err := h.loadSessionFromWorkspaces("nonexistent-id")
	if err == nil {
		t.Error("expected error for nonexistent session")
	}
}

// ---------------------------------------------------------------------------
// Handler: handleSessionLoad nonexistent
// ---------------------------------------------------------------------------

func TestHandlerSessionLoadNonexistentID(t *testing.T) {
	var buf bytes.Buffer
	transport := NewTransport(strings.NewReader(""), &buf)
	h := NewHandler(&config.Config{}, tool.NewRegistry(), transport, nil)
	h.initialized = true

	params := SessionLoadParams{SessionID: "does-not-exist"}
	paramsJSON, _ := json.Marshal(params)
	_, err := h.handleSessionLoad(paramsJSON)
	if err == nil {
		t.Error("expected error for nonexistent session")
	}
}

// ---------------------------------------------------------------------------
// Handler: handleSessionLoad with real session
// ---------------------------------------------------------------------------

func TestHandlerSessionLoadWithSavedSession(t *testing.T) {
	dir := t.TempDir()

	session := NewSession(t.TempDir(), nil)
	session.AddMessage("user", []ContentBlock{{Type: "text", Text: "hello"}})
	session.AddMessage("assistant", []ContentBlock{{Type: "text", Text: "world"}})
	session.Save(dir)

	var buf bytes.Buffer
	transport := NewTransport(strings.NewReader(""), &buf)
	cfg := &config.Config{}
	registry := tool.NewRegistry()
	h := NewHandler(cfg, registry, transport, nil)
	h.initialized = true
	h.sessionsDir = dir

	params := SessionLoadParams{SessionID: session.ID}
	paramsJSON, _ := json.Marshal(params)
	result, err := h.handleSessionLoad(paramsJSON)
	if err != nil {
		t.Fatalf("handleSessionLoad error: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil result after message replay, got %+v", result)
	}

	output := buf.String()
	if !strings.Contains(output, "hello") {
		t.Error("expected 'hello' in notification output")
	}
	if !strings.Contains(output, "world") {
		t.Error("expected 'world' in notification output")
	}
}

// ---------------------------------------------------------------------------
// Handler: handleSessionResume nonexistent
// ---------------------------------------------------------------------------

func TestHandlerSessionResumeNonexistentID(t *testing.T) {
	var buf bytes.Buffer
	transport := NewTransport(strings.NewReader(""), &buf)
	h := NewHandler(&config.Config{}, tool.NewRegistry(), transport, nil)

	params := ResumeSessionRequest{SessionID: "does-not-exist"}
	paramsJSON, _ := json.Marshal(params)
	_, err := h.handleSessionResume(paramsJSON)
	if err == nil {
		t.Error("expected error for nonexistent session")
	}
}

// ---------------------------------------------------------------------------
// Handler: handleSessionResume with saved session
// ---------------------------------------------------------------------------

func TestHandlerSessionResumeWithSavedSession(t *testing.T) {
	validCWD := t.TempDir()

	// Simulate the directory structure: baseDir/workspace-hash/session-id.json
	baseDir := t.TempDir()
	session := NewSession(validCWD, nil)
	// Need a message so Save actually writes the file
	session.AddMessage("user", []ContentBlock{{Type: "text", Text: "hello"}})
	sessionDir := workspaceSessionsDir(baseDir, validCWD)
	os.MkdirAll(sessionDir, 0o755)
	session.Save(sessionDir)

	var buf bytes.Buffer
	transport := NewTransport(strings.NewReader(""), &buf)
	h := NewHandler(&config.Config{}, tool.NewRegistry(), transport, nil)
	h.sessionsDir = baseDir

	params := ResumeSessionRequest{SessionID: session.ID}
	paramsJSON, _ := json.Marshal(params)
	result, err := h.handleSessionResume(paramsJSON)
	if err != nil {
		t.Fatalf("handleSessionResume error: %v", err)
	}
	resp, ok := result.(ResumeSessionResponse)
	if !ok {
		t.Fatal("expected ResumeSessionResponse")
	}
	if resp.Modes == nil || resp.Modes.Current != "auto" {
		t.Error("expected modes with current 'auto'")
	}
}

// ---------------------------------------------------------------------------
// Handler: connectMCPServers with empty list
// ---------------------------------------------------------------------------

func TestHandlerConnectMCPServersEmptyList(t *testing.T) {
	var buf bytes.Buffer
	transport := NewTransport(strings.NewReader(""), &buf)
	h := NewHandler(&config.Config{}, tool.NewRegistry(), transport, nil)

	session := NewSession("/tmp", nil)
	if err := h.connectMCPServers(context.Background(), session, nil); err != nil {
		t.Errorf("expected nil for empty servers, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Update type constants
// ---------------------------------------------------------------------------

func TestSessionUpdateTypeConstantValues(t *testing.T) {
	if UpdateAgentMessageChunk != "agent_message_chunk" {
		t.Errorf("UpdateAgentMessageChunk = %q", UpdateAgentMessageChunk)
	}
	if UpdateUserMessageChunk != "user_message_chunk" {
		t.Errorf("UpdateUserMessageChunk = %q", UpdateUserMessageChunk)
	}
	if UpdateToolCall != "tool_call" {
		t.Errorf("UpdateToolCall = %q", UpdateToolCall)
	}
	if UpdateToolCallUpdate != "tool_call_update" {
		t.Errorf("UpdateToolCallUpdate = %q", UpdateToolCallUpdate)
	}
	if UpdatePlan != "plan" {
		t.Errorf("UpdatePlan = %q", UpdatePlan)
	}
}

// ---------------------------------------------------------------------------
// Handler: session/prompt with uninitialized handler
// ---------------------------------------------------------------------------

func TestHandlerSessionPromptNotInit(t *testing.T) {
	var buf bytes.Buffer
	transport := NewTransport(strings.NewReader(""), &buf)
	h := NewHandler(&config.Config{}, tool.NewRegistry(), transport, nil)

	params := SessionPromptParams{SessionID: "x", Prompt: []ContentBlock{{Type: "text", Text: "hi"}}}
	paramsJSON, _ := json.Marshal(params)
	_, err := h.handleSessionPrompt(paramsJSON)
	if err == nil {
		t.Error("expected error when not initialized")
	}
}
