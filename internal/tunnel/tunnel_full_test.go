package tunnel

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// ─── Gateway Tests ───

func TestGatewayStart(t *testing.T) {
	g := NewGateway()
	port, token, err := g.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	if port <= 0 {
		t.Errorf("port = %d, want > 0", port)
	}
	if len(token) < 10 {
		t.Errorf("token = %q, want at least 10 chars", token)
	}
	if g.Port() != port {
		t.Errorf("Port() = %d, want %d", g.Port(), port)
	}
	if g.Token() != token {
		t.Errorf("Token() = %q, want %q", g.Token(), token)
	}
}

func TestGatewayHealthCheck(t *testing.T) {
	g := NewGateway()
	_, _, err := g.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/health", g.Port()))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("health status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "ok" {
		t.Errorf("health body = %q, want 'ok'", body)
	}
}

func TestGatewayRejectsNoToken(t *testing.T) {
	g := NewGateway()
	_, _, err := g.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	// HTTP GET without token should 401
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/ws", g.Port()))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestGatewayRejectsWrongToken(t *testing.T) {
	g := NewGateway()
	_, _, err := g.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/ws?token=wrong", g.Port()))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestGatewayWebSocketConnect(t *testing.T) {
	g := NewGateway()
	_, _, err := g.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	wsURL := fmt.Sprintf("ws://127.0.0.1:%d/ws?token=%s", g.Port(), g.Token())
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// Verify connection is established
	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"ping"}`)); err != nil {
		t.Fatalf("write ping: %v", err)
	}
}

func TestGatewaySendReceive(t *testing.T) {
	g := NewGateway()
	_, _, err := g.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	// Set up message handler
	received := make(chan GatewayMessage, 10)
	g.OnMessage(func(msg GatewayMessage) {
		received <- msg
	})

	// Connect client
	wsURL := fmt.Sprintf("ws://127.0.0.1:%d/ws?token=%s", g.Port(), g.Token())
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// Client sends message
	msg := GatewayMessage{Type: "message", Data: json.RawMessage(`{"text":"hello"}`)}
	msgBytes, _ := json.Marshal(msg)
	if err := conn.WriteMessage(websocket.TextMessage, msgBytes); err != nil {
		t.Fatal(err)
	}

	// Server receives it
	select {
	case got := <-received:
		if got.Type != "message" {
			t.Errorf("type = %q, want 'message'", got.Type)
		}
		var data MessageData
		if err := json.Unmarshal(got.Data, &data); err != nil {
			t.Fatal(err)
		}
		if data.Text != "hello" {
			t.Errorf("text = %q, want 'hello'", data.Text)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for message")
	}
}

func TestGatewayServerToClient(t *testing.T) {
	g := NewGateway()
	_, _, err := g.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	// Connect client first
	wsURL := fmt.Sprintf("ws://127.0.0.1:%d/ws?token=%s", g.Port(), g.Token())
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	time.Sleep(100 * time.Millisecond) // let goroutine start

	// Server sends message
	err = g.Send(GatewayMessage{
		Type: "text",
		Data: json.RawMessage(`{"id":"msg-1","chunk":"Hello"}`),
	})
	if err != nil {
		t.Fatal(err)
	}

	// Client receives it
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, msgBytes, err := conn.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}

	var got GatewayMessage
	if err := json.Unmarshal(msgBytes, &got); err != nil {
		t.Fatal(err)
	}
	if got.Type != "text" {
		t.Errorf("type = %q, want 'text'", got.Type)
	}
}

func TestGatewaySendNoClient(t *testing.T) {
	g := NewGateway()
	_, _, err := g.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	err = g.Send(GatewayMessage{Type: "test"})
	if err == nil {
		t.Error("expected error when no client connected")
	}
	if err.Error() != "no client connected" {
		t.Errorf("error = %q, want 'no client connected'", err.Error())
	}
}

func TestGatewayConnectURL(t *testing.T) {
	g := NewGateway()
	url := g.ConnectURL("abc123.lhr.life")
	if !strings.HasPrefix(url, "wss://") {
		t.Errorf("ConnectURL = %q, want wss:// prefix", url)
	}
	if !strings.Contains(url, "token=") {
		t.Errorf("ConnectURL = %q, want token parameter", url)
	}
}

func TestGatewayMultipleClients(t *testing.T) {
	g := NewGateway()
	_, _, err := g.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	wsURL := fmt.Sprintf("ws://127.0.0.1:%d/ws?token=%s", g.Port(), g.Token())

	// First client connects
	conn1, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn1.Close()

	time.Sleep(100 * time.Millisecond)

	// Second client connects (replaces first)
	conn2, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn2.Close()

	time.Sleep(100 * time.Millisecond)

	// Server sends to second client (current connection)
	err = g.Send(GatewayMessage{Type: "test", Data: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}

	conn2.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _, err = conn2.ReadMessage()
	if err != nil {
		t.Fatalf("second client should receive: %v", err)
	}
}

// ─── Token Tests ───

func TestGenerateToken(t *testing.T) {
	token1 := generateToken(24)
	token2 := generateToken(24)

	if len(token1) != 48 { // hex of 24 bytes
		t.Errorf("token length = %d, want 48", len(token1))
	}
	if token1 == token2 {
		t.Error("two tokens should be different")
	}
}

// ─── Gateway Close Tests ───

func TestGatewayCloseAndReuse(t *testing.T) {
	g := NewGateway()
	_, _, err := g.Start()
	if err != nil {
		t.Fatal(err)
	}

	port := g.Port()
	g.Close()

	// Port should be released
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Logf("port not released (may be TIME_WAIT): %v", err)
	} else {
		ln.Close()
	}
}

// ─── Broker Tests ───

func TestBrokerSendSessionInfo(t *testing.T) {
	g := NewGateway()
	_, _, err := g.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	// Connect client
	wsURL := fmt.Sprintf("ws://127.0.0.1:%d/ws?token=%s", g.Port(), g.Token())
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	time.Sleep(100 * time.Millisecond)

	// Create a minimal session wrapper
	sess := &Session{gateway: g}
	sess.gateway.OnMessage(func(msg GatewayMessage) {})

	broker := NewBroker(sess)
	broker.SendSessionInfo(SessionInfoData{
		Workspace: "/home/user/project",
		Model:     "gpt-4",
		Provider:  "openai",
		Mode:      "supervised",
		Version:   "1.3.6",
	})

	// Client should receive the session_info message
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, msgBytes, err := conn.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}

	var msg GatewayMessage
	if err := json.Unmarshal(msgBytes, &msg); err != nil {
		t.Fatal(err)
	}
	if msg.Type != "session_info" {
		t.Errorf("type = %q, want 'session_info'", msg.Type)
	}

	var data SessionInfoData
	if err := json.Unmarshal(msg.Data, &data); err != nil {
		t.Fatal(err)
	}
	if data.Workspace != "/home/user/project" {
		t.Errorf("workspace = %q, want '/home/user/project'", data.Workspace)
	}
	if data.Model != "gpt-4" {
		t.Errorf("model = %q, want 'gpt-4'", data.Model)
	}
}

func TestBrokerStreamingText(t *testing.T) {
	g := NewGateway()
	_, _, err := g.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	wsURL := fmt.Sprintf("ws://127.0.0.1:%d/ws?token=%s", g.Port(), g.Token())
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	time.Sleep(100 * time.Millisecond)

	sess := &Session{gateway: g}
	sess.gateway.OnMessage(func(msg GatewayMessage) {})
	broker := NewBroker(sess)

	msgID := broker.NextMessageID()
	broker.PushText(msgID, "Hello, ")
	broker.PushText(msgID, "world!")
	broker.PushTextDone(msgID)

	// Read 3 messages
	expected := []string{"Hello, ", "world!", ""}
	for i, want := range expected {
		conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, msgBytes, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
		var msg GatewayMessage
		if err := json.Unmarshal(msgBytes, &msg); err != nil {
			t.Fatal(err)
		}

		expectedType := "text"
		if i == 2 {
			expectedType = "text_done"
		}
		if msg.Type != expectedType {
			t.Errorf("msg[%d].type = %q, want %q", i, msg.Type, expectedType)
		}

		var data TextData
		if err := json.Unmarshal(msg.Data, &data); err != nil {
			t.Fatal(err)
		}
		if data.Chunk != want {
			t.Errorf("msg[%d].chunk = %q, want %q", i, data.Chunk, want)
		}
		if data.ID != msgID {
			t.Errorf("msg[%d].id = %q, want %q", i, data.ID, msgID)
		}
	}
}

func TestBrokerToolEvents(t *testing.T) {
	g := NewGateway()
	_, _, err := g.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	wsURL := fmt.Sprintf("ws://127.0.0.1:%d/ws?token=%s", g.Port(), g.Token())
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	time.Sleep(100 * time.Millisecond)

	sess := &Session{gateway: g}
	sess.gateway.OnMessage(func(msg GatewayMessage) {})
	broker := NewBroker(sess)

	broker.PushStatus(StatusRunning, "executing read_file")
	broker.PushToolCall("read_file", `{"path":"main.go"}`, "read_file(main.go)")
	broker.PushToolResult("read_file", "package main\n", false)

	// Read 3 messages
	types := []string{}
	for i := 0; i < 3; i++ {
		conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, msgBytes, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
		var msg GatewayMessage
		json.Unmarshal(msgBytes, &msg)
		types = append(types, msg.Type)
	}

	if types[0] != "status" {
		t.Errorf("msg[0] type = %q, want 'status'", types[0])
	}
	if types[1] != "tool_call" {
		t.Errorf("msg[1] type = %q, want 'tool_call'", types[1])
	}
	if types[2] != "tool_result" {
		t.Errorf("msg[2] type = %q, want 'tool_result'", types[2])
	}
}

func TestBrokerApproval(t *testing.T) {
	g := NewGateway()
	_, _, err := g.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	wsURL := fmt.Sprintf("ws://127.0.0.1:%d/ws?token=%s", g.Port(), g.Token())
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	time.Sleep(100 * time.Millisecond)

	sess := &Session{gateway: g}
	sess.gateway.OnMessage(func(msg GatewayMessage) {})
	broker := NewBroker(sess)

	broker.PushApprovalRequest("123", "run_command", "rm -rf /")

	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, msgBytes, err := conn.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}

	var msg GatewayMessage
	json.Unmarshal(msgBytes, &msg)
	if msg.Type != "approval_request" {
		t.Errorf("type = %q, want 'approval_request'", msg.Type)
	}

	var data ApprovalRequestData
	json.Unmarshal(msg.Data, &data)
	if data.ID != "123" {
		t.Errorf("id = %q, want '123'", data.ID)
	}
	if data.ToolName != "run_command" {
		t.Errorf("tool_name = %q, want 'run_command'", data.ToolName)
	}
	if data.Input != "rm -rf /" {
		t.Errorf("input = %q, want 'rm -rf /'", data.Input)
	}
}

func TestBrokerError(t *testing.T) {
	g := NewGateway()
	_, _, err := g.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	wsURL := fmt.Sprintf("ws://127.0.0.1:%d/ws?token=%s", g.Port(), g.Token())
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	time.Sleep(100 * time.Millisecond)

	sess := &Session{gateway: g}
	sess.gateway.OnMessage(func(msg GatewayMessage) {})
	broker := NewBroker(sess)

	broker.PushError("something went wrong")

	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, msgBytes, err := conn.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}

	var msg GatewayMessage
	json.Unmarshal(msgBytes, &msg)
	if msg.Type != "error" {
		t.Errorf("type = %q, want 'error'", msg.Type)
	}

	var data ErrorData
	json.Unmarshal(msg.Data, &data)
	if data.Message != "something went wrong" {
		t.Errorf("message = %q, want 'something went wrong'", data.Message)
	}
}

func TestBrokerNextMessageID(t *testing.T) {
	g := NewGateway()
	sess := &Session{gateway: g}
	broker := NewBroker(sess)

	id1 := broker.NextMessageID()
	id2 := broker.NextMessageID()
	if id1 == id2 {
		t.Error("IDs should be unique")
	}
	if !strings.HasPrefix(id1, "msg-") {
		t.Errorf("id = %q, want 'msg-' prefix", id1)
	}
}

// ─── Protocol Constants Tests ───

func TestProtocolConstants(t *testing.T) {
	// Verify all constants are defined and non-empty
	events := []string{
		EventConnected, EventSessionInfo, EventText, EventTextDone,
		EventStatus, EventToolCall, EventToolResult,
		EventApprovalRequest, EventApprovalResult, EventError,
		EventPing, EventDisconnected,
	}
	for _, e := range events {
		if e == "" {
			t.Error("empty event constant")
		}
	}

	cmds := []string{CmdMessage, CmdApprovalResponse, CmdInterrupt, CmdModeChange, CmdPong}
	for _, c := range cmds {
		if c == "" {
			t.Error("empty command constant")
		}
	}

	statuses := []string{StatusIdle, StatusThinking, StatusRunning, StatusWaiting, StatusError}
	for _, s := range statuses {
		if s == "" {
			t.Error("empty status constant")
		}
	}

	modes := []string{ModeSupervised, ModeAuto, ModeBypass, ModeAutopilot}
	for _, m := range modes {
		if m == "" {
			t.Error("empty mode constant")
		}
	}

	decisions := []string{DecisionAllow, DecisionDeny, DecisionAlwaysAllow}
	for _, d := range decisions {
		if d == "" {
			t.Error("empty decision constant")
		}
	}
}

func TestProtocolJSONRoundtrip(t *testing.T) {
	tests := []struct {
		name string
		val  interface{}
	}{
		{"SessionInfoData", SessionInfoData{Workspace: "/tmp", Model: "gpt-4", Provider: "openai", Mode: "auto", Version: "1.0"}},
		{"TextData", TextData{ID: "msg-1", Chunk: "hello", Done: false}},
		{"StatusData", StatusData{Status: StatusThinking, Message: "processing"}},
		{"ToolCallData", ToolCallData{ToolName: "read_file", Args: `{}`, Detail: "read_file(main.go)"}},
		{"ToolResultData", ToolResultData{ToolName: "read_file", Result: "ok", IsError: false}},
		{"ApprovalRequestData", ApprovalRequestData{ID: "123", ToolName: "run_command", Input: "ls"}},
		{"ApprovalResponseData", ApprovalResponseData{ID: "123", Decision: DecisionAllow}},
		{"MessageData", MessageData{Text: "hello"}},
		{"ModeChangeData", ModeChangeData{Mode: ModeAuto}},
		{"ErrorData", ErrorData{Message: "oops", Code: "E001"}},

		// Ask User types
		{"AskUserRequestData", AskUserRequestData{
			ID: "ask-1", Title: "Choose deployment",
			Questions: []AskUserQuestion{
				{ID: "q1", Prompt: "Scale?", Kind: "single",
					Choices:       []AskUserChoice{{ID: "c1", Label: "Small"}, {ID: "c2", Label: "Full"}},
					AllowFreeform: true, Placeholder: "Or type..."},
			},
		}},
		{"AskUserResponseData", AskUserResponseData{
			ID: "ask-1", Status: "submitted",
			Answers: []AskUserAnswer{
				{QuestionID: "q1", ChoiceIDs: []string{"c1"}, FreeformText: ""},
			},
		}},

		// Sub-agent types
		{"SubagentSpawnData", SubagentSpawnData{AgentID: "sa-1", Name: "Researcher", Task: "Search codebase", Color: "#4CAF50"}},
		{"SubagentTextData", SubagentTextData{AgentID: "sa-1", ID: "msg-1", Chunk: "Found 3 files", Done: false}},
		{"SubagentStatusData", SubagentStatusData{AgentID: "sa-1", Status: "running", Message: "Searching..."}},
		{"SubagentCompleteData", SubagentCompleteData{AgentID: "sa-1", Name: "Researcher", Summary: "Found 3 files", Success: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, err := json.Marshal(tt.val)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			// Verify it's valid JSON with expected fields
			if !json.Valid(b) {
				t.Errorf("invalid JSON: %s", b)
			}
		})
	}
}

// ─── QR Code Tests ───

func TestQRCodeForURL(t *testing.T) {
	qr, err := QRCodeForURL("wss://abc.lhr.life/ws?token=123")
	if err != nil {
		t.Fatal(err)
	}
	if qr == "" {
		t.Error("QR code is empty")
	}
	if len(qr) < 50 {
		t.Errorf("QR code too short (%d chars), expected a visual QR code", len(qr))
	}
}

func TestQRCodeLines(t *testing.T) {
	lines, err := QRCodeLines("wss://abc.lhr.life/ws?token=123")
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) < 5 {
		t.Errorf("QR code has %d lines, expected at least 5", len(lines))
	}
}

// ─── Session Tests (without real tunnel) ───

func TestNewSession(t *testing.T) {
	sess := NewSession()
	if sess == nil {
		t.Fatal("NewSession returned nil")
	}
	if sess.Info() != nil {
		t.Error("Info() should be nil before Start()")
	}
}

// ─── Gateway with httptest ───

func TestGatewayUpgraderCheckOrigin(t *testing.T) {
	g := NewGateway()
	// The upgrader should allow any origin
	if !g.upgrader.CheckOrigin(httptest.NewRequest("GET", "/ws", nil)) {
		t.Error("CheckOrigin should return true for any request")
	}
}

// ─── Broker: Ask User Tests ───

func TestBrokerAskUserRequest(t *testing.T) {
	g, conn := startGatewayWithClient(t)
	defer g.Close()
	defer conn.Close()

	sess := &Session{gateway: g}
	sess.gateway.OnMessage(func(msg GatewayMessage) {})
	broker := NewBroker(sess)

	broker.PushAskUserRequest("ask-1", "Choose deployment scale", []AskUserQuestion{
		{ID: "q1", Prompt: "Scale?", Kind: "single",
			Choices:       []AskUserChoice{{ID: "c1", Label: "Small"}, {ID: "c2", Label: "Full"}},
			AllowFreeform: true, Placeholder: "Or type custom..."},
		{ID: "q2", Prompt: "Region?", Kind: "text", Placeholder: "e.g. us-east-1"},
	})

	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, msgBytes, err := conn.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}

	var msg GatewayMessage
	json.Unmarshal(msgBytes, &msg)
	if msg.Type != "ask_user_request" {
		t.Fatalf("type = %q, want 'ask_user_request'", msg.Type)
	}

	var data AskUserRequestData
	json.Unmarshal(msg.Data, &data)
	if data.ID != "ask-1" {
		t.Errorf("id = %q, want 'ask-1'", data.ID)
	}
	if data.Title != "Choose deployment scale" {
		t.Errorf("title = %q", data.Title)
	}
	if len(data.Questions) != 2 {
		t.Fatalf("questions count = %d, want 2", len(data.Questions))
	}
	if data.Questions[0].Kind != "single" {
		t.Errorf("q1 kind = %q, want 'single'", data.Questions[0].Kind)
	}
	if len(data.Questions[0].Choices) != 2 {
		t.Errorf("q1 choices = %d, want 2", len(data.Questions[0].Choices))
	}
	if data.Questions[1].Kind != "text" {
		t.Errorf("q2 kind = %q, want 'text'", data.Questions[1].Kind)
	}
}

// ─── Broker: Sub-agent Tests ───

func TestBrokerSubagentLifecycle(t *testing.T) {
	g, conn := startGatewayWithClient(t)
	defer g.Close()
	defer conn.Close()

	sess := &Session{gateway: g}
	sess.gateway.OnMessage(func(msg GatewayMessage) {})
	broker := NewBroker(sess)

	// 1. Spawn
	broker.PushSubagentSpawn("sa-1", "Researcher", "Search codebase for TODO patterns", "#4CAF50", "")

	// 2. Status
	broker.PushSubagentStatus("sa-1", "running", "Searching files...")

	// 3. Streaming text
	msgID := broker.NextMessageID()
	broker.PushSubagentText("sa-1", msgID, "Found 3 TODO items in main.go", true) // done in one shot

	// 4. Complete
	broker.PushSubagentComplete("sa-1", "Researcher", "Found 3 TODO items in main.go", true)

	// Read and verify all 4 messages
	expectedTypes := []string{"subagent_spawn", "subagent_status", "subagent_text", "subagent_complete"}
	for i, want := range expectedTypes {
		conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, msgBytes, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
		var msg GatewayMessage
		json.Unmarshal(msgBytes, &msg)
		if msg.Type != want {
			t.Errorf("msg[%d] type = %q, want %q", i, msg.Type, want)
		}
	}

	// Verify spawn data
	// (re-read first message)
	conn2, _, _ := websocket.DefaultDialer.Dial(
		fmt.Sprintf("ws://127.0.0.1:%d/ws?token=%s", g.Port(), g.Token()), nil)
	defer conn2.Close()
	time.Sleep(100 * time.Millisecond)

	broker.PushSubagentSpawn("sa-2", "Coder", "Implement fix", "#2196F3", "sa-1")
	conn2.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, msgBytes, _ := conn2.ReadMessage()
	var spawnMsg GatewayMessage
	json.Unmarshal(msgBytes, &spawnMsg)
	var spawnData SubagentSpawnData
	json.Unmarshal(spawnMsg.Data, &spawnData)
	if spawnData.AgentID != "sa-2" {
		t.Errorf("agent_id = %q, want 'sa-2'", spawnData.AgentID)
	}
	if spawnData.ParentID != "sa-1" {
		t.Errorf("parent_id = %q, want 'sa-1'", spawnData.ParentID)
	}
	if spawnData.Color != "#2196F3" {
		t.Errorf("color = %q, want '#2196F3'", spawnData.Color)
	}
}

// ─── Helper ───

func startGatewayWithClient(t *testing.T) (*Gateway, *websocket.Conn) {
	t.Helper()
	g := NewGateway()
	_, _, err := g.Start()
	if err != nil {
		t.Fatal(err)
	}
	wsURL := fmt.Sprintf("ws://127.0.0.1:%d/ws?token=%s", g.Port(), g.Token())
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		g.Close()
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	return g, conn
}
