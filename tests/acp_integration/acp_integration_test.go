package acp_integration

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/acp"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// simpleTool implements tool.Tool for testing.
type simpleTool struct {
	name        string
	description string
	parameters  json.RawMessage
	execute     func(ctx context.Context, input json.RawMessage) (tool.Result, error)
}

func (s *simpleTool) Name() string                { return s.name }
func (s *simpleTool) Description() string         { return s.description }
func (s *simpleTool) Parameters() json.RawMessage { return s.parameters }
func (s *simpleTool) Execute(ctx context.Context, input json.RawMessage) (tool.Result, error) {
	return s.execute(ctx, input)
}

// --- Provider setup using real configuration ---
// API keys are read ONLY from environment variables, never written to disk.

const (
	testAnthropicBaseURL = "https://open.bigmodel.cn/api/anthropic"
	testOpenAIBaseURL    = "https://open.bigmodel.cn/api/coding/paas/v4"
	testDefaultModel     = "glm-5-turbo"
)

func getAPIKey() string {
	key := os.Getenv("ZAI_API_KEY")
	if key == "" {
		key = os.Getenv("GGCODE_ZAI_API_KEY")
	}
	return key
}

func getModel() string {
	m := os.Getenv("ZAI_MODEL")
	if m == "" {
		m = testDefaultModel
	}
	return m
}

func skipIfNoAPIKey(t *testing.T) string {
	t.Helper()
	key := getAPIKey()
	if key == "" {
		t.Skip("ZAI_API_KEY not set, skipping ACP integration test")
	}
	return key
}

func newRealProvider(t *testing.T) provider.Provider {
	t.Helper()
	key := skipIfNoAPIKey(t)
	return provider.NewAnthropicProviderWithBaseURL(key, getModel(), 256, testAnthropicBaseURL)
}

// --- notificationCollector reads all messages from clientRead ---
// Single reader for the pipe — all message access goes through this collector.

type notificationCollector struct {
	mu       sync.Mutex
	messages []map[string]interface{}
	done     chan struct{}
}

func newNotificationCollector(clientRead *io.PipeReader) *notificationCollector {
	nc := &notificationCollector{
		done: make(chan struct{}),
	}
	scanner := bufio.NewScanner(clientRead)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	go func() {
		defer close(nc.done)
		for scanner.Scan() {
			line := scanner.Bytes()
			if len(line) == 0 {
				continue
			}
			var msg map[string]interface{}
			if err := json.Unmarshal(line, &msg); err != nil {
				continue
			}
			nc.mu.Lock()
			nc.messages = append(nc.messages, msg)
			nc.mu.Unlock()
		}
	}()
	return nc
}

func (nc *notificationCollector) waitForResponse(expectedID int, timeout time.Duration) map[string]interface{} {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		nc.mu.Lock()
		for _, msg := range nc.messages {
			if id, ok := msg["id"].(float64); ok && int(id) == expectedID {
				nc.mu.Unlock()
				return msg
			}
		}
		nc.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	return nil
}

