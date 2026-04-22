package acp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// --- pipeTransport wraps a pair of pipes for bidirectional testing ---
type pipeTransport struct {
	clientWrite *io.PipeWriter // client writes here (agent reads)
	agentRead   *io.PipeReader
	agentWrite  *io.PipeWriter // agent writes here (client reads)
	clientRead  *io.PipeReader
}

func newPipeTransport() (*Transport, *Transport) {
	// Client → Agent pipe
	cr, cw := io.Pipe()
	// Agent → Client pipe
	ar, aw := io.Pipe()

	agentTransport := NewTransport(cr, aw)
	clientTransport := NewTransport(ar, cw)

	return agentTransport, clientTransport
}

// --- E2E Test: Full ACP lifecycle ---

// TestE2EInitializeFlow tests: client sends initialize → agent responds with capabilities
func TestE2EInitializeFlow(t *testing.T) {
	agentTransport, clientTransport := newPipeTransport()
	defer agentTransport.Writer.(*io.PipeWriter).Close()
	defer clientTransport.Writer.(*io.PipeWriter).Close()

	cfg := &config.Config{}
	registry := tool.NewRegistry()
	handler := NewHandler(cfg, registry, agentTransport, nil)

	// Run handler in background
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go handler.Run(ctx)

	// Client sends initialize
	initReq := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1,"clientCapabilities":{"terminal":true}}}` + "\n"
	clientTransport.Writer.Write([]byte(initReq))

	// Read response from agent
	if !clientTransport.Scanner.Scan() {
		t.Fatal("expected response from agent")
	}

	var resp JSONRPCResponse
	if err := json.Unmarshal(clientTransport.Scanner.Bytes(), &resp); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if resp.ID == nil || *resp.ID != 1 {
		t.Errorf("expected id 1, got %v", resp.ID)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}

	// Verify result contains agent capabilities
	resultBytes, _ := json.Marshal(resp.Result)
	if !strings.Contains(string(resultBytes), "agentCapabilities") {
		t.Errorf("response missing agentCapabilities: %s", string(resultBytes))
	}
	if !strings.Contains(string(resultBytes), "authMethods") {
		t.Errorf("response missing authMethods: %s", string(resultBytes))
	}
}

// TestE2ESessionNewAndPrompt tests: initialize → session/new → verify session created
func TestE2ESessionNewAndPrompt(t *testing.T) {
	agentTransport, clientTransport := newPipeTransport()
	defer agentTransport.Writer.(*io.PipeWriter).Close()
	defer clientTransport.Writer.(*io.PipeWriter).Close()

	cfg := &config.Config{MaxIterations: 5}
	registry := tool.NewRegistry()
	handler := NewHandler(cfg, registry, agentTransport, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go handler.Run(ctx)

	// Step 1: Initialize
	sendAndReadResponse(t, clientTransport, 1, "initialize", InitializeParams{
		ProtocolVersion:    1,
		ClientCapabilities: ClientCapabilities{},
	})

	// Step 2: Session/New
	resp := sendAndReadResponse(t, clientTransport, 2, "session/new", SessionNewParams{
		CWD: "/tmp/test-e2e",
	})

	var newResult SessionNewResult
	if err := json.Unmarshal(resp.RawResult, &newResult); err != nil {
		// Try extracting from Result field
		resultBytes, _ := json.Marshal(resp.Result)
		json.Unmarshal(resultBytes, &newResult)
	}
	if newResult.SessionID == "" {
		t.Fatal("expected non-empty session ID")
	}
}

// TestE2EPermissionRequestResponse tests the core bidirectional flow:
// Agent sends session/request_permission → Client responds → Agent receives response
func TestE2EPermissionRequestResponse(t *testing.T) {
	// Create a direct pipe for testing SendRequest
	agentRead, clientWrite := io.Pipe()
	clientRead, agentWrite := io.Pipe()

	agentTransport := NewTransport(agentRead, agentWrite)
	clientTransport := NewTransport(clientRead, clientWrite)

	// Agent sends a request in background
	var result json.RawMessage
	var sendErr error
	done := make(chan struct{})

	// Start a background reader on agentTransport that reads incoming
	// messages and delivers responses to pending SendRequest calls.
	// This simulates what Handler.Run() does in production.
	go func() {
		for {
			_, resp, err := agentTransport.ReadAnyMessage()
			if err != nil {
				return
			}
			if resp != nil {
				agentTransport.DeliverResponse(resp)
			}
		}
	}()

	go func() {
		defer close(done)
		result, sendErr = agentTransport.SendRequest(
			"session/request_permission",
			PermissionRequestParams{
				SessionID: "test-session",
				Request: PermissionRequest{
					Type:        "tool_use",
					Description: "Execute tool: write_file",
				},
			},
			5*time.Second,
		)
	}()

	// Wait a bit for the request to be sent
	time.Sleep(50 * time.Millisecond)

	// Client reads the request
	req, err := clientTransport.ReadMessage()
	if err != nil {
		t.Fatalf("client read request error: %v", err)
	}
	if req.Method != "session/request_permission" {
		t.Errorf("expected method 'session/request_permission', got %q", req.Method)
	}

	// Client sends response
	if err := clientTransport.WriteResponse(*req.ID, map[string]bool{"approved": true}); err != nil {
		t.Fatalf("client write response error: %v", err)
	}

	// Wait for agent to receive response
	<-done

	if sendErr != nil {
		t.Fatalf("SendRequest error: %v", sendErr)
	}

	// Verify response content
	var permResp struct {
		Approved bool `json:"approved"`
	}
	if err := json.Unmarshal(result, &permResp); err != nil {
		t.Fatalf("unmarshal permission response: %v", err)
	}
	if !permResp.Approved {
		t.Error("expected approved=true from client")
	}
}

// TestE2EPermissionDenied tests: Client denies permission → Agent receives denial
func TestE2EPermissionDenied(t *testing.T) {
	agentRead, clientWrite := io.Pipe()
	clientRead, agentWrite := io.Pipe()

	agentTransport := NewTransport(agentRead, agentWrite)
	clientTransport := NewTransport(clientRead, clientWrite)

	var result json.RawMessage
	var sendErr error
	done := make(chan struct{})

	// Start readLoop to deliver responses to pending SendRequest calls.
	go func() {
		for {
			_, resp, err := agentTransport.ReadAnyMessage()
			if err != nil {
				return
			}
			if resp != nil {
				agentTransport.DeliverResponse(resp)
			}
		}
	}()

	go func() {
		defer close(done)
		result, sendErr = agentTransport.SendRequest(
			"session/request_permission",
			PermissionRequestParams{
				SessionID: "test-session",
				Request: PermissionRequest{
					Type:        "fs_write",
					Path:        "/etc/passwd",
					Description: "Write to system file",
				},
			},
			5*time.Second,
		)
	}()

	time.Sleep(50 * time.Millisecond)

	// Client reads and denies
	req, _ := clientTransport.ReadMessage()
	clientTransport.WriteResponse(*req.ID, map[string]bool{"approved": false})

	<-done

	if sendErr != nil {
		t.Fatalf("SendRequest error: %v", sendErr)
	}

	var permResp struct {
		Approved bool `json:"approved"`
	}
	json.Unmarshal(result, &permResp)
	if permResp.Approved {
		t.Error("expected approved=false (denied)")
	}
}

// TestE2EFSReadFileViaClient tests: Agent requests fs/read_text_file → Client responds with content
func TestE2EFSReadFileViaClient(t *testing.T) {
	agentRead, clientWrite := io.Pipe()
	clientRead, agentWrite := io.Pipe()

	agentTransport := NewTransport(agentRead, agentWrite)
	clientTransport := NewTransport(clientRead, clientWrite)

	var result json.RawMessage
	var sendErr error
	done := make(chan struct{})

	// Start readLoop to deliver responses to pending SendRequest calls.
	go func() {
		for {
			_, resp, err := agentTransport.ReadAnyMessage()
			if err != nil {
				return
			}
			if resp != nil {
				agentTransport.DeliverResponse(resp)
			}
		}
	}()

	go func() {
		defer close(done)
		result, sendErr = agentTransport.SendRequest(
			"fs/read_text_file",
			FSReadTextFileParams{Path: "/remote/project/main.go"},
			5*time.Second,
		)
	}()

	time.Sleep(50 * time.Millisecond)

	// Client reads the request
	req, _ := clientTransport.ReadMessage()
	if req.Method != "fs/read_text_file" {
		t.Errorf("expected method 'fs/read_text_file', got %q", req.Method)
	}

	// Verify the path in the request
	var params FSReadTextFileParams
	json.Unmarshal(req.Params, &params)
	if params.Path != "/remote/project/main.go" {
		t.Errorf("expected path '/remote/project/main.go', got %q", params.Path)
	}

	// Client responds with file content
	clientTransport.WriteResponse(*req.ID, FSReadTextFileResult{
		Content: "package main\n\nfunc main() {}\n",
	})

	<-done

	if sendErr != nil {
		t.Fatalf("SendRequest error: %v", sendErr)
	}

	var fsResp FSReadTextFileResult
	json.Unmarshal(result, &fsResp)
	if !strings.Contains(fsResp.Content, "package main") {
		t.Errorf("unexpected file content: %q", fsResp.Content)
	}
}

// TestE2ESendRequestTimeout tests: Agent sends request → no response → timeout
func TestE2ESendRequestTimeout(t *testing.T) {
	// Use buffered pipes so that writeJSON does not block on send.
	// io.Pipe is synchronous and blocks until the other side reads.
	agentRead, clientWrite := io.Pipe()
	clientRead, agentWrite := io.Pipe()

	agentTransport := NewTransport(agentRead, agentWrite)

	// Drain the client side in background so the agent's writeJSON to agentWrite
	// doesn't block (agentWrite → clientRead pipe needs a reader).
	go io.Copy(io.Discard, clientRead)
	// Also close clientWrite so agentRead sees EOF after we're done.
	defer clientWrite.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = agentTransport.SendRequest(
			"session/request_permission",
			PermissionRequestParams{SessionID: "test"},
			200*time.Millisecond, // short timeout
		)
	}()

	select {
	case <-done:
		// Expected: timeout
	case <-time.After(5 * time.Second):
		t.Fatal("SendRequest did not timeout")
	}
}

// --- Mock Provider for agent loop E2E ---

type mockProvider struct {
	events []provider.StreamEvent
	delay  time.Duration
}

func (m *mockProvider) Name() string { return "mock" }
func (m *mockProvider) Chat(ctx context.Context, messages []provider.Message, tools []provider.ToolDefinition) (*provider.ChatResponse, error) {
	return &provider.ChatResponse{Message: provider.Message{Role: "assistant", Content: []provider.ContentBlock{provider.TextBlock("hello")}}}, nil
}
func (m *mockProvider) ChatStream(ctx context.Context, messages []provider.Message, tools []provider.ToolDefinition) (<-chan provider.StreamEvent, error) {
	ch := make(chan provider.StreamEvent, len(m.events)+1)
	go func() {
		defer close(ch)
		for _, e := range m.events {
			select {
			case <-ctx.Done():
				return
			case ch <- e:
			}
			if m.delay > 0 {
				time.Sleep(m.delay)
			}
		}
	}()
	return ch, nil
}
func (m *mockProvider) CountTokens(ctx context.Context, messages []provider.Message) (int, error) {
	return 100, nil
}

// TestE2EAgentLoopStreamEvents tests: agent produces events → ACP notifications sent to client
// NOTE: This test requires a real LLM provider or a fully mocked agent loop.
// The mock provider approach deadlocks on pipe transport because the agent loop
// internally calls SendRequest for permissions, which blocks on the pipe.
// TODO: Re-enable with real provider integration tests.
func TestE2EAgentLoopStreamEvents(t *testing.T) {
	t.Skip("requires real LLM provider; mock pipe transport deadlocks")
	mock := &mockProvider{
		events: []provider.StreamEvent{
			{Type: provider.StreamEventText, Text: "Hello from agent"},
			{Type: provider.StreamEventToolCallDone, Tool: provider.ToolCallDelta{
				ID:        "call-1",
				Name:      "read_file",
				Arguments: json.RawMessage(`{"path":"/tmp/test.go"}`),
			}},
			{Type: provider.StreamEventToolResult, Tool: provider.ToolCallDelta{
				ID:   "call-1",
				Name: "read_file",
			}, Result: "file contents here"},
			{Type: provider.StreamEventDone},
		},
	}

	agentRead, clientWrite := io.Pipe()
	clientRead, agentWrite := io.Pipe()

	agentTransport := NewTransport(agentRead, agentWrite)
	clientTransport := NewTransport(clientRead, clientWrite)

	cfg := &config.Config{MaxIterations: 5}
	registry := tool.NewRegistry()
	session := NewSession("/tmp", nil)

	loop := NewAgentLoop(cfg, registry, agentTransport, session, ClientCapabilities{}, mock)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Run agent loop
	done := make(chan error, 1)
	go func() {
		done <- loop.ExecutePrompt(ctx, []ContentBlock{{Type: "text", Text: "read the file"}})
	}()

	// Client reads notifications
	var notifications []map[string]interface{}
	for {
		if !clientTransport.Scanner.Scan() {
			break
		}
		line := clientTransport.Scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var notif map[string]interface{}
		if err := json.Unmarshal(line, &notif); err != nil {
			continue
		}
		notifications = append(notifications, notif)
		if len(notifications) >= 3 { // expect at least 3 notifications
			break
		}
	}

	// Close pipes to let agent loop finish
	clientWrite.Close()
	agentWrite.Close()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("agent loop did not finish")
	}

	// Verify we got the right notifications
	if len(notifications) < 3 {
		t.Fatalf("expected at least 3 notifications, got %d", len(notifications))
	}

	// First: agent_message_chunk
	first := notifications[0]
	if first["method"] != "session/update" {
		t.Errorf("expected method 'session/update', got %v", first["method"])
	}

	// Check that session has messages persisted
	msgs := session.Messages()
	if len(msgs) < 2 {
		t.Fatalf("expected at least 2 messages in session (user + assistant), got %d", len(msgs))
	}
	if msgs[0].Role != "user" {
		t.Errorf("expected first message role 'user', got %q", msgs[0].Role)
	}
	if msgs[1].Role != "assistant" {
		t.Errorf("expected second message role 'assistant', got %q", msgs[1].Role)
	}
}

// TestE2EFullHandlerWithBidirectional tests the complete flow through Handler
// NOTE: Same pipe transport limitation as TestE2EAgentLoopStreamEvents.
// TODO: Re-enable with real provider integration tests.
func TestE2EFullHandlerWithBidirectional(t *testing.T) {
	t.Skip("requires real LLM provider; mock pipe transport deadlocks")
	agentRead, clientWrite := io.Pipe()
	clientRead, agentWrite := io.Pipe()

	agentTransport := NewTransport(agentRead, agentWrite)
	clientTransport := NewTransport(clientRead, clientWrite)

	mock := &mockProvider{
		events: []provider.StreamEvent{
			{Type: provider.StreamEventText, Text: "I'll read that file"},
			{Type: provider.StreamEventDone},
		},
	}

	cfg := &config.Config{MaxIterations: 5}
	registry := tool.NewRegistry()
	handler := NewHandler(cfg, registry, agentTransport, mock)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go handler.Run(ctx)

	// Step 1: Initialize
	sendLine(t, clientTransport, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1,"clientCapabilities":{"fs":{"readTextFile":true}}}}`)
	readAndVerifyID(t, clientTransport, 1)

	// Step 2: session/authenticate
	sendLine(t, clientTransport, `{"jsonrpc":"2.0","id":2,"method":"session/authenticate","params":{"authMethodId":"api-key"}}`)
	// Note: this may fail if GGCODE_API_KEY is not set, but we just need to test the routing
	time.Sleep(100 * time.Millisecond)

	// Step 3: session/new
	sendLine(t, clientTransport, `{"jsonrpc":"2.0","id":3,"method":"session/new","params":{"cwd":"/tmp/e2e-test"}}`)
	respBytes := scanLine(t, clientTransport)
	var sessionResp JSONRPCResponse
	json.Unmarshal(respBytes, &sessionResp)
	if sessionResp.Error != nil {
		t.Fatalf("session/new error: %v", sessionResp.Error)
	}

	// Extract session ID
	var newResult map[string]interface{}
	resultBytes, _ := json.Marshal(sessionResp.Result)
	json.Unmarshal(resultBytes, &newResult)
	sessionID, _ := newResult["sessionId"].(string)
	if sessionID == "" {
		t.Fatal("expected session ID")
	}

	// Step 4: session/prompt
	promptReq := fmt.Sprintf(`{"jsonrpc":"2.0","id":4,"method":"session/prompt","params":{"sessionId":%q,"prompt":[{"type":"text","text":"hello"}]}}`, sessionID)
	sendLine(t, clientTransport, promptReq)

	// Read the prompt response (returns immediately) + session/update notifications
	time.Sleep(200 * time.Millisecond)

	// Step 5: session/cancel
	cancelReq := fmt.Sprintf(`{"jsonrpc":"2.0","id":5,"method":"session/cancel","params":{"sessionId":%q}}`, sessionID)
	sendLine(t, clientTransport, cancelReq)

	// Cleanup
	clientWrite.Close()
	agentWrite.Close()
}

