package tunnel

import (
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"
)

// Broker bridges agent events and the WebSocket tunnel protocol.
//
// Delivery guarantees:
//   - Text chunks are batched: PushText appends to a per-msgID buffer;
//     a 300ms ticker flushes all accumulated text as a single message.
//   - Every outbound message carries a monotonically-increasing Seq.
//   - A sender goroutine sends one message at a time and waits for the
//     mobile client to ACK (by seq) before sending the next.
//   - If ACK times out (5s), the next message is sent anyway.
//   - ReplayToClient bypasses ACK flow control for fast reconnect.
type Broker struct {
	session         *Session
	onCommand       func(cmd GatewayMessage)
	onClientConnect func()

	// Sequencing
	nextSeq atomic.Int64

	// ACK flow control
	pendingAck chan int64 // sender goroutine waits here for ack seq
	ackTimeout time.Duration

	// Send queue: all outbound messages go here.
	// The sender goroutine drains it one-at-a-time, waiting for ACK.
	sendQueue chan GatewayMessage

	// Text batching
	textMu   sync.Mutex
	textBuf  map[string]string // msgID → accumulated text chunks
	textTick *time.Ticker
	textDone chan struct{} // stop text flusher

	// Replay log
	logMu   sync.Mutex
	sentLog []GatewayMessage
}

func NewBroker(sess *Session) *Broker {
	b := &Broker{
		session:    sess,
		pendingAck: make(chan int64, 1),
		ackTimeout: 5 * time.Second,
		sendQueue:  make(chan GatewayMessage, 1000),
		textBuf:    make(map[string]string),
		textTick:   time.NewTicker(300 * time.Millisecond),
		textDone:   make(chan struct{}),
	}

	// Start sender goroutine (ACK flow control)
	go b.senderLoop()

	// Start text flush ticker
	go b.textFlushLoop()

	// Handle incoming messages from mobile
	sess.OnMessage(func(msg GatewayMessage) {
		if msg.Type == EventAck {
			// Extract seq from data
			var ackData struct {
				Seq int64 `json:"seq"`
			}
			if msg.Data != nil {
				json.Unmarshal(msg.Data, &ackData)
			}
			select {
			case b.pendingAck <- ackData.Seq:
			default:
			}
			return
		}
		if b.onCommand != nil {
			b.onCommand(msg)
		}
	})

	sess.OnConnect(func() {
		log.Printf("[broker] client connected, replaying %d messages", len(b.sentLog))
		if b.onClientConnect != nil {
			b.onClientConnect()
		}
	})

	return b
}

// ─── Goroutines ───

