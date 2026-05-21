package tunnel

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

// Broker bridges agent events and the WebSocket tunnel protocol.
//
// Delivery guarantees:
//   - Text chunks are batched: PushText appends to a per-msgID buffer;
//     a 300ms ticker flushes all accumulated text as a single message.
//   - Every outbound message carries a stable session_id + event_id.
//   - The broker only produces ordered events for the current active session.
//   - Relay owns per-client replay; broker only emits the canonical event stream.
type Broker struct {
	session   *Session
	onCommand func(cmd GatewayMessage)

	// Session-scoped event identity.
	sessionMu sync.RWMutex
	sessionID string
	nextEvent atomic.Int64

	// Send queue: all outbound messages go here.
	// The sender goroutine drains it continuously (no ACK blocking).
	// TCP/WebSocket guarantees ordered delivery.
	outMu    sync.Mutex
	outCond  *sync.Cond
	outbound []GatewayMessage // unbounded queue: enqueue never blocks
	outDone  chan struct{}

	// Text batching
	textMu   sync.Mutex
	textBuf  map[string]*textEntry // msgID → accumulated text entry
	textTick *time.Ticker
	textDone chan struct{} // stop text flusher
}

func NewBroker(sess *Session) *Broker {
	b := &Broker{
		session:   sess,
		sessionID: newTunnelSessionID(),
		outDone:   make(chan struct{}),
		textBuf:   make(map[string]*textEntry),
		textTick:  time.NewTicker(300 * time.Millisecond),
		textDone:  make(chan struct{}),
	}
	b.outCond = sync.NewCond(&b.outMu)

	// Start sender goroutine.
	go b.senderLoop()

	// Start text flush ticker
	go b.textFlushLoop()

	// Handle incoming messages from mobile
	sess.OnMessage(func(msg GatewayMessage) {
		if b.onCommand != nil {
			b.onCommand(msg)
		}
	})

	return b
}

func newTunnelSessionID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err == nil {
		return "sess-" + hex.EncodeToString(buf)
	}
	return fmt.Sprintf("sess-%d", time.Now().UnixNano())
}

// ─── Goroutines ───

// textEntry tracks accumulated text for a message stream.
// agentID == "" means main agent text; otherwise it's a subagent/teammate.
type textEntry struct {
	agentID string
	text    string
}

// senderLoop drains the outbound queue. Never blocks the producer.
func (b *Broker) senderLoop() {
	for {
		b.outMu.Lock()
		for len(b.outbound) == 0 {
			select {
			case <-b.outDone:
				b.outMu.Unlock()
				return
			default:
			}
			b.outCond.Wait()
		}
		batch := b.outbound
		b.outbound = nil
		b.outMu.Unlock()

		for _, msg := range batch {
			if err := b.session.Send(msg); err != nil {
				debug.Log("tunnel", "broker: send %s event=%s failed: %v", msg.Type, msg.EventID, err)
			}
		}
	}
}

// textFlushLoop periodically flushes accumulated text buffers to the sendQueue.
func (b *Broker) textFlushLoop() {
	for {
		select {
		case <-b.textTick.C:
			b.flushAllText()
		case <-b.textDone:
			return
		}
	}
}

// flushAllText sends all accumulated text chunks as a single message per msgID.
func (b *Broker) flushAllText() {
	b.textMu.Lock()
	if len(b.textBuf) == 0 {
		b.textMu.Unlock()
		return
	}
	bufs := make(map[string]*textEntry)
	for k, v := range b.textBuf {
		if v.text != "" {
			bufs[k] = v
		}
	}
	for k := range b.textBuf {
		delete(b.textBuf, k)
	}
	b.textMu.Unlock()

	for msgID, entry := range bufs {
		if entry.agentID != "" {
			b.enqueueWithStream(EventSubagentText, msgID, SubagentTextData{AgentID: entry.agentID, ID: msgID, Chunk: entry.text})
		} else {
			b.enqueueWithStream(EventText, msgID, TextData{ID: msgID, Chunk: entry.text})
		}
	}
}

// flushText flushes the buffer for a specific msgID immediately.
func (b *Broker) flushText(msgID string) {
	b.textMu.Lock()
	entry := b.textBuf[msgID]
	if entry == nil || entry.text == "" {
		b.textMu.Unlock()
		return
	}
	delete(b.textBuf, msgID)
	b.textMu.Unlock()

	if entry.agentID != "" {
		b.enqueueWithStream(EventSubagentText, msgID, SubagentTextData{AgentID: entry.agentID, ID: msgID, Chunk: entry.text})
	} else {
		b.enqueueWithStream(EventText, msgID, TextData{ID: msgID, Chunk: entry.text})
	}
}