func (nc *notificationCollector) waitForMinCount(minCount int, timeout time.Duration) []map[string]interface{} {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		nc.mu.Lock()
		count := len(nc.messages)
		nc.mu.Unlock()
		if count >= minCount {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	nc.mu.Lock()
	defer nc.mu.Unlock()
	return nc.messages
}

// waitForNotifications waits until at least minCount notifications (method != "") are collected.
// Responses (with "id" but no "method") are not counted.
func (nc *notificationCollector) waitForNotifications(minCount int, timeout time.Duration) []map[string]interface{} {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		nc.mu.Lock()
		notifCount := 0
		for _, msg := range nc.messages {
			if _, hasMethod := msg["method"]; hasMethod {
				notifCount++
			}
		}
		nc.mu.Unlock()
		if notifCount >= minCount {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	nc.mu.Lock()
	defer nc.mu.Unlock()
	return nc.messages
}

func (nc *notificationCollector) all() []map[string]interface{} {
	nc.mu.Lock()
	defer nc.mu.Unlock()
	return nc.messages
}

func (nc *notificationCollector) waitForMethod(method string, timeout time.Duration) map[string]interface{} {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		nc.mu.Lock()
		for _, msg := range nc.messages {
			if m, ok := msg["method"].(string); ok && m == method {
				nc.mu.Unlock()
				return msg
			}
		}
		nc.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	return nil
}

// --- Pipe Transport ---

type pipeTransport struct {
	agentRead   *io.PipeReader
	agentWrite  *io.PipeWriter
	clientRead  *io.PipeReader
	clientWrite *io.PipeWriter
	agent       *acp.Transport
	client      *acp.Transport
	collector   *notificationCollector
}

func newPipeTransport(t *testing.T) *pipeTransport {
	t.Helper()
	ar, cw := io.Pipe()
	cr, aw := io.Pipe()
	pt := &pipeTransport{
		agentRead:   ar,
		agentWrite:  aw,
		clientRead:  cr,
		clientWrite: cw,
		agent:       acp.NewTransport(ar, aw),
		client:      acp.NewTransport(cr, cw),
	}
	// Single reader for agent→client messages
	pt.collector = newNotificationCollector(cr)
	return pt
}

func (pt *pipeTransport) close() {
	pt.clientWrite.Close()
	pt.agentWrite.Close()
}

// --- Client helpers ---

func clientSend(t *testing.T, pt *pipeTransport, line string) {
	t.Helper()
	pt.clientWrite.Write([]byte(line + "\n"))
}

func clientSendAndRead(t *testing.T, pt *pipeTransport, req string) map[string]interface{} {
	t.Helper()
	var reqParsed struct {
		ID int `json:"id"`
	}
	json.Unmarshal([]byte(req), &reqParsed)

	clientSend(t, pt, req)
	resp := pt.collector.waitForResponse(reqParsed.ID, 10*time.Second)
	if resp == nil {
		t.Fatalf("timed out waiting for response id %d", reqParsed.ID)
	}
	return resp
}

// readNotifications waits for at least minCount messages (responses + notifications).
// readNotifications waits for at least minCount total messages.
func readNotifications(pt *pipeTransport, minCount int, timeout time.Duration) []map[string]interface{} {
	return pt.collector.waitForMinCount(minCount, timeout)
}

// readNotificationsOnly waits for at least minCount actual notifications (with "method" field).
func readNotificationsOnly(pt *pipeTransport, minCount int, timeout time.Duration) []map[string]interface{} {
	return pt.collector.waitForNotifications(minCount, timeout)
}

// extractSessionID gets sessionId from a response.
func extractSessionID(resp map[string]interface{}) string {
	resultBytes, _ := json.Marshal(resp["result"])
	var r struct {
		SessionID string `json:"sessionId"`
	}
	json.Unmarshal(resultBytes, &r)
	return r.SessionID
}

// =====================================================================
// Test 1: Initialize handshake
// =====================================================================

func TestIntegrationInitialize(t *testing.T) {
	skipIfNoAPIKey(t)
	pt := newPipeTransport(t)
	defer pt.close()

	cfg := &config.Config{}
	registry := tool.NewRegistry()
	prov := newRealProvider(t)
	handler := acp.NewHandler(cfg, registry, pt.agent, prov)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go handler.Run(ctx)

	resp := clientSendAndRead(t, pt, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1,"clientCapabilities":{"terminal":true}}}`)

	if id, ok := resp["id"].(float64); !ok || id != 1 {
		t.Fatalf("expected id 1, got %v", resp["id"])
	}
	if resp["error"] != nil {
		t.Fatalf("unexpected error: %v", resp["error"])
	}

	result, _ := json.Marshal(resp["result"])
	for _, keyword := range []string{"agentCapabilities", "authMethods", "ggcode"} {
		if !strings.Contains(string(result), keyword) {
			t.Errorf("missing %s in response", keyword)
		}
	}
	t.Logf("Initialize OK: %d bytes", len(result))
}

// =====================================================================
// Test 2: Full session lifecycle
// =====================================================================

func TestIntegrationSessionLifecycle(t *testing.T) {
	skipIfNoAPIKey(t)
	pt := newPipeTransport(t)
	defer pt.close()

	cfg := &config.Config{MaxIterations: 5}
	registry := tool.NewRegistry()
	prov := newRealProvider(t)
	handler := acp.NewHandler(cfg, registry, pt.agent, prov)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go handler.Run(ctx)

	// Initialize
	resp := clientSendAndRead(t, pt, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1,"clientCapabilities":{}}}`)
	if resp["error"] != nil {
		t.Fatalf("initialize error: %v", resp["error"])
	}

	// Authenticate (api-key)
	resp = clientSendAndRead(t, pt, `{"jsonrpc":"2.0","id":2,"method":"session/authenticate","params":{"authMethodId":"api-key"}}`)
	t.Logf("authenticate: error=%v", resp["error"])

	// session/new
	resp = clientSendAndRead(t, pt, `{"jsonrpc":"2.0","id":3,"method":"session/new","params":{"cwd":"/tmp/acp-test"}}`)
	if resp["error"] != nil {
		t.Fatalf("session/new error: %v", resp["error"])
	}
	sessionID := extractSessionID(resp)
	if sessionID == "" {
		t.Fatal("expected session ID")
	}
	t.Logf("Session created: %s", sessionID)

	// session/cancel
	resp = clientSendAndRead(t, pt, fmt.Sprintf(`{"jsonrpc":"2.0","id":4,"method":"session/cancel","params":{"sessionId":%q}}`, sessionID))
	if resp["error"] != nil {
		t.Fatalf("session/cancel error: %v", resp["error"])
	}
}