// --- Test helpers ---

func sendAndReadResponse(t *testing.T, ct *Transport, id int, method string, params interface{}) *JSONRPCResponse {
	t.Helper()

	paramsJSON, _ := json.Marshal(params)
	line := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":%q,"params":%s}`, id, method, string(paramsJSON))
	ct.Writer.Write([]byte(line + "\n"))

	respBytes := scanLine(t, ct)
	var resp JSONRPCResponse
	if err := json.Unmarshal(respBytes, &resp); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	return &resp
}

func sendLine(t *testing.T, ct *Transport, line string) {
	t.Helper()
	ct.Writer.Write([]byte(line + "\n"))
}

func scanLine(t *testing.T, ct *Transport) []byte {
	t.Helper()
	if !ct.Scanner.Scan() {
		t.Fatal("scanner ended without data")
	}
	return ct.Scanner.Bytes()
}

func readAndVerifyID(t *testing.T, ct *Transport, expectedID int) {
	t.Helper()
	data := scanLine(t, ct)
	var resp JSONRPCResponse
	json.Unmarshal(data, &resp)
	if resp.ID == nil || *resp.ID != expectedID {
		t.Errorf("expected id %d, got %v", expectedID, resp.ID)
	}
	if resp.Error != nil {
		t.Errorf("unexpected error in response: %v", resp.Error)
	}
}