// ─── Lifecycle ───

func (b *Broker) OnCommand(fn func(cmd GatewayMessage)) {
	b.onCommand = fn
}

func (b *Broker) Stop() {
	b.textTick.Stop()
	close(b.textDone)
	close(b.outDone)
	b.outCond.Broadcast()
}

func (b *Broker) SessionID() string {
	b.sessionMu.RLock()
	defer b.sessionMu.RUnlock()
	return b.sessionID
}

func (b *Broker) resetSession() string {
	b.sessionMu.Lock()
	defer b.sessionMu.Unlock()
	b.sessionID = newTunnelSessionID()
	b.nextEvent.Store(0)
	return b.sessionID
}

// ─── Session lifecycle ───

func (b *Broker) SendSessionInfo(data SessionInfoData) {
	b.enqueue(EventSessionInfo, data)
}

func (b *Broker) ResetSession() {
	// Clear text buffers too
	b.textMu.Lock()
	b.textBuf = make(map[string]*textEntry)
	b.textMu.Unlock()
	b.resetSession()
	b.enqueue(EventSnapshotReset, nil)
}

func (b *Broker) PushSharingStopped() {
	b.enqueue("sharing_stopped", nil)
}

// ─── User message ───

func (b *Broker) PushUserMessage(text string) {
	b.enqueue(EventUserMessage, map[string]string{"text": text})
}

// ─── Streaming text (batched) ───

func (b *Broker) PushText(id, chunk string) {
	b.textMu.Lock()
	if b.textBuf[id] == nil {
		b.textBuf[id] = &textEntry{agentID: ""}
	}
	b.textBuf[id].text += chunk
	b.textMu.Unlock()
}

func (b *Broker) PushTextDone(id string) {
	// Flush remaining text immediately, then send text_done
	b.flushText(id)
	b.enqueueWithStream(EventTextDone, id, TextData{ID: id, Done: true})
}

// ─── Status ───

func (b *Broker) PushStatus(status, message string) {
	b.enqueue(EventStatus, StatusData{Status: status, Message: message})
}

// ─── Tool calls ───

func (b *Broker) PushToolCall(toolID, toolName, args, detail string) {
	b.enqueueWithStream(EventToolCall, toolID, ToolCallData{ToolID: toolID, ToolName: toolName, Args: args, Detail: detail})
}

func (b *Broker) PushToolResult(toolID, toolName, result string, isError bool) {
	b.enqueueWithStream(EventToolResult, toolID, ToolResultData{ToolID: toolID, ToolName: toolName, Result: result, IsError: isError})
}

// ─── Approval ───

func (b *Broker) PushApprovalRequest(id, toolName, input string) {
	b.enqueueWithStream(EventApprovalRequest, id, ApprovalRequestData{ID: id, ToolName: toolName, Input: input})
}

func (b *Broker) PushApprovalResult(id, decision string) {
	b.enqueueWithStream(EventApprovalResult, id, map[string]string{"id": id, "decision": decision})
}

// ─── Error ───

func (b *Broker) PushError(message string) {
	b.enqueue(EventError, ErrorData{Message: message})
}

// ─── Ask User ───

func (b *Broker) PushAskUserRequest(id, title string, questions []AskUserQuestion) {
	b.enqueueWithStream(EventAskUserRequest, id, AskUserRequestData{ID: id, Title: title, Questions: questions})
}

func (b *Broker) PushAskUserResponse(id, status string, answers []AskUserAnswer) {
	b.enqueueWithStream(EventAskUserResponse, id, AskUserResponseData{ID: id, Status: status, Answers: answers})
}

// ─── Sub-agent / Teammate ───

func (b *Broker) PushSubagentSpawn(agentID, name, task, color, parentID string) {
	b.enqueueWithStream(EventSubagentSpawn, agentID, SubagentSpawnData{
		AgentID: agentID, Name: name, Task: task, Color: color, ParentID: parentID,
	})
}