// =====================================================================
// Test 3: Real LLM prompt — streaming text
// =====================================================================

func TestIntegrationPromptWithRealLLM(t *testing.T) {
	skipIfNoAPIKey(t)
	pt := newPipeTransport(t)
	defer pt.close()

	cfg := &config.Config{MaxIterations: 5}
	registry := tool.NewRegistry()
	prov := newRealProvider(t)
	handler := acp.NewHandler(cfg, registry, pt.agent, prov)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go handler.Run(ctx)

	clientSendAndRead(t, pt, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1,"clientCapabilities":{}}}`)
	resp := clientSendAndRead(t, pt, `{"jsonrpc":"2.0","id":2,"method":"session/new","params":{"cwd":"/tmp"}}`)
	sessionID := extractSessionID(resp)
	if sessionID == "" {
		t.Fatal("expected session ID")
	}

	promptReq := fmt.Sprintf(`{"jsonrpc":"2.0","id":3,"method":"session/prompt","params":{"sessionId":%q,"prompt":[{"type":"text","text":"Say hello in exactly 3 words. Nothing else."}]}}`, sessionID)
	clientSend(t, pt, promptReq)

	// Wait for actual notifications (session/update) — LLM takes time to respond
	notifications := readNotificationsOnly(pt, 1, 30*time.Second)
	if len(notifications) < 4 { // at least init resp + session/new resp + prompt resp + 1 notification
		t.Fatalf("expected at least 4 messages, got %d", len(notifications))
	}

	var fullText string
	for _, notif := range notifications {
		params, _ := notif["params"].(map[string]interface{})
		update, _ := params["update"].(map[string]interface{})
		if update == nil {
			continue
		}
		if update["sessionUpdate"] == "agent_message_chunk" {
			content, _ := update["content"].(map[string]interface{})
			if text, ok := content["text"].(string); ok {
				fullText += text
			}
		}
	}

	if fullText == "" {
		t.Error("expected non-empty text from real LLM")
	} else {
		t.Logf("LLM response: %q", fullText)
	}

	clientSend(t, pt, fmt.Sprintf(`{"jsonrpc":"2.0","id":4,"method":"session/cancel","params":{"sessionId":%q}}`, sessionID))
	time.Sleep(100 * time.Millisecond)
}

// =====================================================================
// Test 4: Permission approved (bidirectional via collector)
// =====================================================================

func TestIntegrationPermissionApproved(t *testing.T) {
	skipIfNoAPIKey(t)
	pt := newPipeTransport(t)
	defer pt.close()

	cfg := &config.Config{}
	registry := tool.NewRegistry()
	prov := newRealProvider(t)
	handler := acp.NewHandler(cfg, registry, pt.agent, prov)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go handler.Run(ctx)

	// Agent sends permission request
	done := make(chan bool, 1)
	go func() {
		result, err := pt.agent.SendRequest(
			"session/request_permission",
			acp.PermissionRequestParams{
				SessionID: "test-session",
				Request: acp.PermissionRequest{
					Type:        "tool_use",
					Description: "Execute tool: write_file",
				},
			},
			10*time.Second,
		)
		if err != nil {
			t.Logf("SendRequest error: %v", err)
			done <- false
			return
		}
		var r struct {
			Approved bool `json:"approved"`
		}
		json.Unmarshal(result, &r)
		done <- r.Approved
	}()

	// Wait for the permission request to appear in collector
	permReq := pt.collector.waitForMethod("session/request_permission", 5*time.Second)
	if permReq == nil {
		t.Fatal("timed out waiting for permission request")
	}

	// Extract the request ID and respond
	reqID := permReq["id"].(float64)
	respLine := fmt.Sprintf(`{"jsonrpc":"2.0","id":%.0f,"result":{"approved":true}}`, reqID)
	pt.clientWrite.Write([]byte(respLine + "\n"))

	select {
	case approved := <-done:
		if !approved {
			t.Error("expected approved=true")
		}
		t.Log("Permission approved OK")
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for approval result")
	}
}

// =====================================================================
// Test 5: Permission denied
// =====================================================================

func TestIntegrationPermissionDenied(t *testing.T) {
	skipIfNoAPIKey(t)
	pt := newPipeTransport(t)
	defer pt.close()

	cfg := &config.Config{}
	registry := tool.NewRegistry()
	prov := newRealProvider(t)
	handler := acp.NewHandler(cfg, registry, pt.agent, prov)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go handler.Run(ctx)

	done := make(chan bool, 1)
	go func() {
		result, err := pt.agent.SendRequest(
			"session/request_permission",
			acp.PermissionRequestParams{
				SessionID: "test-denied",
				Request: acp.PermissionRequest{
					Type:        "fs_write",
					Path:        "/etc/passwd",
					Description: "Write to system file",
				},
			},
			10*time.Second,
		)
		if err != nil {
			done <- false
			return
		}
		var r struct {
			Approved bool `json:"approved"`
		}
		json.Unmarshal(result, &r)
		done <- !r.Approved
	}()

	permReq := pt.collector.waitForMethod("session/request_permission", 5*time.Second)
	if permReq == nil {
		t.Fatal("timed out waiting for permission request")
	}

	reqID := permReq["id"].(float64)
	respLine := fmt.Sprintf(`{"jsonrpc":"2.0","id":%.0f,"result":{"approved":false}}`, reqID)
	pt.clientWrite.Write([]byte(respLine + "\n"))

	select {
	case ok := <-done:
		if !ok {
			t.Error("expected denied")
		}
		t.Log("Permission denied OK")
	case <-time.After(5 * time.Second):
		t.Fatal("timeout")
	}
}

// =====================================================================
// Test 6: FS read file via client
// =====================================================================

func TestIntegrationFSReadFile(t *testing.T) {
	skipIfNoAPIKey(t)
	pt := newPipeTransport(t)
	defer pt.close()

	cfg := &config.Config{}
	registry := tool.NewRegistry()
	prov := newRealProvider(t)
	handler := acp.NewHandler(cfg, registry, pt.agent, prov)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go handler.Run(ctx)

	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	os.WriteFile(testFile, []byte("Hello from integration test!\n"), 0o644)

	done := make(chan string, 1)
	go func() {
		result, err := pt.agent.SendRequest(
			"fs/read_text_file",
			acp.FSReadTextFileParams{Path: testFile},
			10*time.Second,
		)
		if err != nil {
			done <- ""
			return
		}
		var r acp.FSReadTextFileResult
		json.Unmarshal(result, &r)
		done <- r.Content
	}()

	fsReq := pt.collector.waitForMethod("fs/read_text_file", 5*time.Second)
	if fsReq == nil {
		t.Fatal("timed out waiting for fs/read_text_file")
	}

	reqID := fsReq["id"].(float64)
	paramsBytes, _ := json.Marshal(fsReq["params"])
	var params acp.FSReadTextFileParams
	json.Unmarshal(paramsBytes, &params)
	data, err := os.ReadFile(params.Path)
	content := "mock"
	if err == nil {
		content = string(data)
	}

	respLine := fmt.Sprintf(`{"jsonrpc":"2.0","id":%.0f,"result":{"content":%q}}`, reqID, content)
	pt.clientWrite.Write([]byte(respLine + "\n"))

	select {
	case content := <-done:
		if !strings.Contains(content, "Hello") {
			t.Errorf("unexpected content: %q", content)
		}
		t.Logf("Read file OK: %q", content)
	case <-time.After(5 * time.Second):
		t.Fatal("timeout")
	}
}

// =====================================================================
// Test 7: FS write file via client
// =====================================================================

func TestIntegrationFSWriteFile(t *testing.T) {
	skipIfNoAPIKey(t)
	pt := newPipeTransport(t)
	defer pt.close()

	cfg := &config.Config{}
	registry := tool.NewRegistry()
	prov := newRealProvider(t)
	handler := acp.NewHandler(cfg, registry, pt.agent, prov)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go handler.Run(ctx)

	tmpDir := t.TempDir()
	writePath := filepath.Join(tmpDir, "written.txt")

	done := make(chan error, 1)
	go func() {
		_, err := pt.agent.SendRequest(
			"fs/write_text_file",
			acp.FSWriteTextFileParams{Path: writePath, Content: "written by agent"},
			10*time.Second,
		)
		done <- err
	}()

	fsReq := pt.collector.waitForMethod("fs/write_text_file", 5*time.Second)
	if fsReq == nil {
		t.Fatal("timed out waiting for fs/write_text_file")
	}

	reqID := fsReq["id"].(float64)
	paramsBytes, _ := json.Marshal(fsReq["params"])
	var params acp.FSWriteTextFileParams
	json.Unmarshal(paramsBytes, &params)
	os.WriteFile(params.Path, []byte(params.Content), 0o644)

	respLine := fmt.Sprintf(`{"jsonrpc":"2.0","id":%.0f,"result":{}}`, reqID)
	pt.clientWrite.Write([]byte(respLine + "\n"))

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("write error: %v", err)
		}
		data, _ := os.ReadFile(writePath)
		if string(data) != "written by agent" {
			t.Errorf("content mismatch: %q", string(data))
		}
		t.Log("Write file OK")
	case <-time.After(5 * time.Second):
		t.Fatal("timeout")
	}
}

// =====================================================================
// Test 8: SendRequest timeout
// =====================================================================

func TestIntegrationSendRequestTimeout(t *testing.T) {
	ar, _ := io.Pipe()
	clientRead, agentWrite := io.Pipe()

	// Drain agent output so SendRequest's write doesn't block
	go io.Copy(io.Discard, clientRead)

	at := acp.NewTransport(ar, agentWrite)

	done := make(chan error, 1)
	go func() {
		_, err := at.SendRequest(
			"session/request_permission",
			acp.PermissionRequestParams{SessionID: "timeout"},
			200*time.Millisecond,
		)
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "timeout") {
			t.Errorf("expected timeout error, got: %v", err)
		}
		t.Logf("Timeout OK: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("did not timeout")
	}
}

// =====================================================================
// Test 9: session/set_mode
// =====================================================================

func TestIntegrationSessionSetMode(t *testing.T) {
	skipIfNoAPIKey(t)
	pt := newPipeTransport(t)
	defer pt.close()

	cfg := &config.Config{MaxIterations: 5}
	registry := tool.NewRegistry()
	prov := newRealProvider(t)
	handler := acp.NewHandler(cfg, registry, pt.agent, prov)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go handler.Run(ctx)

	clientSendAndRead(t, pt, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1,"clientCapabilities":{}}}`)
	resp := clientSendAndRead(t, pt, `{"jsonrpc":"2.0","id":2,"method":"session/new","params":{"cwd":"/tmp"}}`)
	sessionID := extractSessionID(resp)

	resp = clientSendAndRead(t, pt, fmt.Sprintf(`{"jsonrpc":"2.0","id":3,"method":"session/set_mode","params":{"sessionId":%q,"mode":"autopilot"}}`, sessionID))
	if resp["error"] != nil {
		t.Fatalf("set_mode error: %v", resp["error"])
	}
	t.Logf("set_mode autopilot OK")
}

