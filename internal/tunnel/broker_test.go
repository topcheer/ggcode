//go:build !integration

package tunnel

import (
	"encoding/json"
	"fmt"
	"strings"
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
		session:           sess,
		sessionGeneration: 1,
		outDone:           make(chan struct{}),
		textBuf:           make(map[string]*textEntry),
		activeText:        make(map[string]*textEntry),
		textTick:          time.NewTicker(300 * time.Millisecond),
		textDone:          make(chan struct{}),
		sendWaiters:       make(map[string]chan struct{}),
		toolArgs:          make(map[string]string),
		subagentToolArgs:  make(map[string]string),
	}
	b.outCond = sync.NewCond(&b.outMu)
	b.projectionCond = sync.NewCond(&b.projectionMu)

	// Do NOT start senderLoop - we'll drain manually
	// Do NOT start textFlushLoop - we'll flush manually or wait for ticker

	// Start only the text flush loop (so batched text tests work)
	go b.textFlushLoop()

	d.b = b
	return b, d
}

type drainHelper struct {
	b    *Broker
	msgs []GatewayMessage
	mu   sync.Mutex
}

func mustMarshalJSON(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return data
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

func TestBrokerSwitchSessionWithoutRelaySession(t *testing.T) {
	b, _ := newBrokerForTest()
	defer b.Stop()
	b.session = nil

	b.SwitchSession("sess-1")
	b.AnnounceActiveSession("sess-1")

	got, _ := b.sessionState()
	if got != "sess-1" {
		t.Fatalf("current session id = %q, want sess-1", got)
	}
}

func TestBrokerOnRelayConnected(t *testing.T) {
	b, _ := newBrokerForTest()
	defer b.Stop()

	var got RelayConnectedState
	b.OnRelayConnected(func(info RelayConnectedState) {
		got = info
	})

	b.handleRelayConnected(RelayConnectedState{Role: "client", SessionID: "sess-1", HistoryCount: 3})

	if got.Role != "client" || got.SessionID != "sess-1" || got.HistoryCount != 3 {
		t.Fatalf("relay connected callback mismatch: %+v", got)
	}
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

func TestBrokerPushReasoningDone(t *testing.T) {
	b, d := newBrokerForTest()
	defer b.Stop()

	b.PushReasoning("msg-r1", "thinking")
	b.PushReasoningDone("msg-r1")
	time.Sleep(50 * time.Millisecond)
	msgs := d.drain()

	if len(msgs) != 2 {
		t.Fatalf("expected 2 reasoning messages, got %d", len(msgs))
	}
	if msgs[0].Type != EventReasoning {
		t.Fatalf("expected first event %q, got %q", EventReasoning, msgs[0].Type)
	}
	if msgs[1].Type != EventReasoningDone {
		t.Fatalf("expected second event %q, got %q", EventReasoningDone, msgs[1].Type)
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
		Theme:     "midnight",
	})
	time.Sleep(50 * time.Millisecond)
	msgs := d.drain()

	found := false
	for _, m := range msgs {
		if m.Type == EventSessionInfo {
			found = true
			var data SessionInfoData
			json.Unmarshal(m.Data, &data)
			if data.Workspace != "/tmp/project" || data.Model != "gpt-4" || data.Theme != "midnight" {
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

func TestBrokerPushSystemMessage(t *testing.T) {
	b, d := newBrokerForTest()
	defer b.Stop()

	b.PushSystemMessage("still waiting")
	time.Sleep(50 * time.Millisecond)
	msgs := d.drain()

	found := false
	for _, m := range msgs {
		if m.Type == EventSystemMessage {
			found = true
			var data MessageData
			if err := json.Unmarshal(m.Data, &data); err != nil {
				t.Fatal(err)
			}
			if data.Text != "still waiting" {
				t.Fatalf("system text = %q, want %q", data.Text, "still waiting")
			}
		}
	}
	if !found {
		t.Fatal("expected system_message event")
	}
}

func TestBrokerPushUserMessageData(t *testing.T) {
	b, d := newBrokerForTest()
	defer b.Stop()

	b.PushUserMessageData(MessageData{
		Text:        "run scheduled check",
		DisplayText: "⏰ Cron job triggered",
		Kind:        "cron",
	})
	time.Sleep(50 * time.Millisecond)
	msgs := d.drain()

	for _, m := range msgs {
		if m.Type != EventUserMessage {
			continue
		}
		var data MessageData
		if err := json.Unmarshal(m.Data, &data); err != nil {
			t.Fatalf("unmarshal user_message data: %v", err)
		}
		if data.Text != "run scheduled check" || data.DisplayText != "⏰ Cron job triggered" || data.Kind != "cron" {
			t.Fatalf("unexpected user_message data: %+v", data)
		}
		return
	}
	t.Fatal("expected user_message event")
}

func TestBrokerPushTextDataPreservesKind(t *testing.T) {
	b, d := newBrokerForTest()
	defer b.Stop()

	b.PushTextData(TextData{ID: "shell-1", Chunk: "\x1b[31mfail\x1b[0m\n", Kind: MessageKindShellOutput})
	b.flushText("shell-1")
	time.Sleep(50 * time.Millisecond)
	msgs := d.drain()

	for _, m := range msgs {
		if m.Type != EventText {
			continue
		}
		var data TextData
		if err := json.Unmarshal(m.Data, &data); err != nil {
			t.Fatalf("unmarshal text data: %v", err)
		}
		if data.ID != "shell-1" || data.Kind != MessageKindShellOutput {
			t.Fatalf("unexpected text data: %+v", data)
		}
		return
	}
	t.Fatal("expected text event")
}

func TestBrokerPushToolCall(t *testing.T) {
	b, d := newBrokerForTest()
	defer b.Stop()

	b.PushToolCall("t1", "read_file", "Inspect config", `{"path":"/tmp"}`, "reading file")
	time.Sleep(50 * time.Millisecond)
	msgs := d.drain()

	found := false
	for _, m := range msgs {
		if m.Type == EventToolCall {
			found = true
			var td ToolCallData
			json.Unmarshal(m.Data, &td)
			if td.ToolID != "t1" || td.ToolName != "read_file" || td.DisplayName != "Inspect config" {
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

func TestBrokerPushTaskToolResultIncludesStructuredFields(t *testing.T) {
	b, d := newBrokerForTest()
	defer b.Stop()

	b.PushToolCall("t-task", "task_get", "Task", `{"taskId":"task-1"}`, "task-1")
	b.PushToolResult("t-task", "task_get", `{"id":"task-1","subject":"Fix parity","status":"in_progress"}`, false)
	time.Sleep(50 * time.Millisecond)
	msgs := d.drain()

	for _, m := range msgs {
		if m.Type != EventToolResult {
			continue
		}
		var td ToolResultData
		json.Unmarshal(m.Data, &td)
		if td.ToolID != "t-task" {
			continue
		}
		if td.Summary != "Fix parity [in progress] — task-1" {
			t.Fatalf("unexpected summary: %+v", td)
		}
		if td.PayloadMode != "task_fields" || !strings.Contains(td.Payload, "Task ID: task-1") {
			t.Fatalf("unexpected payload: %+v", td)
		}
		return
	}
	t.Fatal("expected structured tool_result event")
}

func TestBrokerPushCronCreateResultIncludesStructuredFields(t *testing.T) {
	b, d := newBrokerForTest()
	defer b.Stop()

	b.PushToolCall("t-cron", "cron_create", "Schedule Job", `{"cron":"*/5 * * * *","prompt":"check status"}`, "*/5 * * * *")
	b.PushToolResult("t-cron", "cron_create", `{"ID":"job-1","CronExpr":"*/5 * * * *","Prompt":"check status","Recurring":true,"NextFire":"2026-05-24T17:30:00+08:00"}`, false)
	time.Sleep(50 * time.Millisecond)
	msgs := d.drain()

	for _, m := range msgs {
		if m.Type != EventToolResult {
			continue
		}
		var td ToolResultData
		json.Unmarshal(m.Data, &td)
		if td.ToolID != "t-cron" {
			continue
		}
		if td.Summary != "Scheduled */5 * * * * — job-1" {
			t.Fatalf("unexpected summary: %+v", td)
		}
		if td.PayloadMode != "cron_job" || !strings.Contains(td.Payload, "Prompt: check status") {
			t.Fatalf("unexpected payload: %+v", td)
		}
		return
	}
	t.Fatal("expected structured cron tool_result event")
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

func TestBrokerPushSubagentReasoningDone(t *testing.T) {
	b, d := newBrokerForTest()
	defer b.Stop()

	b.PushSubagentReasoning("agent-1", "reasoning-1", "plan", false)
	b.PushSubagentReasoning("agent-1", "reasoning-1", "", true)
	time.Sleep(50 * time.Millisecond)
	msgs := d.drain()

	if len(msgs) != 2 {
		t.Fatalf("expected 2 subagent reasoning messages, got %d", len(msgs))
	}
	if msgs[0].Type != EventSubagentReasoning {
		t.Fatalf("expected first event %q, got %q", EventSubagentReasoning, msgs[0].Type)
	}
	if msgs[1].Type != EventSubagentReasoningDone {
		t.Fatalf("expected second event %q, got %q", EventSubagentReasoningDone, msgs[1].Type)
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

	b.PushSubagentToolCall("agent-1", "t1", "search", "Find TODOs", `{"pattern":"TODO"}`, "searching")
	time.Sleep(50 * time.Millisecond)
	msgs := d.drain()

	found := false
	for _, m := range msgs {
		if m.Type == EventSubagentToolCall {
			found = true
			var td SubagentToolCallData
			json.Unmarshal(m.Data, &td)
			if td.DisplayName != "Find TODOs" {
				t.Errorf("subagent tool call mismatch: %+v", td)
			}
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

func TestBrokerResetSession(t *testing.T) {
	b, d := newBrokerForTest()
	defer b.Stop()

	// First push something to the log
	b.PushStatus("running", "go")
	time.Sleep(50 * time.Millisecond)
	if len(d.drain()) == 0 {
		t.Fatal("expected outbound event before reset")
	}

	// Clear should reset the log and enqueue snapshot_reset with a new session id.
	oldSessionID := b.SessionID()
	b.ResetSession()
	time.Sleep(50 * time.Millisecond)
	msgs := d.drain()

	found := false
	for _, m := range msgs {
		if m.Type == EventSnapshotReset {
			found = true
			if m.SessionID == "" || m.SessionID == oldSessionID {
				t.Fatalf("expected rotated session id after reset, got old=%q new=%q", oldSessionID, m.SessionID)
			}
			if m.EventID != "" {
				t.Fatalf("snapshot_reset should not consume an event id, got %q", m.EventID)
			}
		}
	}
	if !found {
		t.Error("expected snapshot_reset event")
	}

}

func TestBrokerSeedHistory(t *testing.T) {
	b, d := newBrokerForTest()
	defer b.Stop()

	b.SeedHistory([]HistoryEntry{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi there"},
	})
	time.Sleep(50 * time.Millisecond)
	msgs := d.drain()

	foundUser := false
	foundAssistant := false
	for _, m := range msgs {
		if m.Type == EventUserMessage {
			foundUser = true
		}
		if m.Type == EventText {
			foundAssistant = true
		}
	}
	if !foundUser || !foundAssistant {
		t.Errorf("expected synthesized history events, got user=%t assistant=%t", foundUser, foundAssistant)
	}
}

func TestBrokerSeedHistoryPreservesToolDetail(t *testing.T) {
	b, d := newBrokerForTest()
	defer b.Stop()

	b.SeedHistory([]HistoryEntry{{
		Role:       "tool_call",
		ToolID:     "t1",
		ToolName:   "run_command",
		ToolArgs:   `{"command":"cd /tmp && go test ./..."}`,
		ToolDetail: "cd /tmp && go test ./...",
	}})
	time.Sleep(50 * time.Millisecond)
	msgs := d.drain()
	if len(msgs) != 1 || msgs[0].Type != EventToolCall {
		t.Fatalf("expected one tool_call event, got %+v", msgs)
	}
	var data ToolCallData
	if err := json.Unmarshal(msgs[0].Data, &data); err != nil {
		t.Fatal(err)
	}
	if data.Detail != "cd /tmp && go test ./..." {
		t.Fatalf("tool detail = %q, want reconstructed detail", data.Detail)
	}
}

func TestBrokerSeedHistoryLargeBurstDoesNotDrop(t *testing.T) {
	sess := NewSession("wss://test.local")
	rc, err := NewRelayClient("wss://test.local", "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4")
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	sess.client = rc

	b := NewBroker(sess)
	defer b.Stop()

	const historyCount = 400
	history := make([]HistoryEntry, 0, historyCount)
	for i := 0; i < historyCount; i++ {
		history = append(history, HistoryEntry{Role: "user", Content: "hello"})
	}

	done := make(chan int, 1)
	go func() {
		count := 0
		for count < historyCount {
			<-rc.sendCh
			count++
		}
		done <- count
	}()

	b.SeedHistory(history)

	select {
	case count := <-done:
		if count != historyCount {
			t.Fatalf("expected %d relayed history events, got %d", historyCount, count)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for %d relayed history events", historyCount)
	}
}

func TestBrokerHandleRelayConnectedReseedsSnapshotWhenRelayStateLost(t *testing.T) {
	b, d := newBrokerForTest()
	defer b.Stop()
	b.sessionID = "sess-local"
	b.SetSnapshotProvider(func() BrokerSnapshot {
		return BrokerSnapshot{
			SessionInfo: SessionInfoData{Workspace: "/tmp/project", Version: "dev"},
			History: []HistoryEntry{
				{Role: "user", Content: "hello"},
				{Role: "assistant", Content: "world"},
			},
		}
	})

	b.handleRelayConnected(RelayConnectedState{Role: "server"})
	time.Sleep(50 * time.Millisecond)
	msgs := d.drain()

	if len(msgs) < 3 {
		t.Fatalf("expected reseeded snapshot messages, got %d", len(msgs))
	}
	if msgs[0].Type != EventSessionInfo {
		t.Fatalf("expected first event session_info, got %q", msgs[0].Type)
	}
}

func TestBrokerSendSnapshotIncludesExtraEvents(t *testing.T) {
	b, d := newBrokerForTest()
	defer b.Stop()

	b.SendSnapshot(BrokerSnapshot{
		SessionInfo: SessionInfoData{Workspace: "/tmp/project", Version: "dev"},
		ExtraEvents: []SnapshotEvent{
			{
				Type:     EventSubagentSpawn,
				StreamID: "agent-1",
				Data:     json.RawMessage(`{"agent_id":"agent-1","name":"Researcher","task":"teammate"}`),
			},
		},
		Status: StatusData{Status: "running", Message: "processing"},
	})

	msgs := d.drain()
	if len(msgs) != 3 {
		t.Fatalf("expected session_info + extra event + status, got %d", len(msgs))
	}
	if msgs[1].Type != EventSubagentSpawn {
		t.Fatalf("expected extra snapshot event to be replayed, got %q", msgs[1].Type)
	}
	if msgs[2].Type != EventStatus {
		t.Fatalf("expected final status event, got %q", msgs[2].Type)
	}
}

func TestBrokerHandleRelayConnectedReseedsCurrentStatusAfterHistory(t *testing.T) {
	b, d := newBrokerForTest()
	defer b.Stop()
	b.sessionID = "sess-local"
	b.PushStatus("running", "read_file")
	time.Sleep(50 * time.Millisecond)
	d.drain()
	b.SetSnapshotProvider(func() BrokerSnapshot {
		return BrokerSnapshot{
			SessionInfo: SessionInfoData{Workspace: "/tmp/project", Version: "dev"},
			History: []HistoryEntry{
				{Role: "user", Content: "hello"},
				{Role: "assistant", Content: "world"},
			},
		}
	})

	b.handleRelayConnected(RelayConnectedState{Role: "server"})
	time.Sleep(50 * time.Millisecond)
	msgs := d.drain()

	if len(msgs) < 4 {
		t.Fatalf("expected reseeded snapshot plus status, got %d", len(msgs))
	}
	var status StatusData
	found := false
	for _, msg := range msgs {
		if msg.Type != EventStatus {
			continue
		}
		if err := json.Unmarshal(msg.Data, &status); err != nil {
			t.Fatalf("unmarshal status: %v", err)
		}
		found = true
		break
	}
	if !found {
		types := make([]string, 0, len(msgs))
		for _, msg := range msgs {
			types = append(types, msg.Type)
		}
		t.Fatalf("expected reseeded status event, got types %+v", types)
	}
	if status.Status != "running" || status.Message != "read_file" {
		t.Fatalf("unexpected reseeded status: %+v", status)
	}
}

func TestBrokerServerReconnectReplaysInFlightTextAfterSnapshot(t *testing.T) {
	b, d := newBrokerForTest()
	defer b.Stop()
	b.sessionID = "sess-local"
	b.SetSnapshotProvider(func() BrokerSnapshot {
		return BrokerSnapshot{
			SessionInfo: SessionInfoData{Workspace: "/tmp/project", Version: "dev"},
			History:     []HistoryEntry{{Role: "user", Content: "question"}},
			Status:      StatusData{Status: "thinking", Message: "processing"},
		}
	})
	b.PushText("msg-live", "partial answer")
	b.flushAllText()
	d.drain()

	b.handleRelayConnected(RelayConnectedState{Role: "server", SessionID: "", HistoryCount: 0})
	time.Sleep(50 * time.Millisecond)
	msgs := d.drain()

	if len(msgs) < 4 {
		t.Fatalf("expected snapshot plus in-flight text, got %d", len(msgs))
	}
	last := msgs[len(msgs)-1]
	if last.Type != EventText {
		t.Fatalf("expected in-flight text after server reconnect snapshot, got %q", last.Type)
	}
	var data TextData
	if err := json.Unmarshal(last.Data, &data); err != nil {
		t.Fatal(err)
	}
	if data.ID != "msg-live" || data.Chunk != "partial answer" {
		t.Fatalf("unexpected in-flight text replay: %+v", data)
	}
	if last.SessionID != "sess-local" {
		t.Fatalf("server reconnect should keep local session id, got %q", last.SessionID)
	}
}

func TestBrokerHandleRelayConnectedSkipsReseedWhenRelayStateRetained(t *testing.T) {
	b, d := newBrokerForTest()
	defer b.Stop()
	b.sessionID = "sess-local"
	b.SetSnapshotProvider(func() BrokerSnapshot {
		return BrokerSnapshot{
			SessionInfo: SessionInfoData{Workspace: "/tmp/project", Version: "dev"},
			History:     []HistoryEntry{{Role: "user", Content: "hello"}},
		}
	})

	b.handleRelayConnected(RelayConnectedState{
		Role:         "server",
		SessionID:    "sess-local",
		HistoryCount: 2,
	})
	time.Sleep(50 * time.Millisecond)
	msgs := d.drain()
	if len(msgs) != 0 {
		t.Fatalf("expected no reseed when relay state retained, got %d messages", len(msgs))
	}
}

func TestBrokerHandleRelayConnectedRetainedStateAdvancesEventCursor(t *testing.T) {
	b, d := newBrokerForTest()
	defer b.Stop()
	b.sessionID = "sess-local"

	b.handleRelayConnected(RelayConnectedState{
		Role:         "server",
		SessionID:    "sess-local",
		HistoryCount: 3,
		LastEventID:  "ev-000000003",
	})
	time.Sleep(50 * time.Millisecond)
	if msgs := d.drain(); len(msgs) != 0 {
		t.Fatalf("expected no reseed traffic, got %d messages", len(msgs))
	}

	b.PushSystemMessage("cursor advanced")
	time.Sleep(50 * time.Millisecond)
	msgs := d.drain()
	if len(msgs) != 1 {
		t.Fatalf("expected one live event after retained-state reconnect, got %d", len(msgs))
	}
	if msgs[0].EventID != "ev-000000004" {
		t.Fatalf("expected next event id to continue relay history, got %q", msgs[0].EventID)
	}
}

func TestBrokerHandleFirstClientConnectedPublishesAuthoritativeSnapshotWhenStateRetained(t *testing.T) {
	b, d := newBrokerForTest()
	defer b.Stop()
	b.sessionID = "sess-local"
	b.SetSnapshotProvider(func() BrokerSnapshot {
		return BrokerSnapshot{
			SessionInfo: SessionInfoData{Workspace: "/tmp/project", Version: "dev"},
			History: []HistoryEntry{
				{Role: "user", Content: "hello"},
				{Role: "assistant", Content: "world"},
			},
			Status: StatusData{Status: "idle", Message: "Ready"},
		}
	})

	b.handleRelayConnected(RelayConnectedState{
		Role:         "client",
		SessionID:    "sess-local",
		HistoryCount: 1,
	})
	time.Sleep(50 * time.Millisecond)
	msgs := d.drain()
	if len(msgs) < 5 {
		t.Fatalf("expected first retained-state client to receive authoritative snapshot, got %+v", msgs)
	}
	if msgs[0].Type != EventSnapshotReset {
		t.Fatalf("expected first event snapshot_reset, got %q", msgs[0].Type)
	}
}

func TestBrokerHandleAdditionalClientConnectedReseedsWhenLocalReplayIsEmpty(t *testing.T) {
	b, d := newBrokerForTest()
	defer b.Stop()
	b.sessionID = "sess-local"
	b.clientProjectionSeeded.Store(true)
	b.SetSnapshotProvider(func() BrokerSnapshot {
		return BrokerSnapshot{
			SessionInfo: SessionInfoData{Workspace: "/tmp/project", Version: "dev"},
			History: []HistoryEntry{
				{Role: "user", Content: "hello"},
				{Role: "assistant", Content: "world"},
			},
			Status: StatusData{Status: "idle", Message: "Ready"},
		}
	})
	b.SetReplayProvider(func() []GatewayMessage {
		return nil
	})

	b.handleRelayConnected(RelayConnectedState{
		Role:         "client",
		SessionID:    "sess-local",
		HistoryCount: 1,
	})
	time.Sleep(50 * time.Millisecond)
	msgs := d.drain()

	if len(msgs) != 6 {
		t.Fatalf("expected authoritative snapshot reseed when local replay is empty, got %+v", msgs)
	}
	if msgs[0].Type != EventSnapshotReset {
		t.Fatalf("expected snapshot_reset first, got %q", msgs[0].Type)
	}
	if msgs[1].Type != EventSessionInfo {
		t.Fatalf("expected session_info after snapshot_reset, got %q", msgs[1].Type)
	}
}

func TestBrokerHandleClientConnectedFlushesBufferedTextWhenStateRetained(t *testing.T) {
	b, d := newBrokerForTest()
	defer b.Stop()
	b.sessionID = "sess-local"
	b.clientProjectionSeeded.Store(true)
	b.PushText("msg-live", "partial answer")

	b.handleRelayConnected(RelayConnectedState{
		Role:         "client",
		SessionID:    "sess-local",
		HistoryCount: 1,
	})
	time.Sleep(50 * time.Millisecond)
	msgs := d.drain()

	if len(msgs) != 1 {
		t.Fatalf("expected only buffered live text to flush, got %d messages", len(msgs))
	}
	if msgs[0].Type != EventText {
		t.Fatalf("expected retained-state connect to flush text, got %q", msgs[0].Type)
	}
	var data TextData
	if err := json.Unmarshal(msgs[0].Data, &data); err != nil {
		t.Fatal(err)
	}
	if data.ID != "msg-live" || data.Chunk != "partial answer" {
		t.Fatalf("unexpected flushed live text: %+v", data)
	}
}

func TestBrokerHandleClientConnectedReseedsWhenLastEventDiffers(t *testing.T) {
	b, d := newBrokerForTest()
	defer b.Stop()
	b.sessionID = "sess-local"
	b.SetSnapshotProvider(func() BrokerSnapshot {
		return BrokerSnapshot{
			SessionInfo: SessionInfoData{Workspace: "/tmp/project", Version: "dev"},
			Status:      StatusData{Status: "idle", Message: "Ready"},
		}
	})
	b.SetReplayProvider(func() []GatewayMessage {
		raw, err := json.Marshal(TextData{
			ID:    "msg-1",
			Chunk: "fresh reply",
			Done:  false,
		})
		if err != nil {
			t.Fatalf("marshal text data: %v", err)
		}
		return []GatewayMessage{
			{
				Type:     EventText,
				EventID:  "ev-000000002",
				StreamID: "msg-1",
				Data:     raw,
			},
		}
	})

	b.handleRelayConnected(RelayConnectedState{
		Role:         "client",
		SessionID:    "sess-local",
		HistoryCount: 1,
		LastEventID:  "ev-000000001",
	})
	time.Sleep(50 * time.Millisecond)
	msgs := d.drain()

	if len(msgs) != 2 {
		t.Fatalf("expected authoritative replay after stale relay history, got %d messages", len(msgs))
	}
	if msgs[0].Type != EventSnapshotReset {
		t.Fatalf("expected snapshot_reset first, got %q", msgs[0].Type)
	}
	if msgs[1].Type != EventText || msgs[1].EventID != "ev-000000002" {
		t.Fatalf("expected canonical text replay, got %+v", msgs[1])
	}
}

func TestBrokerHandleClientConnectedRepublishesCanonicalReplayWhenRelayHistoryIsIncomplete(t *testing.T) {
	b, d := newBrokerForTest()
	defer b.Stop()
	b.sessionID = "sess-local"
	infoJSON, err := json.Marshal(SessionInfoData{Workspace: "/tmp/project", Version: "dev"})
	if err != nil {
		t.Fatal(err)
	}
	textJSON, err := json.Marshal(TextData{ID: "msg-1", Chunk: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	b.SetSnapshotProvider(func() BrokerSnapshot {
		return BrokerSnapshot{
			SessionInfo: SessionInfoData{Workspace: "/tmp/project", Version: "dev"},
			Status:      StatusData{Status: "idle", Message: "Ready"},
		}
	})
	b.SetReplayProvider(func() []GatewayMessage {
		return []GatewayMessage{
			{
				SessionID: "sess-local",
				EventID:   "ev-000000001",
				Type:      EventSessionInfo,
				Data:      infoJSON,
			},
			{
				SessionID: "sess-local",
				EventID:   "ev-000000002",
				Type:      EventText,
				StreamID:  "msg-1",
				Data:      textJSON,
			},
		}
	})

	b.handleRelayConnected(RelayConnectedState{
		Role:         "client",
		SessionID:    "sess-local",
		HistoryCount: 1,
	})
	time.Sleep(50 * time.Millisecond)
	msgs := d.drain()

	if len(msgs) != 3 {
		t.Fatalf("expected snapshot reset plus canonical replay, got %d messages", len(msgs))
	}
	if msgs[0].Type != EventSnapshotReset {
		t.Fatalf("expected first event snapshot_reset, got %q", msgs[0].Type)
	}
	if msgs[1].Type != EventSessionInfo {
		t.Fatalf("expected session_info replay after reset, got %q", msgs[1].Type)
	}
	if msgs[2].Type != EventText {
		t.Fatalf("expected text replay after session_info, got %q", msgs[2].Type)
	}
}

func TestBrokerHandleLegacyClientConnectedSendsActiveSessionWithoutAuthoritativeReplay(t *testing.T) {
	b, d := newBrokerForTest()
	defer b.Stop()
	b.sessionID = "sess-local"
	activeClient, err := NewRelayClient("wss://test.local", "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4")
	if err != nil {
		t.Fatal(err)
	}
	b.session.client = activeClient
	infoJSON, err := json.Marshal(SessionInfoData{Workspace: "/tmp/project", Version: "dev"})
	if err != nil {
		t.Fatal(err)
	}
	textJSON, err := json.Marshal(TextData{ID: "msg-1", Chunk: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	b.SetSnapshotProvider(func() BrokerSnapshot {
		return BrokerSnapshot{
			SessionInfo: SessionInfoData{Workspace: "/tmp/project", Version: "dev"},
		}
	})
	b.SetReplayProvider(func() []GatewayMessage {
		return []GatewayMessage{
			{
				SessionID: "sess-local",
				EventID:   "ev-000000001",
				Type:      EventSessionInfo,
				Data:      infoJSON,
			},
			{
				SessionID: "sess-local",
				EventID:   "ev-000000002",
				Type:      EventText,
				StreamID:  "msg-1",
				Data:      textJSON,
			},
		}
	})

	b.handleRelayConnected(RelayConnectedState{
		Role:            "client",
		SessionID:       "sess-local",
		HistoryCount:    0,
		ProtocolVersion: ShareProtocolLegacy,
	})

	time.Sleep(10 * time.Millisecond)
	if msgs := d.drain(); len(msgs) != 0 {
		t.Fatalf("expected no authoritative replay before legacy resume completes, got %+v", msgs)
	}
	select {
	case raw := <-activeClient.sendCh:
		t.Fatalf("expected no active_session before legacy resume completes, got %s", raw)
	default:
	}

	b.handleRelayConnected(RelayConnectedState{
		Role:            "client",
		SessionID:       "sess-local",
		HistoryCount:    0,
		ProtocolVersion: ShareProtocolLegacy,
		ResumeComplete:  true,
	})

	time.Sleep(10 * time.Millisecond)
	if msgs := d.drain(); len(msgs) != 0 {
		t.Fatalf("expected legacy clients to rely on relay history without authoritative replay, got %+v", msgs)
	}
	select {
	case raw := <-activeClient.sendCh:
		var msg struct {
			Type      string `json:"type"`
			SessionID string `json:"session_id"`
		}
		if err := json.Unmarshal(raw, &msg); err != nil {
			t.Fatalf("unmarshal relay payload: %v", err)
		}
		if msg.Type != EventActiveSession || msg.SessionID != "sess-local" {
			t.Fatalf("expected active_session for legacy client replay path, got %+v", msg)
		}
	default:
		t.Fatal("expected active_session for legacy client replay path")
	}
}

func TestBrokerHandleClientConnectedReplaysQueuedDistinctConnectAfterInFlightReplay(t *testing.T) {
	b, d := newBrokerForTest()
	defer b.Stop()
	b.sessionID = "sess-local"
	infoJSON, err := json.Marshal(SessionInfoData{Workspace: "/tmp/project", Version: "dev"})
	if err != nil {
		t.Fatal(err)
	}
	textJSON, err := json.Marshal(TextData{ID: "msg-1", Chunk: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	b.SetSnapshotProvider(func() BrokerSnapshot {
		return BrokerSnapshot{
			SessionInfo: SessionInfoData{Workspace: "/tmp/project", Version: "dev"},
		}
	})
	started := make(chan struct{})
	release := make(chan struct{})
	var replayCalls int
	var replayMu sync.Mutex
	b.SetReplayProvider(func() []GatewayMessage {
		replayMu.Lock()
		replayCalls++
		call := replayCalls
		replayMu.Unlock()
		if call == 1 {
			close(started)
			<-release
		}
		return []GatewayMessage{
			{
				SessionID: "sess-local",
				EventID:   "ev-000000001",
				Type:      EventSessionInfo,
				Data:      infoJSON,
			},
			{
				SessionID: "sess-local",
				EventID:   "ev-000000002",
				Type:      EventText,
				StreamID:  "msg-1",
				Data:      textJSON,
			},
		}
	})

	b.handleRelayConnected(RelayConnectedState{
		Role:            "client",
		SessionID:       "sess-local",
		HistoryCount:    0,
		ProtocolVersion: ShareProtocolV2,
	})
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first client replay to start")
	}
	b.handleRelayConnected(RelayConnectedState{
		Role:            "client",
		SessionID:       "sess-local",
		HistoryCount:    1,
		LastEventID:     "ev-remote-tail",
		ProtocolVersion: ShareProtocolV2,
	})
	close(release)

	time.Sleep(20 * time.Millisecond)
	msgs := d.drain()
	if len(msgs) != 6 {
		t.Fatalf("expected replay for original and queued client connects, got %+v", msgs)
	}
	resetCount := 0
	for _, msg := range msgs {
		if msg.Type == EventSnapshotReset {
			resetCount++
		}
	}
	if resetCount != 2 {
		t.Fatalf("expected two snapshot resets for distinct queued client connects, got %d in %+v", resetCount, msgs)
	}
}

func TestBrokerHandleClientConnectedCoalescesDuplicateAuthoritativeSnapshots(t *testing.T) {
	b, d := newBrokerForTest()
	defer b.Stop()
	b.sessionID = "sess-local"
	b.SetSnapshotProvider(func() BrokerSnapshot {
		return BrokerSnapshot{
			SessionInfo: SessionInfoData{Workspace: "/tmp/project", Version: "dev"},
			History: []HistoryEntry{
				{Role: "system", Content: "Starting tunnel..."},
				{Role: "assistant", Content: "done"},
			},
			Status: StatusData{Status: "idle", Message: "Ready"},
		}
	})
	b.SetReplayProvider(func() []GatewayMessage { return nil })

	info := RelayConnectedState{Role: "client", SessionID: "sess-local", HistoryCount: 0}
	b.handleRelayConnected(info)
	b.handleRelayConnected(info)
	time.Sleep(50 * time.Millisecond)

	msgs := d.drain()
	resetCount := 0
	for _, msg := range msgs {
		if msg.Type == EventSnapshotReset {
			resetCount++
		}
	}
	if resetCount != 1 {
		t.Fatalf("expected exactly one snapshot_reset for duplicate client-connect replay, got %d in %+v", resetCount, msgs)
	}
}

func TestBrokerHandleClientConnectedCoalescesDuplicateCanonicalReplay(t *testing.T) {
	b, d := newBrokerForTest()
	defer b.Stop()
	b.sessionID = "sess-local"
	infoJSON, err := json.Marshal(SessionInfoData{Workspace: "/tmp/project", Version: "dev"})
	if err != nil {
		t.Fatal(err)
	}
	textJSON, err := json.Marshal(TextData{ID: "msg-1", Chunk: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	b.SetSnapshotProvider(func() BrokerSnapshot {
		return BrokerSnapshot{
			SessionInfo: SessionInfoData{Workspace: "/tmp/project", Version: "dev"},
			Status:      StatusData{Status: "idle", Message: "Ready"},
		}
	})
	b.SetReplayProvider(func() []GatewayMessage {
		return []GatewayMessage{
			{
				SessionID: "sess-local",
				EventID:   "ev-000000001",
				Type:      EventSessionInfo,
				Data:      infoJSON,
			},
			{
				SessionID: "sess-local",
				EventID:   "ev-000000002",
				Type:      EventText,
				StreamID:  "msg-1",
				Data:      textJSON,
			},
		}
	})

	info := RelayConnectedState{Role: "client", SessionID: "sess-local", HistoryCount: 1}
	b.handleRelayConnected(info)
	b.handleRelayConnected(info)
	time.Sleep(50 * time.Millisecond)

	msgs := d.drain()
	resetCount := 0
	for _, msg := range msgs {
		if msg.Type == EventSnapshotReset {
			resetCount++
		}
	}
	if resetCount != 1 {
		t.Fatalf("expected exactly one snapshot_reset for duplicate canonical replay, got %d in %+v", resetCount, msgs)
	}
	if len(msgs) != 3 {
		t.Fatalf("expected one canonical replay batch, got %d messages: %+v", len(msgs), msgs)
	}
}

func TestBrokerHandleClientConnectedPublishesAuthoritativeSnapshotWhenRoomEmpty(t *testing.T) {
	b, d := newBrokerForTest()
	defer b.Stop()
	b.sessionID = "sess-local"
	b.SetSnapshotProvider(func() BrokerSnapshot {
		return BrokerSnapshot{
			SessionInfo: SessionInfoData{Workspace: "/tmp/project", Version: "dev"},
			History: []HistoryEntry{
				{Role: "user", Content: "hello"},
				{Role: "assistant", Content: "world"},
			},
			Status: StatusData{Status: "idle", Message: "Ready"},
		}
	})

	b.handleRelayConnected(RelayConnectedState{
		Role:         "client",
		SessionID:    "sess-local",
		HistoryCount: 0,
	})
	time.Sleep(50 * time.Millisecond)
	msgs := d.drain()

	if len(msgs) < 5 {
		t.Fatalf("expected snapshot reset plus authoritative snapshot, got %d messages", len(msgs))
	}
	if msgs[0].Type != EventSnapshotReset {
		t.Fatalf("expected first event snapshot_reset, got %q", msgs[0].Type)
	}
	if msgs[0].SessionID != "sess-local" {
		t.Fatalf("expected client snapshot to keep active session id, got %q", msgs[0].SessionID)
	}
	if msgs[1].Type != EventSessionInfo {
		t.Fatalf("expected session_info after snapshot_reset, got %q", msgs[1].Type)
	}
	if msgs[len(msgs)-1].Type != EventStatus {
		t.Fatalf("expected status after snapshot history, got %q", msgs[len(msgs)-1].Type)
	}
}

func TestBrokerHandleClientConnectedDropsStaleSnapshotAfterSessionSwitch(t *testing.T) {
	b, d := newBrokerForTest()
	defer b.Stop()
	b.sessionID = "sess-local"
	b.sessionGeneration = 1

	b.SetSnapshotProvider(func() BrokerSnapshot {
		return BrokerSnapshot{
			SessionInfo: SessionInfoData{Workspace: "/tmp/project", Version: "dev"},
		}
	})
	releaseReplay := make(chan struct{})
	b.SetReplayProvider(func() []GatewayMessage {
		<-releaseReplay
		return []GatewayMessage{
			{
				SessionID: "sess-local",
				EventID:   "ev-000000001",
				Type:      EventSystemMessage,
				Data:      mustMarshalJSON(MessageData{Text: "stale replay"}),
			},
		}
	})

	b.handleRelayConnected(RelayConnectedState{
		Role:         "client",
		SessionID:    "sess-local",
		HistoryCount: 0,
	})
	b.SwitchSession("sess-next")
	d.drain()
	close(releaseReplay)

	time.Sleep(50 * time.Millisecond)
	if msgs := d.drain(); len(msgs) != 0 {
		t.Fatalf("expected stale snapshot replay to be dropped after session switch, got %+v", msgs)
	}
}

func TestBrokerClientConnectedReplaysInFlightTextAfterSnapshot(t *testing.T) {
	b, d := newBrokerForTest()
	defer b.Stop()
	b.sessionID = "sess-local"
	b.SetSnapshotProvider(func() BrokerSnapshot {
		return BrokerSnapshot{
			SessionInfo: SessionInfoData{Workspace: "/tmp/project", Version: "dev"},
			History:     []HistoryEntry{{Role: "user", Content: "question"}},
			Status:      StatusData{Status: "thinking", Message: "processing"},
		}
	})

	b.PushText("msg-live", "partial answer")
	b.flushAllText()
	d.drain()

	b.handleRelayConnected(RelayConnectedState{Role: "client", SessionID: "sess-local", HistoryCount: 0})
	time.Sleep(50 * time.Millisecond)
	msgs := d.drain()

	if len(msgs) < 5 || msgs[0].Type != EventSnapshotReset {
		t.Fatalf("expected reset, snapshot, and in-flight text, got %+v", msgs)
	}
	last := msgs[len(msgs)-1]
	if last.Type != EventText {
		t.Fatalf("expected in-flight text after snapshot, got %q", last.Type)
	}
	var data TextData
	if err := json.Unmarshal(last.Data, &data); err != nil {
		t.Fatal(err)
	}
	if data.ID != "msg-live" || data.Chunk != "partial answer" {
		t.Fatalf("unexpected in-flight text replay: %+v", data)
	}
	if last.SessionID != msgs[0].SessionID {
		t.Fatalf("in-flight text should use reset session id, got reset=%q text=%q", msgs[0].SessionID, last.SessionID)
	}
}

func TestBrokerClientConnectedSerializesConcurrentLiveEventsAfterSnapshot(t *testing.T) {
	b, d := newBrokerForTest()
	defer b.Stop()
	b.sessionID = "sess-local"
	b.SetSnapshotProvider(func() BrokerSnapshot {
		return BrokerSnapshot{
			SessionInfo: SessionInfoData{Workspace: "/tmp/project", Version: "dev"},
			History: []HistoryEntry{
				{Role: "user", Content: "during tool call"},
				{Role: "tool_call", ToolID: "tool-1", ToolName: "bash", ToolDisplayName: "Run bash", ToolArgs: `{"command":"sleep 1"}`},
			},
			Status: StatusData{Status: "busy", Message: "running"},
		}
	})

	b.handleRelayConnected(RelayConnectedState{
		Role:         "client",
		SessionID:    "sess-local",
		HistoryCount: 0,
	})
	b.PushToolResult("tool-1", "bash", "done", false)
	b.PushText("msg-after-tool", "follow-up text")
	b.PushTextDone("msg-after-tool")

	time.Sleep(50 * time.Millisecond)
	msgs := d.drain()

	if len(msgs) < 8 {
		t.Fatalf("expected snapshot plus live tool/text events, got %+v", msgs)
	}
	resetIdx := -1
	toolResultIdx := -1
	textIdx := -1
	textDoneIdx := -1
	for i, msg := range msgs {
		switch msg.Type {
		case EventSnapshotReset:
			resetIdx = i
		case EventToolResult:
			toolResultIdx = i
		case EventText:
			var data TextData
			if err := json.Unmarshal(msg.Data, &data); err != nil {
				t.Fatalf("unmarshal text: %v", err)
			}
			if data.ID == "msg-after-tool" {
				textIdx = i
			}
		case EventTextDone:
			var data TextData
			if err := json.Unmarshal(msg.Data, &data); err != nil {
				t.Fatalf("unmarshal text_done: %v", err)
			}
			if data.ID == "msg-after-tool" {
				textDoneIdx = i
			}
		}
	}
	if resetIdx != 0 {
		t.Fatalf("expected snapshot reset first, got index %d with msgs %+v", resetIdx, msgs)
	}
	if toolResultIdx <= resetIdx {
		t.Fatalf("expected live tool result after snapshot sync, got reset=%d tool_result=%d msgs=%+v", resetIdx, toolResultIdx, msgs)
	}
	if textIdx <= toolResultIdx {
		t.Fatalf("expected live follow-up text after tool result, got tool_result=%d text=%d msgs=%+v", toolResultIdx, textIdx, msgs)
	}
	if textDoneIdx <= textIdx {
		t.Fatalf("expected text_done after text, got text=%d text_done=%d msgs=%+v", textIdx, textDoneIdx, msgs)
	}
}

func TestBrokerEventIDsIncrease(t *testing.T) {
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
		if msgs[i].EventID <= msgs[i-1].EventID {
			t.Errorf("event ids not increasing: msgs[%d].EventID=%s <= msgs[%d].EventID=%s",
				i, msgs[i].EventID, i-1, msgs[i-1].EventID)
		}
	}
}

func TestBrokerPushSharingStoppedKeepsOutboundOrder(t *testing.T) {
	b, d := newBrokerForTest()
	defer b.Stop()

	b.PushSharingStopped()
	b.PushStatus("running", "go")
	time.Sleep(100 * time.Millisecond)
	msgs := d.drain()

	if len(msgs) != 2 {
		t.Fatalf("expected two outbound messages, got %d", len(msgs))
	}
	if msgs[0].Type != "sharing_stopped" || msgs[1].Type != EventStatus {
		t.Fatalf("unexpected outbound ordering: %+v", msgs)
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
	b.Stop()
}

func TestBrokerStopSharingGracefullySendsStopEventBeforeClosing(t *testing.T) {
	sess := NewSession("wss://test.local")
	rc, err := NewRelayClient("wss://test.local", "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4")
	if err != nil {
		t.Fatal(err)
	}
	sess.client = rc

	b := NewBroker(sess)
	b.PushStatus("running", "go")
	b.StopSharingGracefully(200 * time.Millisecond)

	if !rc.closed {
		t.Fatal("relay client should be closed after graceful broker stop")
	}

	var gatewayTypes []string
	var controlType string
	for i := 0; i < 3; i++ {
		select {
		case raw := <-rc.sendCh:
			var meta struct {
				Type       string `json:"type"`
				Nonce      string `json:"nonce"`
				Ciphertext string `json:"ciphertext"`
			}
			if err := json.Unmarshal(raw, &meta); err != nil {
				t.Fatal(err)
			}
			if meta.Type == "stop_sharing" {
				controlType = meta.Type
				continue
			}
			plain, err := rc.crypto.Decrypt(meta.Nonce, meta.Ciphertext)
			if err != nil {
				t.Fatal(err)
			}
			var msg GatewayMessage
			if err := json.Unmarshal(plain, &msg); err != nil {
				t.Fatal(err)
			}
			gatewayTypes = append(gatewayTypes, msg.Type)
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for relayed message %d", i)
		}
	}

	if len(gatewayTypes) != 2 || gatewayTypes[0] != EventStatus || gatewayTypes[1] != "sharing_stopped" {
		t.Fatalf("unexpected graceful shutdown ordering: %v", gatewayTypes)
	}
	if controlType != "stop_sharing" {
		t.Fatalf("expected stop_sharing control message, got %q", controlType)
	}
}

func TestBrokerChatClearFlushesTextBuffers(t *testing.T) {
	b, d := newBrokerForTest()
	defer b.Stop()

	b.PushText("msg-x", "pending text")
	b.ResetSession()
	time.Sleep(100 * time.Millisecond)
	d.drain()

	b.textMu.Lock()
	bufLen := len(b.textBuf)
	b.textMu.Unlock()
	if bufLen != 0 {
		t.Errorf("textBuf should be empty after snapshot_reset, got %d entries", bufLen)
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

	msg := GatewayMessage{EventID: "ev-manual", Type: "test_type", Data: json.RawMessage(`"hello"`)}
	b.enqueueOut(msg)

	time.Sleep(50 * time.Millisecond)
	msgs := d.drain()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].EventID != "ev-manual" || msgs[0].Type != "test_type" {
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
	if msgs[0].EventID == "" {
		t.Error("event_id should be non-empty")
	}
}

func TestBrokerReplayEventsPreservesExistingIDs(t *testing.T) {
	b, d := newBrokerForTest()
	defer b.Stop()
	b.SwitchSession("sess-ledger")
	d.drain()

	b.ReplayEvents([]GatewayMessage{
		{SessionID: "sess-ledger", EventID: "ev-000000007", Type: EventUserMessage, Data: json.RawMessage(`{"text":"hello"}`)},
		{SessionID: "sess-ledger", EventID: "ev-000000008", StreamID: "msg-1", Type: EventText, Data: json.RawMessage(`{"id":"msg-1","chunk":"world"}`)},
	}, false)
	time.Sleep(50 * time.Millisecond)
	msgs := d.drain()

	if len(msgs) != 2 {
		t.Fatalf("expected 2 replayed messages, got %d", len(msgs))
	}
	if msgs[0].EventID != "ev-000000007" || msgs[1].EventID != "ev-000000008" {
		t.Fatalf("replayed event ids changed: %+v", msgs)
	}

	b.PushStatus("idle", "")
	time.Sleep(50 * time.Millisecond)
	msgs = d.drain()
	if len(msgs) != 1 || msgs[0].EventID != "ev-000000009" {
		t.Fatalf("expected next event id to continue after replayed events, got %+v", msgs)
	}
}

func TestBrokerReplayResetDoesNotConsumeEventOrdinal(t *testing.T) {
	b, d := newBrokerForTest()
	defer b.Stop()
	b.SwitchSession("sess-ledger")
	d.drain()

	b.ReplayEvents([]GatewayMessage{
		{SessionID: "sess-ledger", EventID: "ev-000000007", Type: EventUserMessage, Data: json.RawMessage(`{"text":"hello"}`)},
		{SessionID: "sess-ledger", EventID: "ev-000000008", StreamID: "msg-1", Type: EventText, Data: json.RawMessage(`{"id":"msg-1","chunk":"world"}`)},
	}, true)
	time.Sleep(50 * time.Millisecond)
	msgs := d.drain()
	if len(msgs) != 3 {
		t.Fatalf("expected snapshot reset plus 2 replayed messages, got %d", len(msgs))
	}
	if msgs[0].Type != EventSnapshotReset || msgs[0].EventID != "" {
		t.Fatalf("expected snapshot_reset without event id, got %+v", msgs[0])
	}
	if msgs[1].EventID != "ev-000000007" || msgs[2].EventID != "ev-000000008" {
		t.Fatalf("replayed event ids changed after reset: %+v", msgs)
	}

	b.PushStatus("idle", "")
	time.Sleep(50 * time.Millisecond)
	msgs = d.drain()
	if len(msgs) != 1 || msgs[0].EventID != "ev-000000009" {
		t.Fatalf("expected next live event to continue after replay max, got %+v", msgs)
	}
}

func TestBrokerEventsCarryStableSessionMetadata(t *testing.T) {
	b, d := newBrokerForTest()
	defer b.Stop()

	b.PushText("msg-1", "hello")
	b.PushTextDone("msg-1")
	time.Sleep(200 * time.Millisecond)
	msgs := d.drain()
	if len(msgs) == 0 {
		t.Fatal("expected broker messages")
	}

	sessionID := b.SessionID()
	for _, msg := range msgs {
		if msg.SessionID != sessionID {
			t.Fatalf("session_id = %q, want %q", msg.SessionID, sessionID)
		}
		if msg.EventID == "" {
			t.Fatalf("expected event_id on %+v", msg)
		}
		if msg.Type == EventText || msg.Type == EventTextDone {
			if msg.StreamID != "msg-1" {
				t.Fatalf("stream_id = %q, want msg-1", msg.StreamID)
			}
		}
	}
}

func TestBrokerPushChatClearRotatesSession(t *testing.T) {
	b, d := newBrokerForTest()
	defer b.Stop()

	oldSessionID := b.SessionID()
	b.PushStatus("running", "before")
	time.Sleep(50 * time.Millisecond)
	d.drain()

	b.ResetSession()
	time.Sleep(50 * time.Millisecond)
	msgs := d.drain()
	if len(msgs) == 0 {
		t.Fatal("expected snapshot_reset after chat clear")
	}

	reset := msgs[len(msgs)-1]
	if reset.Type != EventSnapshotReset {
		t.Fatalf("expected snapshot_reset, got %q", reset.Type)
	}
	if reset.SessionID == "" || reset.SessionID == oldSessionID {
		t.Fatalf("expected rotated session_id, got old=%q new=%q", oldSessionID, reset.SessionID)
	}
}

func TestPushServerAck(t *testing.T) {
	b, _ := newBrokerForTest()

	b.PushServerAck("msg-123")
	msgs := b.drainHelperForTest()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].Type != EventServerAck {
		t.Errorf("expected type %q, got %q", EventServerAck, msgs[0].Type)
	}
	var ackData AckData
	if err := json.Unmarshal(msgs[0].Data, &ackData); err != nil {
		t.Fatal(err)
	}
	if ackData.MessageID != "msg-123" {
		t.Errorf("expected message_id=msg-123, got %q", ackData.MessageID)
	}

	// Empty message_id should not produce any message.
	b.PushServerAck("")
	b.outMu.Lock()
	extra := b.outbound
	b.outMu.Unlock()
	if len(extra) != 0 {
		t.Errorf("empty message_id should not produce a message, got %d", len(extra))
	}
}

func TestConcurrentEnqueueStrictlyOrdered(t *testing.T) {
	// Verify that event IDs in the outbound queue are strictly increasing
	// even when multiple goroutines enqueue concurrently.
	b, _ := newBrokerForTest()
	defer b.Stop()

	const goroutines = 8
	const eventsPerGoroutine = 200
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := 0; g < goroutines; g++ {
		go func(gid int) {
			defer wg.Done()
			for i := 0; i < eventsPerGoroutine; i++ {
				// Mix different event types to exercise all enqueue paths.
				switch i % 3 {
				case 0:
					b.PushText(fmt.Sprintf("msg-%d-%d", gid, i), "chunk")
				case 1:
					b.PushToolCall(
						fmt.Sprintf("t-%d-%d", gid, i),
						"run_command",
						fmt.Sprintf("desc-%d-%d", gid, i),
						`{"command":"echo"}`,
						"desc",
					)
				case 2:
					b.PushUserMessage(fmt.Sprintf("user-msg-%d-%d", gid, i))
				}
			}
		}(g)
	}

	// Also drive the textFlushLoop so text chunks get flushed concurrently.
	// The ticker is already running (300ms). Wait for all goroutines to finish
	// then give the flush loop time to drain.
	wg.Wait()
	time.Sleep(500 * time.Millisecond)

	// Collect all outbound messages.
	b.outMu.Lock()
	msgs := make([]GatewayMessage, len(b.outbound))
	copy(msgs, b.outbound)
	b.outMu.Unlock()

	// Filter to events that have an EventID (skip snapshot_reset etc.)
	var ordered []GatewayMessage
	for _, m := range msgs {
		if m.EventID != "" {
			ordered = append(ordered, m)
		}
	}
	if len(ordered) < goroutines*eventsPerGoroutine/3 {
		t.Fatalf("expected at least %d events with IDs, got %d",
			goroutines*eventsPerGoroutine/3, len(ordered))
	}

	// Verify strictly increasing event IDs.
	for i := 1; i < len(ordered); i++ {
		prev := ordered[i-1].EventID
		cur := ordered[i].EventID
		if cur <= prev {
			t.Fatalf("event IDs not strictly increasing at index %d: %s >= %s",
				i, prev, cur)
		}
	}
}

func (b *Broker) drainHelperForTest() []GatewayMessage {
	b.outMu.Lock()
	msgs := b.outbound
	b.outbound = nil
	b.outMu.Unlock()
	return msgs
}
