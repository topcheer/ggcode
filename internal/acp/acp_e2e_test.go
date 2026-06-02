//go:build integration_local

package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

const (
	acpTimeoutHelperEnv    = "GGCODE_TEST_ACP_TIMEOUT_HELPER"
	acpTimeoutStderrMarker = "GGCODE_TEST_ACP_TIMEOUT_STDERR_MARKER"
	acpStreamHelperEnv     = "GGCODE_TEST_ACP_STREAM_HELPER"
	acpStreamReadmeEnv     = "GGCODE_TEST_ACP_STREAM_README"
	acpPromptResponseEnv   = "GGCODE_TEST_ACP_PROMPT_RESPONSE_HELPER"
	acpIncompleteHelperEnv = "GGCODE_TEST_ACP_INCOMPLETE_HELPER"
	acpPermissionHelperEnv = "GGCODE_TEST_ACP_PERMISSION_HELPER"
	acpRealCopilotEnv      = "GGCODE_RUN_REAL_COPILOT_ACP_E2E"
	acpRealCopilotCWD      = "GGCODE_REAL_COPILOT_ACP_E2E_CWD"
	acpRealCopilotAsk      = "GGCODE_REAL_COPILOT_ACP_E2E_PROMPT"
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
	if resp.ID == nil || resp.ID != float64(1) {
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
		CWD: "/tmp",
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
			RequestPermissionRequest{
				SessionID: "test-session",
				ToolCall: &ToolCallUpdate{
					Title: "Execute tool: write_file",
					Kind:  ToolKindExecute,
				},
				Options: []PermissionOption{
					{OptionID: "allow", Name: "Allow", Kind: PermissionOptionAllowOnce},
					{OptionID: "reject", Name: "Reject", Kind: PermissionOptionRejectOnce},
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
	if err := clientTransport.WriteResponse(req.ID, map[string]interface{}{"outcome": map[string]interface{}{"outcome": "selected", "optionId": "allow"}}); err != nil {
		t.Fatalf("client write response error: %v", err)
	}

	// Wait for agent to receive response
	<-done

	if sendErr != nil {
		t.Fatalf("SendRequest error: %v", sendErr)
	}

	// Verify response content
	var permResp RequestPermissionResponse
	if err := json.Unmarshal(result, &permResp); err != nil {
		t.Fatalf("unmarshal permission response: %v", err)
	}
	if permResp.Outcome.Outcome != "selected" || permResp.Outcome.SelectedOption == nil || permResp.Outcome.SelectedOption.OptionID != "allow" {
		t.Errorf("expected outcome=selected/allow, got %+v", permResp.Outcome)
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
			RequestPermissionRequest{
				SessionID: "test-session",
				ToolCall: &ToolCallUpdate{
					Title: "Write to system file",
					Kind:  ToolKindEdit,
				},
				Options: []PermissionOption{
					{OptionID: "allow", Name: "Allow", Kind: PermissionOptionAllowOnce},
					{OptionID: "reject", Name: "Reject", Kind: PermissionOptionRejectOnce},
				},
			},
			5*time.Second,
		)
	}()

	time.Sleep(50 * time.Millisecond)

	// Client reads and denies
	req, _ := clientTransport.ReadMessage()
	clientTransport.WriteResponse(req.ID, map[string]interface{}{"outcome": map[string]interface{}{"outcome": "cancelled"}})

	<-done

	if sendErr != nil {
		t.Fatalf("SendRequest error: %v", sendErr)
	}

	var permResp RequestPermissionResponse
	json.Unmarshal(result, &permResp)
	if permResp.Outcome.Outcome != "cancelled" {
		t.Error("expected outcome=cancelled (denied)")
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
	clientTransport.WriteResponse(req.ID, FSReadTextFileResult{
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
	mu        sync.Mutex
	events    []provider.StreamEvent
	sequences [][]provider.StreamEvent
	nextSeq   int
	delay     time.Duration
}

func (m *mockProvider) Name() string { return "mock" }
func (m *mockProvider) Chat(ctx context.Context, messages []provider.Message, tools []provider.ToolDefinition) (*provider.ChatResponse, error) {
	return &provider.ChatResponse{Message: provider.Message{Role: "assistant", Content: []provider.ContentBlock{provider.TextBlock("hello")}}}, nil
}
func (m *mockProvider) ChatStream(ctx context.Context, messages []provider.Message, tools []provider.ToolDefinition) (<-chan provider.StreamEvent, error) {
	events := m.events
	m.mu.Lock()
	if len(m.sequences) > 0 {
		idx := m.nextSeq
		if idx >= len(m.sequences) {
			idx = len(m.sequences) - 1
		}
		events = m.sequences[idx]
		m.nextSeq++
	}
	m.mu.Unlock()

	ch := make(chan provider.StreamEvent, len(events)+1)
	go func() {
		defer close(ch)
		for _, e := range events {
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

func TestACPTimeoutHelperProcess(t *testing.T) {
	if os.Getenv(acpTimeoutHelperEnv) != "1" {
		t.Skip("helper process only")
	}

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	encoder := json.NewEncoder(os.Stdout)

	for scanner.Scan() {
		var req JSONRPCRequest
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			fmt.Fprintf(os.Stderr, "helper parse error: %v\n", err)
			continue
		}

		switch req.Method {
		case "initialize":
			if err := encoder.Encode(JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: InitializeResponse{
					ProtocolVersion: ProtocolVersion,
					AgentCapabilities: AgentCapabilities{
						LoadSession: true,
					},
					AgentInfo: ImplementationInfo{
						Name:    "timeout-helper",
						Version: "1.0",
					},
				},
			}); err != nil {
				t.Fatalf("write initialize response: %v", err)
			}
		case "session/new":
			if err := encoder.Encode(JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: NewSessionResponse{
					SessionID: "session-timeout-helper",
				},
			}); err != nil {
				t.Fatalf("write session/new response: %v", err)
			}
		case "session/prompt":
			fmt.Fprintln(os.Stderr, acpTimeoutStderrMarker)
			// Intentionally do not respond so the client-side timeout path fires.
		case "session/close":
			if err := encoder.Encode(JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result:  CloseSessionResponse{},
			}); err != nil {
				t.Fatalf("write session/close response: %v", err)
			}
		default:
			if req.ID != nil {
				if err := encoder.Encode(JSONRPCResponse{
					JSONRPC: "2.0",
					ID:      req.ID,
					Result:  map[string]any{},
				}); err != nil {
					t.Fatalf("write fallback response: %v", err)
				}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		t.Fatalf("helper scanner error: %v", err)
	}
}

func TestACPStreamHelperProcess(t *testing.T) {
	if os.Getenv(acpStreamHelperEnv) != "1" {
		t.Skip("helper process only")
	}

	readmePath := os.Getenv(acpStreamReadmeEnv)
	if readmePath == "" {
		t.Fatal("missing stream helper README path")
	}

	transport := NewTransport(os.Stdin, os.Stdout)
	cfg := &config.Config{MaxIterations: 5}
	registry := tool.NewRegistry()
	if err := tool.RegisterBuiltinTools(registry, nil, filepath.Dir(readmePath)); err != nil {
		t.Fatalf("register builtin tools: %v", err)
	}
	handler := NewHandler(cfg, registry, transport, &mockProvider{
		sequences: [][]provider.StreamEvent{
			{
				{Type: provider.StreamEventText, Text: "Hello "},
				{Type: provider.StreamEventText, Text: "from ACP"},
				{Type: provider.StreamEventToolCallDone, Tool: provider.ToolCallDelta{
					ID:        "call-1",
					Name:      "read_file",
					Arguments: json.RawMessage(fmt.Sprintf(`{"path":%q}`, readmePath)),
				}},
				{Type: provider.StreamEventDone},
			},
			{
				{Type: provider.StreamEventText, Text: "\nDone."},
				{Type: provider.StreamEventDone},
			},
		},
		delay: 10 * time.Millisecond,
	})

	if err := handler.Run(context.Background()); err != nil {
		t.Fatalf("stream helper handler error: %v", err)
	}
}

func TestACPIncompletePromptHelperProcess(t *testing.T) {
	if os.Getenv(acpIncompleteHelperEnv) != "1" {
		t.Skip("helper process only")
	}

	transport := NewTransport(os.Stdin, os.Stdout)
	for {
		req, err := transport.ReadMessage()
		if err == io.EOF {
			return
		}
		if err != nil {
			t.Fatalf("incomplete helper read error: %v", err)
		}
		if req == nil {
			continue
		}

		switch req.Method {
		case "initialize":
			if err := transport.WriteResponse(req.ID, InitializeResponse{
				ProtocolVersion: ProtocolVersion,
				AgentCapabilities: AgentCapabilities{
					LoadSession: false,
				},
				AgentInfo: ImplementationInfo{
					Name:    "incomplete-helper",
					Version: "1.0",
				},
			}); err != nil {
				t.Fatalf("write initialize response: %v", err)
			}
		case "session/new":
			if err := transport.WriteResponse(req.ID, NewSessionResponse{
				SessionID: "session-incomplete-helper",
			}); err != nil {
				t.Fatalf("write session/new response: %v", err)
			}
		case "session/prompt":
			if err := transport.WriteResponse(req.ID, PromptResponse{}); err != nil {
				t.Fatalf("write session/prompt response: %v", err)
			}
			for i := 1; i <= 5; i++ {
				rawInput := json.RawMessage(fmt.Sprintf(`{"command":"step-%d"}`, i))
				if err := transport.WriteNotification("session/update", SessionNotification{
					SessionID: "session-incomplete-helper",
					Update: SessionUpdate{
						Type:       UpdateToolCall,
						ToolCallID: fmt.Sprintf("call-%d", i),
						Title:      "Run",
						Kind:       ToolKindExecute,
						RawInput:   rawInput,
					},
				}); err != nil {
					t.Fatalf("write tool call notification: %v", err)
				}

				status := ToolCallStatusCompleted
				rawOutput := json.RawMessage(fmt.Sprintf(`"ok-%d"`, i))
				if i == 5 {
					status = ToolCallStatusFailed
					rawOutput = json.RawMessage(`"exit status 1"`)
				}
				if err := transport.WriteNotification("session/update", SessionNotification{
					SessionID: "session-incomplete-helper",
					Update: SessionUpdate{
						Type:       UpdateToolCallUpdate,
						ToolCallID: fmt.Sprintf("call-%d", i),
						Title:      "Run",
						Kind:       ToolKindExecute,
						Status:     status,
						RawInput:   rawInput,
						RawOutput:  rawOutput,
					},
				}); err != nil {
					t.Fatalf("write tool update notification: %v", err)
				}
			}
			// Intentionally omit session/prompt_complete to reproduce a hung agent.
		case "session/cancel":
			// Accept cancel notification and keep waiting for session/close.
		case "session/close":
			if err := transport.WriteResponse(req.ID, CloseSessionResponse{}); err != nil {
				t.Fatalf("write session/close response: %v", err)
			}
			return
		default:
			if req.ID != nil {
				if err := transport.WriteResponse(req.ID, map[string]any{}); err != nil {
					t.Fatalf("write fallback response: %v", err)
				}
			}
		}
	}
}

func TestACPPermissionStallHelperProcess(t *testing.T) {
	if os.Getenv(acpPermissionHelperEnv) != "1" {
		t.Skip("helper process only")
	}

	transport := NewTransport(os.Stdin, os.Stdout)
	for {
		req, resp, err := transport.ReadAnyMessage()
		if err == io.EOF {
			return
		}
		if err != nil {
			t.Fatalf("permission helper read error: %v", err)
		}
		if resp != nil {
			transport.DeliverResponse(resp)
			continue
		}
		if req == nil {
			continue
		}

		switch req.Method {
		case "initialize":
			if err := transport.WriteResponse(req.ID, InitializeResponse{
				ProtocolVersion: ProtocolVersion,
				AgentCapabilities: AgentCapabilities{
					LoadSession: false,
				},
				AgentInfo: ImplementationInfo{
					Name:    "permission-helper",
					Version: "1.0",
				},
			}); err != nil {
				t.Fatalf("write initialize response: %v", err)
			}
		case "session/new":
			if err := transport.WriteResponse(req.ID, NewSessionResponse{
				SessionID: "session-permission-helper",
			}); err != nil {
				t.Fatalf("write session/new response: %v", err)
			}
		case "session/prompt":
			if err := transport.WriteResponse(req.ID, PromptResponse{}); err != nil {
				t.Fatalf("write session/prompt response: %v", err)
			}
			go func() {
				_, _ = transport.SendRequest("session/request_permission", RequestPermissionRequest{
					SessionID: "session-permission-helper",
					ToolCall: &ToolCallUpdate{
						ToolCallID: "perm-1",
						Title:      "Execute tool: write_file",
						Kind:       ToolKindExecute,
						RawInput:   json.RawMessage(`{"path":"blocked.txt","content":"secret"}`),
					},
					Options: []PermissionOption{
						{OptionID: "allow", Name: "Allow", Kind: PermissionOptionAllowOnce},
						{OptionID: "reject", Name: "Reject", Kind: PermissionOptionRejectOnce},
					},
				}, 30*time.Second)
			}()
		case "session/cancel":
			// Ignore; the parent test expects the client to time out before completing.
		case "session/close":
			if err := transport.WriteResponse(req.ID, CloseSessionResponse{}); err != nil {
				t.Fatalf("write session/close response: %v", err)
			}
			return
		default:
			if req.ID != nil {
				if err := transport.WriteResponse(req.ID, map[string]any{}); err != nil {
					t.Fatalf("write fallback response: %v", err)
				}
			}
		}
	}
}

func TestACPPromptResponseHelperProcess(t *testing.T) {
	if os.Getenv(acpPromptResponseEnv) != "1" {
		t.Skip("helper process only")
	}

	transport := NewTransport(os.Stdin, os.Stdout)
	for {
		req, resp, err := transport.ReadAnyMessage()
		if err == io.EOF {
			return
		}
		if err != nil {
			t.Fatalf("prompt-response helper read error: %v", err)
		}
		if resp != nil {
			transport.DeliverResponse(resp)
			continue
		}
		if req == nil {
			continue
		}

		switch req.Method {
		case "initialize":
			if err := transport.WriteResponse(req.ID, InitializeResponse{
				ProtocolVersion: ProtocolVersion,
				AgentCapabilities: AgentCapabilities{
					LoadSession: false,
				},
				AgentInfo: ImplementationInfo{
					Name:    "prompt-response-helper",
					Version: "1.0",
				},
			}); err != nil {
				t.Fatalf("write initialize response: %v", err)
			}
		case "session/new":
			if err := transport.WriteResponse(req.ID, NewSessionResponse{
				SessionID: "session-prompt-response-helper",
			}); err != nil {
				t.Fatalf("write session/new response: %v", err)
			}
		case "session/prompt":
			for _, chunk := range []string{"Hello ", "from ", "Copilot-style ACP"} {
				if err := transport.WriteNotification("session/update", SessionNotification{
					SessionID: "session-prompt-response-helper",
					Update: SessionUpdate{
						Type:    UpdateAgentMessageChunk,
						Content: ContentBlock{Type: "text", Text: chunk},
					},
				}); err != nil {
					t.Fatalf("write text chunk notification: %v", err)
				}
				time.Sleep(50 * time.Millisecond)
			}
			if err := transport.WriteResponse(req.ID, PromptResponse{
				StopReason: StopReasonEndTurn,
			}); err != nil {
				t.Fatalf("write delayed session/prompt response: %v", err)
			}
			// Intentionally omit session/prompt_complete to mimic Copilot ACP behavior.
		case "session/cancel":
			// Ignore cancel; the parent test controls lifecycle.
		case "session/close":
			if err := transport.WriteResponse(req.ID, CloseSessionResponse{}); err != nil {
				t.Fatalf("write session/close response: %v", err)
			}
			return
		default:
			if req.ID != nil {
				if err := transport.WriteResponse(req.ID, map[string]any{}); err != nil {
					t.Fatalf("write fallback response: %v", err)
				}
			}
		}
	}
}

func TestE2EClientPromptTimeoutCapturesStderrWithoutTerminalLeak(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve test executable: %v", err)
	}
	t.Setenv(acpTimeoutHelperEnv, "1")

	stderrCapturePath := filepath.Join(t.TempDir(), "stderr-capture.txt")
	stderrCapture, err := os.Create(stderrCapturePath)
	if err != nil {
		t.Fatalf("create stderr capture file: %v", err)
	}
	oldStderr := os.Stderr
	os.Stderr = stderrCapture
	defer func() {
		os.Stderr = oldStderr
		stderrCapture.Close()
	}()

	client := NewClient(
		DiscoveredAgent{
			Def: AgentDef{
				Name:       "timeout-helper",
				ACPCommand: []string{"-test.run=TestACPTimeoutHelperProcess", "--"},
			},
			Path: exe,
		},
		t.TempDir(),
		nil,
		nil,
	)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Start(ctx); err != nil {
		t.Fatalf("Start error: %v", err)
	}
	defer client.Close()
	if err := client.NewSession(ctx, t.TempDir()); err != nil {
		t.Fatalf("NewSession error: %v", err)
	}

	_, err = client.sendRequest("session/prompt", PromptRequest{
		SessionID: client.sessionID,
		Prompt:    []ContentBlock{{Type: "text", Text: "please hang"}},
	}, 200*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "timeout waiting for client response to session/prompt") {
		t.Fatalf("expected prompt timeout error, got %v", err)
	}
	if !strings.Contains(err.Error(), acpTimeoutStderrMarker) {
		t.Fatalf("expected recent stderr in error, got %v", err)
	}

	time.Sleep(50 * time.Millisecond)
	if err := stderrCapture.Sync(); err != nil {
		t.Fatalf("sync stderr capture: %v", err)
	}
	captured, err := os.ReadFile(stderrCapturePath)
	if err != nil {
		t.Fatalf("read stderr capture: %v", err)
	}
	if strings.Contains(string(captured), acpTimeoutStderrMarker) {
		t.Fatalf("expected child stderr to stay out of parent stderr, got %q", string(captured))
	}
}

func TestE2EClientPromptStreamReceivesHelperSessionUpdates(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve test executable: %v", err)
	}
	t.Setenv(acpStreamHelperEnv, "1")
	workspaceDir := t.TempDir()
	readmePath := filepath.Join(workspaceDir, "README.md")
	if err := os.WriteFile(readmePath, []byte("README contents\n"), 0o600); err != nil {
		t.Fatalf("write README: %v", err)
	}
	t.Setenv(acpStreamReadmeEnv, readmePath)

	client := NewClient(
		DiscoveredAgent{
			Def: AgentDef{
				Name:       "stream-helper",
				ACPCommand: []string{"-test.run=TestACPStreamHelperProcess", "--"},
			},
			Path: exe,
		},
		workspaceDir,
		nil,
		nil,
	)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Start(ctx); err != nil {
		t.Fatalf("Start error: %v", err)
	}
	defer client.Close()
	if err := client.NewSession(ctx, workspaceDir); err != nil {
		t.Fatalf("NewSession error: %v", err)
	}

	var events []tool.ACPPromptEvent
	result, err := client.PromptStream(ctx, "read README.md", func(event tool.ACPPromptEvent) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatalf("PromptStream error: %v", err)
	}

	if result == nil {
		t.Fatal("expected prompt result")
	}
	if result.Text != "Hello from ACP\nDone." {
		t.Fatalf("expected concatenated streamed text, got %q", result.Text)
	}
	if result.StopReason != string(StopReasonEndTurn) {
		t.Fatalf("expected stop reason %q, got %q", StopReasonEndTurn, result.StopReason)
	}
	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call summary, got %d", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Name != "call-1" {
		t.Fatalf("expected tool summary name call-1, got %+v", result.ToolCalls[0])
	}
	if result.ToolCalls[0].Status != string(ToolCallStatusCompleted) {
		t.Fatalf("expected completed tool status, got %+v", result.ToolCalls[0])
	}
	if !strings.Contains(result.ToolCalls[0].Title, "README.md") {
		t.Fatalf("expected tool summary title to mention README.md, got %+v", result.ToolCalls[0])
	}

	if len(events) != 5 {
		t.Fatalf("expected 5 streamed events, got %d: %+v", len(events), events)
	}
	if events[0].Type != tool.ACPPromptEventText || events[0].Text != "Hello " {
		t.Fatalf("unexpected first event: %+v", events[0])
	}
	if events[1].Type != tool.ACPPromptEventText || events[1].Text != "from ACP" {
		t.Fatalf("unexpected second event: %+v", events[1])
	}
	if events[2].Type != tool.ACPPromptEventToolCall {
		t.Fatalf("expected tool call event, got %+v", events[2])
	}
	if events[2].ToolID != "call-1" || events[2].ToolName != "read_file" || events[2].ToolArgs != fmt.Sprintf(`{"path":%q}`, readmePath) {
		t.Fatalf("unexpected tool call event: %+v", events[2])
	}
	if events[3].Type != tool.ACPPromptEventToolResult {
		t.Fatalf("expected tool result event, got %+v", events[3])
	}
	if events[3].ToolID != "call-1" || events[3].IsError || strings.TrimSpace(events[3].Result) == "" {
		t.Fatalf("unexpected tool result event: %+v", events[3])
	}
	if events[4].Type != tool.ACPPromptEventText || events[4].Text != "\nDone." {
		t.Fatalf("unexpected final text event: %+v", events[4])
	}
}

func TestE2EClientPromptStreamTimesOutWhenAgentNeverCompletes(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve test executable: %v", err)
	}
	t.Setenv(acpIncompleteHelperEnv, "1")

	client := NewClient(
		DiscoveredAgent{
			Def: AgentDef{
				Name:       "incomplete-helper",
				ACPCommand: []string{"-test.run=TestACPIncompletePromptHelperProcess", "--"},
			},
			Path: exe,
		},
		t.TempDir(),
		nil,
		nil,
	)
	client.promptIdleTime = 200 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := client.Start(ctx); err != nil {
		t.Fatalf("Start error: %v", err)
	}
	defer client.Close()
	if err := client.NewSession(ctx, t.TempDir()); err != nil {
		t.Fatalf("NewSession error: %v", err)
	}

	var events []tool.ACPPromptEvent
	_, err = client.PromptStream(ctx, "complex hang case", func(event tool.ACPPromptEvent) {
		events = append(events, event)
	})
	if err == nil {
		t.Fatal("expected prompt completion timeout")
	}
	if !strings.Contains(err.Error(), "timeout waiting for agent prompt completion after 200ms") {
		t.Fatalf("expected prompt completion timeout, got %v", err)
	}
	if !strings.Contains(err.Error(), "Recent ACP activity:\nsent session/prompt prompt_len=17") {
		t.Fatalf("expected prompt activity trail in timeout error, got %v", err)
	}
	if !strings.Contains(err.Error(), "session/update tool_call_update id=call-5 title=Run status=failed result=exit status 1") {
		t.Fatalf("expected failed final tool update in timeout error, got %v", err)
	}
	if len(events) != 10 {
		t.Fatalf("expected 10 tool events before timeout, got %d: %+v", len(events), events)
	}
	last := events[len(events)-1]
	if last.Type != tool.ACPPromptEventToolResult || !last.IsError || last.ToolID != "call-5" {
		t.Fatalf("expected failed final tool result before timeout, got %+v", last)
	}
}

func TestE2EClientPromptStreamCompletesFromSessionPromptResponseWithoutPromptComplete(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve test executable: %v", err)
	}
	t.Setenv(acpPromptResponseEnv, "1")

	client := NewClient(
		DiscoveredAgent{
			Def: AgentDef{
				Name:       "prompt-response-helper",
				ACPCommand: []string{"-test.run=TestACPPromptResponseHelperProcess", "--"},
			},
			Path: exe,
		},
		t.TempDir(),
		nil,
		nil,
	)
	client.promptIdleTime = 200 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Start(ctx); err != nil {
		t.Fatalf("Start error: %v", err)
	}
	defer client.Close()
	if err := client.NewSession(ctx, t.TempDir()); err != nil {
		t.Fatalf("NewSession error: %v", err)
	}

	var events []tool.ACPPromptEvent
	result, err := client.PromptStream(ctx, "stream and finish via response", func(event tool.ACPPromptEvent) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatalf("PromptStream error: %v", err)
	}
	if result == nil {
		t.Fatal("expected prompt result")
	}
	if result.Text != "Hello from Copilot-style ACP" {
		t.Fatalf("unexpected prompt text: %q", result.Text)
	}
	if result.StopReason != string(StopReasonEndTurn) {
		t.Fatalf("expected stop reason %q, got %q", StopReasonEndTurn, result.StopReason)
	}
	if len(events) != 3 {
		t.Fatalf("expected 3 streamed text events, got %d: %+v", len(events), events)
	}
	for i, expected := range []string{"Hello ", "from ", "Copilot-style ACP"} {
		if events[i].Type != tool.ACPPromptEventText || events[i].Text != expected {
			t.Fatalf("unexpected event %d: %+v", i, events[i])
		}
	}
}

func TestE2ERealCopilotPromptStreamCompletes(t *testing.T) {
	if os.Getenv(acpRealCopilotEnv) != "1" {
		t.Skip("set GGCODE_RUN_REAL_COPILOT_ACP_E2E=1 to run against local copilot --acp")
	}

	copilotPath, err := exec.LookPath("copilot")
	if err != nil {
		t.Skipf("copilot CLI not available: %v", err)
	}

	repoRoot, err := filepath.Abs(filepath.Join(".", "..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	if cwd := strings.TrimSpace(os.Getenv(acpRealCopilotCWD)); cwd != "" {
		repoRoot = cwd
	}
	prompt := "Run `git --no-pager status --short` in the current directory and answer in one short sentence."
	if override := strings.TrimSpace(os.Getenv(acpRealCopilotAsk)); override != "" {
		prompt = override
	}

	policy := permission.NewConfigPolicyWithMode(map[string]permission.Decision{}, nil, permission.BypassMode)
	client := NewClient(
		DiscoveredAgent{
			Def: AgentDef{
				Name:       "copilot",
				ACPCommand: []string{"--acp", "--stdio"},
			},
			Path: copilotPath,
		},
		repoRoot,
		policy,
		nil,
	)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if err := client.Start(ctx); err != nil {
		t.Fatalf("Start error: %v", err)
	}
	t.Cleanup(func() {
		done := make(chan struct{})
		go func() {
			client.Close()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Log("client.Close still blocked after 5s; leaving subprocess cleanup to context/test shutdown")
		}
	})
	if err := client.NewSession(ctx, repoRoot); err != nil {
		t.Fatalf("NewSession error: %v", err)
	}

	t.Logf("starting real Copilot prompt cwd=%s prompt=%q", repoRoot, prompt)
	var events []tool.ACPPromptEvent
	result, err := client.PromptStream(ctx, prompt, func(ev tool.ACPPromptEvent) {
		events = append(events, ev)
		t.Logf("event: type=%s tool_id=%s tool_name=%s title=%s text_len=%d result_len=%d error=%v", ev.Type, ev.ToolID, ev.ToolName, ev.ToolTitle, len(ev.Text), len(ev.Result), ev.IsError)
	})
	t.Logf("real Copilot prompt returned: err=%v result=%+v", err, result)
	if err != nil {
		t.Logf("captured %d events before error", len(events))
		t.Fatalf("PromptStream error: %v", err)
	}
	if result == nil {
		t.Fatal("expected prompt result")
	}
	if result.StopReason == "" {
		t.Fatalf("expected non-empty stop reason, got result=%+v", result)
	}
	if strings.TrimSpace(result.Text) == "" {
		t.Fatalf("expected non-empty text result, got %+v", result)
	}
}

func TestE2EClientPromptTimeoutShowsPermissionStallActivity(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve test executable: %v", err)
	}
	t.Setenv(acpPermissionHelperEnv, "1")

	client := NewClient(
		DiscoveredAgent{
			Def: AgentDef{
				Name:       "permission-helper",
				ACPCommand: []string{"-test.run=TestACPPermissionStallHelperProcess", "--"},
			},
			Path: exe,
		},
		t.TempDir(),
		nil,
		nil,
	)
	client.promptIdleTime = 200 * time.Millisecond
	client.SetPermissionHandler(func(ctx context.Context, req RequestPermissionRequest) (RequestPermissionResponse, error) {
		<-ctx.Done()
		return RequestPermissionResponse{}, ctx.Err()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := client.Start(ctx); err != nil {
		t.Fatalf("Start error: %v", err)
	}
	defer client.Close()
	if err := client.NewSession(ctx, t.TempDir()); err != nil {
		t.Fatalf("NewSession error: %v", err)
	}

	_, err = client.PromptStream(ctx, "stall on permission", nil)
	if err == nil {
		t.Fatal("expected prompt completion timeout")
	}
	if !strings.Contains(err.Error(), "timeout waiting for agent prompt completion after 200ms") {
		t.Fatalf("expected prompt completion timeout, got %v", err)
	}
	if !strings.Contains(err.Error(), "recv session/request_permission title=Execute tool: write_file kind=execute options=2") {
		t.Fatalf("expected permission request activity in timeout error, got %v", err)
	}
	if strings.Contains(err.Error(), "sent session/request_permission") {
		t.Fatalf("expected stalled permission request without response activity, got %v", err)
	}
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
	if resp.ID == nil || resp.ID != float64(expectedID) {
		t.Errorf("expected id %d, got %v", expectedID, resp.ID)
	}
	if resp.Error != nil {
		t.Errorf("unexpected error in response: %v", resp.Error)
	}
}