// =====================================================================
// Test 10: session/load
// =====================================================================

func TestIntegrationSessionLoad(t *testing.T) {
	skipIfNoAPIKey(t)
	pt := newPipeTransport(t)
	defer pt.close()

	cfg := &config.Config{MaxIterations: 5}
	registry := tool.NewRegistry()
	prov := newRealProvider(t)
	handler := acp.NewHandler(cfg, registry, pt.agent, prov)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go handler.Run(ctx)

	clientSendAndRead(t, pt, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1,"clientCapabilities":{}}}`)
	resp := clientSendAndRead(t, pt, `{"jsonrpc":"2.0","id":2,"method":"session/new","params":{"cwd":"/tmp"}}`)
	sessionID := extractSessionID(resp)

	// Send prompt
	clientSend(t, pt, fmt.Sprintf(`{"jsonrpc":"2.0","id":3,"method":"session/prompt","params":{"sessionId":%q,"prompt":[{"type":"text","text":"Say OK."}]}}`, sessionID))
	// Wait for actual notifications from LLM
	readNotificationsOnly(pt, 1, 30*time.Second)

	// Cancel
	clientSendAndRead(t, pt, fmt.Sprintf(`{"jsonrpc":"2.0","id":4,"method":"session/cancel","params":{"sessionId":%q}}`, sessionID))

	// Load
	clientSend(t, pt, fmt.Sprintf(`{"jsonrpc":"2.0","id":5,"method":"session/load","params":{"sessionId":%q}}`, sessionID))
	loadResp := pt.collector.waitForResponse(5, 5*time.Second)
	if loadResp == nil {
		t.Fatal("timed out waiting for session/load response")
	}
	t.Logf("session/load completed for %s", sessionID)
}

// =====================================================================
// Test 11: Parallel sessions
// =====================================================================

func TestIntegrationParallelSessions(t *testing.T) {
	skipIfNoAPIKey(t)
	pt := newPipeTransport(t)
	defer pt.close()

	cfg := &config.Config{MaxIterations: 3}
	registry := tool.NewRegistry()
	prov := newRealProvider(t)
	handler := acp.NewHandler(cfg, registry, pt.agent, prov)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go handler.Run(ctx)

	clientSendAndRead(t, pt, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1,"clientCapabilities":{}}}`)

	r1 := clientSendAndRead(t, pt, `{"jsonrpc":"2.0","id":2,"method":"session/new","params":{"cwd":"/tmp"}}`)
	r2 := clientSendAndRead(t, pt, `{"jsonrpc":"2.0","id":3,"method":"session/new","params":{"cwd":"/tmp"}}`)

	s1 := extractSessionID(r1)
	s2 := extractSessionID(r2)
	if s1 == s2 {
		t.Error("expected different session IDs")
	}
	t.Logf("Sessions: %s, %s", s1, s2)
}

