//go:build !integration

package tunnel

import (
	"encoding/json"
	"sync"
	"testing"
	"time"
)

// newBrokerForTest creates a Broker with a mock-like session that captures
// all sent messages. The senderLoop goroutine is NOT started so we can
// inspect the outbound queue directly.
func newBrokerForTest() (*Broker, *drainHelper) {
	sess := NewSession("wss://test.local")
	rc, _ := NewRelayClient("wss://test.local", "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4")
	rc.Close()
	sess.client = rc

	d := &drainHelper{}

	b := &Broker{
		session:  sess,
		outDone:  make(chan struct{}),
		textBuf:  make(map[string]*textEntry),
		textTick: time.NewTicker(300 * time.Millisecond),
		textDone: make(chan struct{}),
	}
	b.outCond = sync.NewCond(&b.outMu)

	// Do NOT start senderLoop - we'll drain manually
	// Do NOT start textFlushLoop - we'll flush manually or wait for ticker

	// Start only the text flush loop (so batched text tests work)
	go b.textFlushLoop()

	// Wire handlers like NewBroker does
	sess.OnMessage(func(msg GatewayMessage) {
		if msg.Type == EventAck {
			return
		}
	})
	sess.OnConnect(func() {})

	d.b = b
	return b, d
}

type drainHelper struct {
	b    *Broker
	msgs []GatewayMessage
	mu   sync.Mutex
}

// drain reads all pending outbound messages.
func (d *drainHelper) drain() []GatewayMessage {
	d.b.outMu.Lock()
	msgs := d.b.outbound
	d.b.outbound = nil
	d.b.outMu.Unlock()
	d.mu.Lock()
	d.msgs = append(d.msgs, msgs...)
	d.mu.Unlock()
	return msgs
}

func (d *drainHelper) getAll() []GatewayMessage {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]GatewayMessage, len(d.msgs))
	copy(out, d.msgs)
	return out
}

// --- Broker Tests ---

func TestBrokerNextMessageID(t *testing.T) {
	b, _ := newBrokerForTest()
	defer b.Stop()

	id1 := b.NextMessageID()
	id2 := b.NextMessageID()
	if id1 == id2 {
		t.Error("message IDs should be unique")
	}
	if id1 == "" || id2 == "" {
		t.Error("message IDs should not be empty")
	}
}

func TestBrokerOnCommand(t *testing.T) {
	b, _ := newBrokerForTest()
	defer b.Stop()
	b.OnCommand(func(cmd GatewayMessage) {})
}

func TestBrokerOnClientConnect(t *testing.T) {
	b, _ := newBrokerForTest()
	defer b.Stop()
	b.OnClientConnect(func() {})
}

func TestBrokerPushTextAndFlush(t *testing.T) {
	b, d := newBrokerForTest()
	defer b.Stop()

	b.PushText("msg-1", "hello ")
	b.PushText("msg-1", "world")

	// Wait for text flush ticker (300ms)
	time.Sleep(500 * time.Millisecond)
	msgs := d.drain()

	found := false
	for _, m := range msgs {
		if m.Type == EventText {
			found = true
			var td TextData
			if err := json.Unmarshal(m.Data, &td); err != nil {
				t.Fatal(err)
			}
			if td.Chunk != "hello world" {
				t.Errorf("chunk = %q, want %q", td.Chunk, "hello world")
			}
			if td.ID != "msg-1" {
				t.Errorf("ID = %q, want %q", td.ID, "msg-1")
			}
		}
	}
	if !found {
		t.Error("expected a text event to be flushed")
	}
}

func TestBrokerPushTextDone(t *testing.T) {
	b, d := newBrokerForTest()
	defer b.Stop()

	b.PushText("msg-2", "chunk1")
	b.PushTextDone("msg-2")

	time.Sleep(200 * time.Millisecond)
	msgs := d.drain()

	hasText := false
	hasDone := false
	for _, m := range msgs {
		if m.Type == EventText {
			hasText = true
		}
		if m.Type == EventTextDone {
			hasDone = true
			var td TextData
			json.Unmarshal(m.Data, &td)
			if td.ID != "msg-2" || !td.Done {
				t.Errorf("text_done mismatch: %+v", td)
			}
		}
	}
	if !hasText {
		t.Error("expected text event before done")
	}
	if !hasDone {
		t.Error("expected text_done event")
	}
}