// senderLoop drains sendQueue one message at a time, waiting for ACK after each.
func (b *Broker) senderLoop() {
	for msg := range b.sendQueue {
		if err := b.session.Send(msg); err != nil {
			log.Printf("[broker] send %s seq=%d failed: %v", msg.Type, msg.Seq, err)
			continue
		}
		// Wait for ACK (with timeout)
		if msg.Seq > 0 {
			select {
			case <-b.pendingAck:
			case <-time.After(b.ackTimeout):
				log.Printf("[broker] ACK timeout for seq=%d, proceeding", msg.Seq)
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
	bufs := make(map[string]string)
	for k, v := range b.textBuf {
		if v != "" {
			bufs[k] = v
		}
	}
	// Clear the buffer
	for k := range b.textBuf {
		delete(b.textBuf, k)
	}
	b.textMu.Unlock()

	for msgID, text := range bufs {
		seq := b.nextSeq.Add(1)
		data, _ := json.Marshal(TextData{ID: msgID, Chunk: text})
		msg := GatewayMessage{Seq: seq, Type: EventText, Data: data}
		b.recordLog(msg)
		b.sendQueue <- msg
	}
}

// flushText flushes the buffer for a specific msgID immediately.
func (b *Broker) flushText(msgID string) {
	b.textMu.Lock()
	text := b.textBuf[msgID]
	if text == "" {
		b.textMu.Unlock()
		return
	}
	delete(b.textBuf, msgID)
	b.textMu.Unlock()

	seq := b.nextSeq.Add(1)
	data, _ := json.Marshal(TextData{ID: msgID, Chunk: text})
	msg := GatewayMessage{Seq: seq, Type: EventText, Data: data}
	b.recordLog(msg)
	b.sendQueue <- msg
}

// ─── Lifecycle ───

func (b *Broker) OnCommand(fn func(cmd GatewayMessage)) {
	b.onCommand = fn
}

func (b *Broker) OnClientConnect(fn func()) {
	b.onClientConnect = fn
}

func (b *Broker) Stop() {
	b.textTick.Stop()
	close(b.textDone)
	close(b.sendQueue)
}

// ReplayToClient resends all logged messages, bypassing ACK flow control.
func (b *Broker) ReplayToClient() {
	b.logMu.Lock()
	msgs := make([]GatewayMessage, len(b.sentLog))
	copy(msgs, b.sentLog)
	b.logMu.Unlock()

	log.Printf("[broker] ReplayToClient: %d messages", len(msgs))
	// Always start with chat_clear
	clearMsg := GatewayMessage{Type: "chat_clear"}
	if err := b.session.Send(clearMsg); err != nil {
		log.Printf("[broker] replay chat_clear failed: %v", err)
		return
	}
	for _, msg := range msgs {
		if err := b.session.Send(msg); err != nil {
			log.Printf("[broker] replay send %s failed: %v", msg.Type, err)
			return
		}
	}
}

// ─── Session lifecycle ───

func (b *Broker) SendSessionInfo(data SessionInfoData) {
	b.enqueue(EventSessionInfo, data)
}

func (b *Broker) PushChatClear() {
	b.logMu.Lock()
	b.sentLog = nil
	b.logMu.Unlock()
	// Clear text buffers too
	b.textMu.Lock()
	b.textBuf = make(map[string]string)
	b.textMu.Unlock()
	b.enqueue("chat_clear", nil)
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
	b.textBuf[id] += chunk
	b.textMu.Unlock()
}

func (b *Broker) PushTextDone(id string) {
	// Flush remaining text immediately, then send text_done
	b.flushText(id)
	b.enqueue(EventTextDone, TextData{ID: id, Done: true})
}

// ─── Status ───

func (b *Broker) PushStatus(status, message string) {
	b.enqueue(EventStatus, StatusData{Status: status, Message: message})
}

// ─── Tool calls ───

func (b *Broker) PushToolCall(toolName, args, detail string) {
	b.enqueue(EventToolCall, ToolCallData{ToolName: toolName, Args: args, Detail: detail})
}

func (b *Broker) PushToolResult(toolName, result string, isError bool) {
	b.enqueue(EventToolResult, ToolResultData{ToolName: toolName, Result: result, IsError: isError})
}

// ─── Approval ───

func (b *Broker) PushApprovalRequest(id, toolName, input string) {
	b.enqueue(EventApprovalRequest, ApprovalRequestData{ID: id, ToolName: toolName, Input: input})
}

func (b *Broker) PushApprovalResult(id, decision string) {
	b.enqueue(EventApprovalResult, map[string]string{"id": id, "decision": decision})
}

// ─── Error ───

func (b *Broker) PushError(message string) {
	b.enqueue(EventError, ErrorData{Message: message})
}

// ─── Ask User ───

func (b *Broker) PushAskUserRequest(id, title string, questions []AskUserQuestion) {
	b.enqueue(EventAskUserRequest, AskUserRequestData{ID: id, Title: title, Questions: questions})
}

func (b *Broker) PushAskUserResponse(id, status string, answers []AskUserAnswer) {
	b.enqueue(EventAskUserResponse, AskUserResponseData{ID: id, Status: status, Answers: answers})
}

// ─── Sub-agent / Teammate ───

func (b *Broker) PushSubagentSpawn(agentID, name, task, color, parentID string) {
	b.enqueue(EventSubagentSpawn, SubagentSpawnData{
		AgentID: agentID, Name: name, Task: task, Color: color, ParentID: parentID,
	})
}

func (b *Broker) PushSubagentText(agentID, msgID, chunk string, done bool) {
	// Subagent text also gets batched like main text
	if !done {
		b.textMu.Lock()
		b.textBuf[msgID] += chunk
		b.textMu.Unlock()
	} else {
		b.flushText(msgID)
	}
}

func (b *Broker) PushSubagentStatus(agentID, status, message string) {
	b.enqueue(EventSubagentStatus, SubagentStatusData{AgentID: agentID, Status: status, Message: message})
}

func (b *Broker) PushSubagentComplete(agentID, name, summary string, success bool) {
	b.enqueue(EventSubagentComplete, SubagentCompleteData{
		AgentID: agentID, Name: name, Summary: summary, Success: success,
	})
}

func (b *Broker) PushSubagentToolCall(agentID, toolName, args, detail string) {
	b.enqueue(EventSubagentToolCall, SubagentToolCallData{
		AgentID: agentID, ToolName: toolName, Args: args, Detail: detail,
	})
}

func (b *Broker) PushSubagentToolResult(agentID, toolName, result string, isError bool) {
	b.enqueue(EventSubagentToolResult, SubagentToolResultData{
		AgentID: agentID, ToolName: toolName, Result: result, IsError: isError,
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
}

func (b *Broker) PushChatHistory(messages []HistoryEntry) {
	b.enqueue("chat_history", map[string]interface{}{
		"messages": messages,
	})
}

// ─── Internal ───

// enqueue assigns a seq, marshals, records to sentLog, and puts on sendQueue.
func (b *Broker) enqueue(eventType string, data interface{}) {
	seq := b.nextSeq.Add(1)

	dataBytes, err := json.Marshal(data)
	if err != nil {
		log.Printf("broker: marshal error for %s: %v", eventType, err)
		return
	}
	msg := GatewayMessage{
		Seq:  seq,
		Type: eventType,
		Data: dataBytes,
	}
	b.recordLog(msg)
	b.sendQueue <- msg
}

// recordLog appends to sentLog for replay.
func (b *Broker) recordLog(msg GatewayMessage) {
	switch msg.Type {
	case "sharing_stopped":
		// don't log
	default:
		b.logMu.Lock()
		b.sentLog = append(b.sentLog, msg)
		b.logMu.Unlock()
	}
}