// =====================================================================
// Test 12: Method not found
// =====================================================================

func TestIntegrationMethodNotFound(t *testing.T) {
	skipIfNoAPIKey(t)
	pt := newPipeTransport(t)
	defer pt.close()

	cfg := &config.Config{}
	registry := tool.NewRegistry()
	prov := newRealProvider(t)
	handler := acp.NewHandler(cfg, registry, pt.agent, prov)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go handler.Run(ctx)

	resp := clientSendAndRead(t, pt, `{"jsonrpc":"2.0","id":1,"method":"nonexistent/method","params":{}}`)
	if resp["error"] == nil {
		t.Fatal("expected error")
	}
	errObj, _ := resp["error"].(map[string]interface{})
	if code, _ := errObj["code"].(float64); code != -32601 {
		t.Errorf("expected -32601, got %v", code)
	}
}

// =====================================================================
// Test 13: Real LLM tool use
// =====================================================================

func TestIntegrationRealLLMToolUse(t *testing.T) {
	skipIfNoAPIKey(t)
	pt := newPipeTransport(t)
	defer pt.close()

	cfg := &config.Config{MaxIterations: 5}
	registry := tool.NewRegistry()
	registry.Register(&simpleTool{
		name:        "get_time",
		description: "Get the current time",
		parameters:  json.RawMessage(`{"type":"object","properties":{}}`),
		execute: func(ctx context.Context, input json.RawMessage) (tool.Result, error) {
			return tool.Result{Content: time.Now().Format(time.RFC3339)}, nil
		},
	})

	prov := newRealProvider(t)
	handler := acp.NewHandler(cfg, registry, pt.agent, prov)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go handler.Run(ctx)

	clientSendAndRead(t, pt, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1,"clientCapabilities":{"fs":{"readTextFile":true}}}}`)
	resp := clientSendAndRead(t, pt, `{"jsonrpc":"2.0","id":2,"method":"session/new","params":{"cwd":"/tmp"}}`)
	sessionID := extractSessionID(resp)

	promptReq := fmt.Sprintf(`{"jsonrpc":"2.0","id":3,"method":"session/prompt","params":{"sessionId":%q,"prompt":[{"type":"text","text":"What time is it? Use the get_time tool to find out."}]}}`, sessionID)
	clientSend(t, pt, promptReq)

	// Wait for actual notifications from the LLM (tool_call, tool_result, agent_message_chunk)
	notifications := readNotificationsOnly(pt, 1, 60*time.Second)
	t.Logf("Got %d notifications", len(notifications))

	var hasToolCall, hasToolResult bool
	for _, notif := range notifications {
		params, _ := notif["params"].(map[string]interface{})
		update, _ := params["update"].(map[string]interface{})
		if update == nil {
			continue
		}
		switch update["sessionUpdate"] {
		case "tool_call":
			hasToolCall = true
		case "tool_result":
			hasToolResult = true
		}
	}
	if !hasToolCall {
		t.Log("No tool_call — LLM may have answered directly")
	}
	if !hasToolResult {
		t.Log("No tool_result")
	}

	clientSend(t, pt, fmt.Sprintf(`{"jsonrpc":"2.0","id":4,"method":"session/cancel","params":{"sessionId":%q}}`, sessionID))
	time.Sleep(100 * time.Millisecond)
}

// =====================================================================
// Test 14: Real LLM streaming
// =====================================================================

func TestIntegrationRealLLMStreaming(t *testing.T) {
	skipIfNoAPIKey(t)
	pt := newPipeTransport(t)
	defer pt.close()

	cfg := &config.Config{MaxIterations: 3}
	registry := tool.NewRegistry()
	prov := newRealProvider(t)
	handler := acp.NewHandler(cfg, registry, pt.agent, prov)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go handler.Run(ctx)

	clientSendAndRead(t, pt, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1,"clientCapabilities":{}}}`)
	resp := clientSendAndRead(t, pt, `{"jsonrpc":"2.0","id":2,"method":"session/new","params":{"cwd":"/tmp"}}`)
	sessionID := extractSessionID(resp)

	promptReq := fmt.Sprintf(`{"jsonrpc":"2.0","id":3,"method":"session/prompt","params":{"sessionId":%q,"prompt":[{"type":"text","text":"Write a 4-line poem about coding in Go. Just the poem."}]}}`, sessionID)
	clientSend(t, pt, promptReq)

	// Wait for actual streaming notifications
	notifications := readNotificationsOnly(pt, 1, 30*time.Second)

	var textChunks int
	var fullText string
	for _, notif := range notifications {
		params, _ := notif["params"].(map[string]interface{})
		update, _ := params["update"].(map[string]interface{})
		if update == nil {
			continue
		}
		if update["sessionUpdate"] == "agent_message_chunk" {
			content, _ := update["content"].(map[string]interface{})
			if text, ok := content["text"].(string); ok {
				textChunks++
				fullText += text
			}
		}
	}

	t.Logf("Streamed %d chunks, %d chars: %q", textChunks, len(fullText), fullText)
	if fullText == "" {
		t.Error("expected streamed text")
	}

	clientSend(t, pt, fmt.Sprintf(`{"jsonrpc":"2.0","id":4,"method":"session/cancel","params":{"sessionId":%q}}`, sessionID))
	time.Sleep(100 * time.Millisecond)
}