func TestBrokerPushTextDoneEmptyBuffer(t *testing.T) {
	b, d := newBrokerForTest()
	defer b.Stop()

	b.PushTextDone("msg-empty")
	time.Sleep(200 * time.Millisecond)
	msgs := d.drain()

	for _, m := range msgs {
		if m.Type == EventText {
			t.Error("should not have text event for empty buffer")
		}
		if m.Type == EventTextDone {
			var td TextData
			json.Unmarshal(m.Data, &td)
			if td.ID != "msg-empty" {
				t.Errorf("ID = %q, want %q", td.ID, "msg-empty")
			}
		}
	}
}

func TestBrokerSendSessionInfo(t *testing.T) {
	b, d := newBrokerForTest()
	defer b.Stop()

	b.SendSessionInfo(SessionInfoData{
		Workspace: "/tmp/project",
		Model:     "gpt-4",
		Provider:  "openai",
		Mode:      "auto",
		Version:   "1.0",
	})
	time.Sleep(50 * time.Millisecond)
	msgs := d.drain()

	found := false
	for _, m := range msgs {
		if m.Type == EventSessionInfo {
			found = true
			var data SessionInfoData
			json.Unmarshal(m.Data, &data)
			if data.Workspace != "/tmp/project" || data.Model != "gpt-4" {
				t.Errorf("session info mismatch: %+v", data)
			}
		}
	}
	if !found {
		t.Error("expected session_info event")
	}
}

func TestBrokerPushStatus(t *testing.T) {
	b, d := newBrokerForTest()
	defer b.Stop()

	b.PushStatus("thinking", "processing")
	time.Sleep(50 * time.Millisecond)
	msgs := d.drain()

	found := false
	for _, m := range msgs {
		if m.Type == EventStatus {
			found = true
			var sd StatusData
			json.Unmarshal(m.Data, &sd)
			if sd.Status != "thinking" || sd.Message != "processing" {
				t.Errorf("status mismatch: %+v", sd)
			}
		}
	}
	if !found {
		t.Error("expected status event")
	}
}

func TestBrokerPushUserMessage(t *testing.T) {
	b, d := newBrokerForTest()
	defer b.Stop()

	b.PushUserMessage("hello agent")
	time.Sleep(50 * time.Millisecond)
	msgs := d.drain()

	found := false
	for _, m := range msgs {
		if m.Type == EventUserMessage {
			found = true
		}
	}
	if !found {
		t.Error("expected user_message event")
	}
}

func TestBrokerPushToolCall(t *testing.T) {
	b, d := newBrokerForTest()
	defer b.Stop()

	b.PushToolCall("t1", "read_file", `{"path":"/tmp"}`, "reading file")
	time.Sleep(50 * time.Millisecond)
	msgs := d.drain()

	found := false
	for _, m := range msgs {
		if m.Type == EventToolCall {
			found = true
			var td ToolCallData
			json.Unmarshal(m.Data, &td)
			if td.ToolID != "t1" || td.ToolName != "read_file" {
				t.Errorf("tool call mismatch: %+v", td)
			}
		}
	}
	if !found {
		t.Error("expected tool_call event")
	}
}

func TestBrokerPushToolResult(t *testing.T) {
	b, d := newBrokerForTest()
	defer b.Stop()

	b.PushToolResult("t1", "search", "found 3", false)
	time.Sleep(50 * time.Millisecond)
	msgs := d.drain()

	found := false
	for _, m := range msgs {
		if m.Type == EventToolResult {
			found = true
			var td ToolResultData
			json.Unmarshal(m.Data, &td)
			if td.ToolID != "t1" || td.IsError {
				t.Errorf("tool result mismatch: %+v", td)
			}
		}
	}
	if !found {
		t.Error("expected tool_result event")
	}
}