func (b *Broker) PushSubagentText(agentID, msgID, chunk string, done bool) {
	if !done {
		b.textMu.Lock()
		if b.textBuf[msgID] == nil {
			b.textBuf[msgID] = &textEntry{agentID: agentID}
		}
		b.textBuf[msgID].text += chunk
		b.textMu.Unlock()
	} else {
		b.flushText(msgID)
	}
}

func (b *Broker) PushSubagentStatus(agentID, status, message string) {
	b.enqueueWithStream(EventSubagentStatus, agentID, SubagentStatusData{AgentID: agentID, Status: status, Message: message})
}

func (b *Broker) PushSubagentComplete(agentID, name, summary string, success bool) {
	b.enqueueWithStream(EventSubagentComplete, agentID, SubagentCompleteData{
		AgentID: agentID, Name: name, Summary: summary, Success: success,
	})
}

func (b *Broker) PushSubagentToolCall(agentID, toolID, toolName, args, detail string) {
	streamID := toolID
	if streamID == "" {
		streamID = fmt.Sprintf("%s-tool", agentID)
	}
	b.enqueueWithStream(EventSubagentToolCall, streamID, SubagentToolCallData{
		AgentID: agentID, ToolID: toolID, ToolName: toolName, Args: args, Detail: detail,
	})
}

func (b *Broker) PushSubagentToolResult(agentID, toolID, toolName, result string, isError bool) {
	streamID := toolID
	if streamID == "" {
		streamID = fmt.Sprintf("%s-tool", agentID)
	}
	b.enqueueWithStream(EventSubagentToolResult, streamID, SubagentToolResultData{
		AgentID: agentID, ToolID: toolID, ToolName: toolName, Result: result, IsError: isError,
	})
}

// ─── Utility ───

func (b *Broker) NextMessageID() string {
	return fmt.Sprintf("msg-%d", msgCount.Add(1))
}

var msgCount atomic.Int64

type HistoryEntry struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	// Tool fields (role == "tool_call" or "tool_result")
	ToolID   string `json:"tool_id,omitempty"`
	ToolName string `json:"tool_name,omitempty"`
	ToolArgs string `json:"tool_args,omitempty"`
	Result   string `json:"result,omitempty"`
	IsError  bool   `json:"is_error,omitempty"`
}

func (b *Broker) SeedHistory(messages []HistoryEntry) {
	for _, entry := range messages {
		switch entry.Role {
		case "user":
			if entry.Content != "" {
				b.PushUserMessage(entry.Content)
			}
		case "assistant":
			if entry.Content == "" {
				continue
			}
			msgID := b.NextMessageID()
			b.enqueueWithStream(EventText, msgID, TextData{ID: msgID, Chunk: entry.Content})
			b.enqueueWithStream(EventTextDone, msgID, TextData{ID: msgID, Done: true})
		case "tool_call":
			b.PushToolCall(entry.ToolID, entry.ToolName, entry.ToolArgs, entry.ToolArgs)
		case "tool_result":
			b.PushToolResult(entry.ToolID, entry.ToolName, entry.Result, entry.IsError)
		}
	}
}

// ─── Internal ───

// enqueueOut appends a message to the unbounded outbound queue and wakes the sender.
// This NEVER blocks, so OnUpdate callbacks can call tunnel methods safely.
func (b *Broker) enqueueOut(msg GatewayMessage) {
	b.outMu.Lock()
	b.outbound = append(b.outbound, msg)
	b.outMu.Unlock()
	b.outCond.Signal()
}

// enqueue assigns stable session/event metadata and puts on outbound.
func (b *Broker) enqueue(eventType string, data interface{}) {
	msg := b.newMessage(eventType, "", data)
	if msg.Type == "" {
		return
	}
	b.enqueueOut(msg)
}

func (b *Broker) enqueueWithStream(eventType, streamID string, data interface{}) {
	msg := b.newMessage(eventType, streamID, data)
	if msg.Type == "" {
		return
	}
	b.enqueueOut(msg)
}

func (b *Broker) newMessage(eventType, streamID string, data interface{}) GatewayMessage {
	eventNum := b.nextEvent.Add(1)

	dataBytes, err := json.Marshal(data)
	if err != nil {
		debug.Log("tunnel", "broker: marshal error for %s: %v", eventType, err)
		return GatewayMessage{}
	}

	return GatewayMessage{
		SessionID: b.SessionID(),
		EventID:   fmt.Sprintf("ev-%09d", eventNum),
		StreamID:  streamID,
		Type:      eventType,
		Data:      dataBytes,
	}
}