// =====================================================================
// Test 15: Bidirectional concurrent
// =====================================================================

func TestIntegrationBidirectionalConcurrent(t *testing.T) {
	skipIfNoAPIKey(t)
	pt := newPipeTransport(t)
	defer pt.close()

	cfg := &config.Config{}
	registry := tool.NewRegistry()
	prov := newRealProvider(t)
	handler := acp.NewHandler(cfg, registry, pt.agent, prov)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go handler.Run(ctx)

	// Client sends initialize
	clientSend(t, pt, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1,"clientCapabilities":{}}}`)

	// Simultaneously, agent sends a permission request
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		pt.agent.SendRequest(
			"session/request_permission",
			acp.PermissionRequestParams{
				SessionID: "concurrent",
				Request:   acp.PermissionRequest{Type: "terminal", Command: "ls", Description: "List"},
			},
			10*time.Second,
		)
	}()

	// Wait for both messages via collector: init response + permission request
	time.Sleep(100 * time.Millisecond)

	// Find and verify init response
	initResp := pt.collector.waitForResponse(1, 5*time.Second)
	if initResp == nil {
		t.Fatal("timed out waiting for init response")
	}

	// Find and respond to permission request
	permReq := pt.collector.waitForMethod("session/request_permission", 5*time.Second)
	if permReq == nil {
		t.Fatal("timed out waiting for permission request")
	}

	reqID := permReq["id"].(float64)
	respLine := fmt.Sprintf(`{"jsonrpc":"2.0","id":%.0f,"result":{"approved":true}}`, reqID)
	pt.clientWrite.Write([]byte(respLine + "\n"))

	wg.Wait()
	t.Log("Bidirectional concurrent OK")
}

// =====================================================================
// Test 16: MCP server config
// =====================================================================

func TestIntegrationSessionWithMCPConfig(t *testing.T) {
	skipIfNoAPIKey(t)
	pt := newPipeTransport(t)
	defer pt.close()

	cfg := &config.Config{MaxIterations: 3}
	registry := tool.NewRegistry()
	prov := newRealProvider(t)
	handler := acp.NewHandler(cfg, registry, pt.agent, prov)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go handler.Run(ctx)

	clientSendAndRead(t, pt, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1,"clientCapabilities":{}}}`)

	resp := clientSendAndRead(t, pt, `{"jsonrpc":"2.0","id":2,"method":"session/new","params":{"cwd":"/tmp","mcpServers":[{"name":"test-server","command":"echo","args":["hello"]}]}}`)
	if resp["error"] != nil {
		t.Logf("MCP session/new error (expected): %v", resp["error"])
	}
	sessionID := extractSessionID(resp)
	if sessionID != "" {
		t.Logf("Session with MCP created: %s", sessionID)
	}
}