func TestBrokerPushApprovalRequest(t *testing.T) {
	b, d := newBrokerForTest()
	defer b.Stop()

	b.PushApprovalRequest("appr-1", "run_command", "rm -rf /")
	time.Sleep(50 * time.Millisecond)
	msgs := d.drain()

	found := false
	for _, m := range msgs {
		if m.Type == EventApprovalRequest {
			found = true
		}
	}
	if !found {
		t.Error("expected approval_request event")
	}
}

func TestBrokerPushApprovalResult(t *testing.T) {
	b, d := newBrokerForTest()
	defer b.Stop()

	b.PushApprovalResult("appr-1", "allow")
	time.Sleep(50 * time.Millisecond)
	msgs := d.drain()

	found := false
	for _, m := range msgs {
		if m.Type == EventApprovalResult {
			found = true
		}
	}
	if !found {
		t.Error("expected approval_result event")
	}
}

func TestBrokerPushError(t *testing.T) {
	b, d := newBrokerForTest()
	defer b.Stop()

	b.PushError("something broke")
	time.Sleep(50 * time.Millisecond)
	msgs := d.drain()

	found := false
	for _, m := range msgs {
		if m.Type == EventError {
			found = true
			var ed ErrorData
			json.Unmarshal(m.Data, &ed)
			if ed.Message != "something broke" {
				t.Errorf("error message = %q, want %q", ed.Message, "something broke")
			}
		}
	}
	if !found {
		t.Error("expected error event")
	}
}

func TestBrokerPushAskUserRequest(t *testing.T) {
	b, d := newBrokerForTest()
	defer b.Stop()

	b.PushAskUserRequest("ask-1", "Confirm", []AskUserQuestion{
		{ID: "q1", Prompt: "Continue?", Kind: "single"},
	})
	time.Sleep(50 * time.Millisecond)
	msgs := d.drain()

	found := false
	for _, m := range msgs {
		if m.Type == EventAskUserRequest {
			found = true
		}
	}
	if !found {
		t.Error("expected ask_user_request event")
	}
}

func TestBrokerPushAskUserResponse(t *testing.T) {
	b, d := newBrokerForTest()
	defer b.Stop()

	b.PushAskUserResponse("ask-1", "submitted", []AskUserAnswer{
		{QuestionID: "q1", ChoiceIDs: []string{"y"}},
	})
	time.Sleep(50 * time.Millisecond)
	msgs := d.drain()

	found := false
	for _, m := range msgs {
		if m.Type == EventAskUserResponse {
			found = true
		}
	}
	if !found {
		t.Error("expected ask_user_response event")
	}
}

func TestBrokerPushSubagentSpawn(t *testing.T) {
	b, d := newBrokerForTest()
	defer b.Stop()

	b.PushSubagentSpawn("agent-1", "Researcher", "find bugs", "#4CAF50", "")
	time.Sleep(50 * time.Millisecond)
	msgs := d.drain()

	found := false
	for _, m := range msgs {
		if m.Type == EventSubagentSpawn {
			found = true
			var sd SubagentSpawnData
			json.Unmarshal(m.Data, &sd)
			if sd.AgentID != "agent-1" || sd.Name != "Researcher" {
				t.Errorf("spawn mismatch: %+v", sd)
			}
		}
	}
	if !found {
		t.Error("expected subagent_spawn event")
	}
}

func TestBrokerPushSubagentTextBatched(t *testing.T) {
	b, d := newBrokerForTest()
	defer b.Stop()

	b.PushSubagentText("agent-1", "msg-s1", "hello ", false)
	b.PushSubagentText("agent-1", "msg-s1", "world", false)

	time.Sleep(500 * time.Millisecond)
	msgs := d.drain()

	found := false
	for _, m := range msgs {
		if m.Type == EventSubagentText {
			found = true
			var sd SubagentTextData
			json.Unmarshal(m.Data, &sd)
			if sd.Chunk != "hello world" {
				t.Errorf("subagent text chunk = %q, want %q", sd.Chunk, "hello world")
			}
			if sd.AgentID != "agent-1" {
				t.Errorf("agentID = %q, want %q", sd.AgentID, "agent-1")
			}
		}
	}
	if !found {
		t.Error("expected subagent_text event")
	}
}