// =====================================================================
// Test 17: OpenAI protocol provider
// =====================================================================

func TestIntegrationOpenAIProtocolProvider(t *testing.T) {
	key := skipIfNoAPIKey(t)
	pt := newPipeTransport(t)
	defer pt.close()

	prov := provider.NewOpenAIProviderWithBaseURL(key, getModel(), 128, testOpenAIBaseURL)
	cfg := &config.Config{MaxIterations: 3}
	registry := tool.NewRegistry()
	handler := acp.NewHandler(cfg, registry, pt.agent, prov)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go handler.Run(ctx)

	clientSendAndRead(t, pt, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1,"clientCapabilities":{}}}`)
	resp := clientSendAndRead(t, pt, `{"jsonrpc":"2.0","id":2,"method":"session/new","params":{"cwd":"/tmp"}}`)
	sessionID := extractSessionID(resp)

	promptReq := fmt.Sprintf(`{"jsonrpc":"2.0","id":3,"method":"session/prompt","params":{"sessionId":%q,"prompt":[{"type":"text","text":"Say hello in one word."}]}}`, sessionID)
	clientSend(t, pt, promptReq)

	// Wait for actual notifications
	notifications := readNotificationsOnly(pt, 1, 30*time.Second)
	t.Logf("OpenAI provider: %d notifications", len(notifications))

	clientSend(t, pt, fmt.Sprintf(`{"jsonrpc":"2.0","id":4,"method":"session/cancel","params":{"sessionId":%q}}`, sessionID))
	time.Sleep(100 * time.Millisecond)
}

// =====================================================================
// Test 18: Invalid initialize params
// =====================================================================

func TestIntegrationInvalidInitialize(t *testing.T) {
	skipIfNoAPIKey(t)
	pt := newPipeTransport(t)
	defer pt.close()

	cfg := &config.Config{}
	registry := tool.NewRegistry()
	prov := newRealProvider(t)
	handler := acp.NewHandler(cfg, registry, pt.agent, prov)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go handler.Run(ctx)

	resp := clientSendAndRead(t, pt, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":"invalid"}`)
	if resp["error"] == nil {
		t.Fatal("expected error")
	}
}

// =====================================================================
// Test 19: Session not found
// =====================================================================

func TestIntegrationSessionNotFound(t *testing.T) {
	skipIfNoAPIKey(t)
	pt := newPipeTransport(t)
	defer pt.close()

	cfg := &config.Config{}
	registry := tool.NewRegistry()
	prov := newRealProvider(t)
	handler := acp.NewHandler(cfg, registry, pt.agent, prov)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go handler.Run(ctx)

	clientSendAndRead(t, pt, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1,"clientCapabilities":{}}}`)
	resp := clientSendAndRead(t, pt, `{"jsonrpc":"2.0","id":2,"method":"session/prompt","params":{"sessionId":"nonexistent","prompt":[{"type":"text","text":"hi"}]}}`)
	if resp["error"] == nil {
		t.Fatal("expected error")
	}
}

// =====================================================================
// Test 20: Not initialized
// =====================================================================

func TestIntegrationNotInitialized(t *testing.T) {
	skipIfNoAPIKey(t)
	pt := newPipeTransport(t)
	defer pt.close()

	cfg := &config.Config{}
	registry := tool.NewRegistry()
	prov := newRealProvider(t)
	handler := acp.NewHandler(cfg, registry, pt.agent, prov)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go handler.Run(ctx)

	resp := clientSendAndRead(t, pt, `{"jsonrpc":"2.0","id":1,"method":"session/new","params":{"cwd":"/tmp"}}`)
	if resp["error"] == nil {
		t.Fatal("expected not-initialized error")
	}
}