func TestBrokerPushSubagentTextDone(t *testing.T) {
	b, d := newBrokerForTest()
	defer b.Stop()

	b.PushSubagentText("agent-1", "msg-s2", "final chunk", false)
	b.PushSubagentText("agent-1", "msg-s2", "", true) // done=true triggers flush

	time.Sleep(200 * time.Millisecond)
	msgs := d.drain()

	hasText := false
	for _, m := range msgs {
		if m.Type == EventSubagentText {
			hasText = true
			var sd SubagentTextData
			json.Unmarshal(m.Data, &sd)
			if sd.Chunk != "final chunk" {
				t.Errorf("chunk = %q, want %q", sd.Chunk, "final chunk")
			}
		}
	}
	if !hasText {
		t.Error("expected subagent_text event on done")
	}
}

func TestBrokerPushSubagentStatus(t *testing.T) {
	b, d := newBrokerForTest()
	defer b.Stop()

	b.PushSubagentStatus("agent-1", "running", "working")
	time.Sleep(50 * time.Millisecond)
	msgs := d.drain()

	found := false
	for _, m := range msgs {
		if m.Type == EventSubagentStatus {
			found = true
		}
	}
	if !found {
		t.Error("expected subagent_status event")
	}
}

func TestBrokerPushSubagentComplete(t *testing.T) {
	b, d := newBrokerForTest()
	defer b.Stop()

	b.PushSubagentComplete("agent-1", "Researcher", "found 3 bugs", true)
	time.Sleep(50 * time.Millisecond)
	msgs := d.drain()

	found := false
	for _, m := range msgs {
		if m.Type == EventSubagentComplete {
			found = true
			var sd SubagentCompleteData
			json.Unmarshal(m.Data, &sd)
			if !sd.Success {
				t.Error("Success should be true")
			}
		}
	}
	if !found {
		t.Error("expected subagent_complete event")
	}
}

func TestBrokerPushSubagentToolCall(t *testing.T) {
	b, d := newBrokerForTest()
	defer b.Stop()

	b.PushSubagentToolCall("agent-1", "t1", "search", `{"pattern":"TODO"}`, "searching")
	time.Sleep(50 * time.Millisecond)
	msgs := d.drain()

	found := false
	for _, m := range msgs {
		if m.Type == EventSubagentToolCall {
			found = true
		}
	}
	if !found {
		t.Error("expected subagent_tool_call event")
	}
}

func TestBrokerPushSubagentToolResult(t *testing.T) {
	b, d := newBrokerForTest()
	defer b.Stop()

	b.PushSubagentToolResult("agent-1", "t1", "search", "found 5", false)
	time.Sleep(50 * time.Millisecond)
	msgs := d.drain()

	found := false
	for _, m := range msgs {
		if m.Type == EventSubagentToolResult {
			found = true
		}
	}
	if !found {
		t.Error("expected subagent_tool_result event")
	}
}

func TestBrokerPushSharingStopped(t *testing.T) {
	b, d := newBrokerForTest()
	defer b.Stop()

	b.PushSharingStopped()
	time.Sleep(50 * time.Millisecond)
	msgs := d.drain()

	found := false
	for _, m := range msgs {
		if m.Type == "sharing_stopped" {
			found = true
		}
	}
	if !found {
		t.Error("expected sharing_stopped event")
	}
}

func TestBrokerPushChatClear(t *testing.T) {
	b, d := newBrokerForTest()
	defer b.Stop()

	// First push something to the log
	b.PushStatus("running", "go")
	time.Sleep(50 * time.Millisecond)
	d.drain()

	// Verify we have a log entry
	b.logMu.Lock()
	logLen := len(b.sentLog)
	b.logMu.Unlock()
	if logLen == 0 {
		t.Fatal("should have log entries before clear")
	}

	// Clear should reset the log and enqueue chat_clear
	b.PushChatClear()
	time.Sleep(50 * time.Millisecond)
	msgs := d.drain()

	found := false
	for _, m := range msgs {
		if m.Type == "chat_clear" {
			found = true
		}
	}
	if !found {
		t.Error("expected chat_clear event")
	}

	// Verify sentLog was cleared (chat_clear itself is not logged)
	b.logMu.Lock()
	logLen = len(b.sentLog)
	b.logMu.Unlock()
	// chat_clear is logged by recordLog (only sharing_stopped is skipped)
	// so after clearing, the log will contain the chat_clear entry itself
	if logLen > 1 {
		t.Errorf("sentLog should have at most 1 entry (chat_clear), got %d", logLen)
	}
}

func TestBrokerPushChatHistory(t *testing.T) {
	b, d := newBrokerForTest()
	defer b.Stop()

	b.PushChatHistory([]HistoryEntry{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi there"},
	})
	time.Sleep(50 * time.Millisecond)
	msgs := d.drain()

	found := false
	for _, m := range msgs {
		if m.Type == "chat_history" {
			found = true
		}
	}
	if !found {
		t.Error("expected chat_history event")
	}
}

func TestBrokerSeqIncrement(t *testing.T) {
	b, d := newBrokerForTest()
	defer b.Stop()

	b.PushStatus("idle", "")
	b.PushStatus("running", "")
	b.PushStatus("thinking", "")
	time.Sleep(100 * time.Millisecond)
	msgs := d.drain()

	if len(msgs) < 3 {
		t.Fatalf("expected at least 3 messages, got %d", len(msgs))
	}

	for i := 1; i < len(msgs); i++ {
		if msgs[i].Seq <= msgs[i-1].Seq {
			t.Errorf("seq not increasing: msgs[%d].Seq=%d <= msgs[%d].Seq=%d",
				i, msgs[i].Seq, i-1, msgs[i-1].Seq)
		}
	}
}

func TestBrokerRecordLogSkipsSharingStopped(t *testing.T) {
	b, d := newBrokerForTest()
	defer b.Stop()

	b.PushSharingStopped()
	b.PushStatus("running", "go")
	time.Sleep(100 * time.Millisecond)
	d.drain()

	b.logMu.Lock()
	log := b.sentLog
	b.logMu.Unlock()

	for _, m := range log {
		if m.Type == "sharing_stopped" {
			t.Error("sharing_stopped should not be recorded in sentLog")
		}
	}
}

func TestBrokerMultipleMessageIDs(t *testing.T) {
	b, d := newBrokerForTest()
	defer b.Stop()

	b.PushText("msg-a", "hello")
	b.PushText("msg-b", "world")

	time.Sleep(500 * time.Millisecond)
	msgs := d.drain()

	ids := map[string]bool{}
	for _, m := range msgs {
		if m.Type == EventText {
			var td TextData
			json.Unmarshal(m.Data, &td)
			ids[td.ID] = true
		}
	}
	if !ids["msg-a"] || !ids["msg-b"] {
		t.Errorf("expected both msg-a and msg-b, got ids: %v", ids)
	}
}

func TestBrokerStop(t *testing.T) {
	b, _ := newBrokerForTest()
	b.Stop()
}

func TestBrokerReplayToClient(t *testing.T) {
	b, d := newBrokerForTest()
	defer b.Stop()

	// Push a few messages to populate the log
	b.PushStatus("running", "go")
	b.PushError("test error")
	time.Sleep(100 * time.Millisecond)
	d.drain()

	// Verify sentLog has entries
	b.logMu.Lock()
	logLen := len(b.sentLog)
	b.logMu.Unlock()
	if logLen == 0 {
		t.Error("sentLog should have entries before replay")
	}
}

func TestBrokerReplayToClientDirect(t *testing.T) {
	// Test ReplayToClient by calling it directly with a session that has a client
	sess := NewSession("wss://test.local")
	rc, _ := NewRelayClient("wss://test.local", "0123456789abcdef0123456789abcdef")
	rc.Close()
	sess.client = rc

	// Build broker manually with senderLoop NOT running
	b := &Broker{
		session:  sess,
		outDone:  make(chan struct{}),
		textBuf:  make(map[string]*textEntry),
		textTick: time.NewTicker(300 * time.Millisecond),
		textDone: make(chan struct{}),
	}
	b.outCond = sync.NewCond(&b.outMu)

	// Add entries to the sent log
	b.logMu.Lock()
	b.sentLog = []GatewayMessage{
		{Seq: 1, Type: "text", Data: json.RawMessage(`{"chunk":"hello"}`)},
		{Seq: 2, Type: "status", Data: json.RawMessage(`{"status":"running"}`)},
	}
	b.logMu.Unlock()

	// ReplayToClient calls session.Send which calls rc.Send (closed, so errors)
	// It should not panic, just log errors via debug.Log
	b.ReplayToClient()
	// If we get here without panic, the test passes
}

func TestBrokerChatClearFlushesTextBuffers(t *testing.T) {
	b, d := newBrokerForTest()
	defer b.Stop()

	b.PushText("msg-x", "pending text")
	b.PushChatClear()
	time.Sleep(100 * time.Millisecond)
	d.drain()

	b.textMu.Lock()
	bufLen := len(b.textBuf)
	b.textMu.Unlock()
	if bufLen != 0 {
		t.Errorf("textBuf should be empty after chat_clear, got %d entries", bufLen)
	}
}

func TestBrokerFlushTextNoEntry(t *testing.T) {
	b, d := newBrokerForTest()
	defer b.Stop()

	// flushText on non-existent entry should be no-op
	b.flushText("nonexistent")
	time.Sleep(50 * time.Millisecond)
	msgs := d.drain()
	if len(msgs) != 0 {
		t.Errorf("expected 0 messages, got %d", len(msgs))
	}
}

func TestBrokerFlushTextEmptyText(t *testing.T) {
	b, d := newBrokerForTest()
	defer b.Stop()

	b.textMu.Lock()
	b.textBuf["msg-e"] = &textEntry{agentID: "", text: ""}
	b.textMu.Unlock()

	b.flushText("msg-e")
	time.Sleep(50 * time.Millisecond)
	msgs := d.drain()
	if len(msgs) != 0 {
		t.Errorf("expected 0 messages for empty text, got %d", len(msgs))
	}
}

func TestBrokerFlushAllTextEmpty(t *testing.T) {
	b, d := newBrokerForTest()
	defer b.Stop()

	// flushAllText with no entries should be no-op
	b.flushAllText()
	time.Sleep(50 * time.Millisecond)
	msgs := d.drain()
	if len(msgs) != 0 {
		t.Errorf("expected 0 messages, got %d", len(msgs))
	}
}

func TestBrokerEnqueueOutDirect(t *testing.T) {
	b, d := newBrokerForTest()
	defer b.Stop()

	msg := GatewayMessage{Seq: 99, Type: "test_type", Data: json.RawMessage(`"hello"`)}
	b.enqueueOut(msg)

	time.Sleep(50 * time.Millisecond)
	msgs := d.drain()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].Seq != 99 || msgs[0].Type != "test_type" {
		t.Errorf("message mismatch: %+v", msgs[0])
	}
}

func TestBrokerEnqueueDirect(t *testing.T) {
	b, d := newBrokerForTest()
	defer b.Stop()

	b.enqueue("custom_event", map[string]string{"key": "value"})
	time.Sleep(50 * time.Millisecond)
	msgs := d.drain()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].Type != "custom_event" {
		t.Errorf("type = %q, want %q", msgs[0].Type, "custom_event")
	}
	if msgs[0].Seq == 0 {
		t.Error("seq should be non-zero")
	}
}
