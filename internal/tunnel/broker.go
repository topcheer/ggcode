package tunnel

import (
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
)

// Broker bridges agent events and the WebSocket tunnel protocol.
//
// It maintains a sentLog of all messages pushed to the mobile client.
// On reconnection, ReplayToClient() replays this log so the mobile
// always shows exactly what the desktop shows — regardless of
// session persistence timing.
type Broker struct {
	session         *Session
	onCommand       func(cmd GatewayMessage)
	onClientConnect func()
	sendMu          sync.Mutex // serializes all sends + sentLog access
	msgCount        atomic.Int64
	sentLog         []GatewayMessage // replay log: everything sent since last chat_clear
}

// NewBroker creates a broker bound to a tunnel session.
func NewBroker(sess *Session) *Broker {
	b := &Broker{session: sess}
	sess.OnMessage(func(msg GatewayMessage) {
		dataPreview := string(msg.Data)
		if len(dataPreview) > 200 {
			dataPreview = dataPreview[:200]
		}
		log.Printf("[broker] OnMessage: type=%s data=%s", msg.Type, dataPreview)
		if b.onCommand != nil {
			b.onCommand(msg)
		} else {
			log.Printf("[broker] OnMessage: onCommand is nil, dropping")
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

// OnCommand sets the handler for commands from the mobile client.
func (b *Broker) OnCommand(fn func(cmd GatewayMessage)) {
	b.onCommand = fn
}

// OnClientConnect sets the handler called when a mobile client connects.
func (b *Broker) OnClientConnect(fn func()) {
	b.onClientConnect = fn
}

// ReplayToClient resends all logged messages to the mobile client.
// Called on reconnect to ensure mobile shows the current desktop state.
func (b *Broker) ReplayToClient() {
	b.sendMu.Lock()
	msgs := make([]GatewayMessage, len(b.sentLog))
	copy(msgs, b.sentLog)
	b.sendMu.Unlock()

	log.Printf("[broker] ReplayToClient: %d messages", len(msgs))
	// Always start with chat_clear to reset mobile state
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
	b.send(EventSessionInfo, data)
}

// PushChatClear tells the mobile client to clear its message list.
// Also clears the broker's replay log.
func (b *Broker) PushChatClear() {
	b.sendMu.Lock()
	b.sentLog = nil
	b.sendMu.Unlock()
	b.send("chat_clear", nil)
}

// PushSharingStopped tells mobile clients the server is stopping.
func (b *Broker) PushSharingStopped() {
	b.send("sharing_stopped", nil)
}

// ─── User message ───

func (b *Broker) PushUserMessage(text string) {
	b.send(EventUserMessage, map[string]string{"text": text})
}

// ─── Streaming text ───

func (b *Broker) PushText(id, chunk string) {
	b.send(EventText, TextData{ID: id, Chunk: chunk})
}

func (b *Broker) PushTextDone(id string) {
	b.send(EventTextDone, TextData{ID: id, Done: true})
}

// ─── Status ───

func (b *Broker) PushStatus(status, message string) {
	b.send(EventStatus, StatusData{Status: status, Message: message})
}

// ─── Tool calls ───

func (b *Broker) PushToolCall(toolName, args, detail string) {
	b.send(EventToolCall, ToolCallData{ToolName: toolName, Args: args, Detail: detail})
}

func (b *Broker) PushToolResult(toolName, result string, isError bool) {
	b.send(EventToolResult, ToolResultData{ToolName: toolName, Result: result, IsError: isError})
}

// ─── Approval ───

func (b *Broker) PushApprovalRequest(id, toolName, input string) {
	b.send(EventApprovalRequest, ApprovalRequestData{ID: id, ToolName: toolName, Input: input})
}

func (b *Broker) PushApprovalResult(id, decision string) {
	b.send(EventApprovalResult, map[string]string{"id": id, "decision": decision})
}

// ─── Error ───

func (b *Broker) PushError(message string) {
	b.send(EventError, ErrorData{Message: message})
}

// ─── Ask User ───

func (b *Broker) PushAskUserRequest(id, title string, questions []AskUserQuestion) {
	b.send(EventAskUserRequest, AskUserRequestData{ID: id, Title: title, Questions: questions})
}

func (b *Broker) PushAskUserResponse(id, status string, answers []AskUserAnswer) {
	b.send(EventAskUserResponse, AskUserResponseData{ID: id, Status: status, Answers: answers})
}

// ─── Sub-agent / Teammate ───

func (b *Broker) PushSubagentSpawn(agentID, name, task, color, parentID string) {
	b.send(EventSubagentSpawn, SubagentSpawnData{
		AgentID: agentID, Name: name, Task: task, Color: color, ParentID: parentID,
	})
}

func (b *Broker) PushSubagentText(agentID, msgID, chunk string, done bool) {
	b.send(EventSubagentText, SubagentTextData{AgentID: agentID, ID: msgID, Chunk: chunk, Done: done})
}

func (b *Broker) PushSubagentStatus(agentID, status, message string) {
	b.send(EventSubagentStatus, SubagentStatusData{AgentID: agentID, Status: status, Message: message})
}

func (b *Broker) PushSubagentComplete(agentID, name, summary string, success bool) {
	b.send(EventSubagentComplete, SubagentCompleteData{
		AgentID: agentID, Name: name, Summary: summary, Success: success,
	})
}

// ─── Utility ───

func (b *Broker) NextMessageID() string {
	return fmt.Sprintf("msg-%d", b.msgCount.Add(1))
}

// HistoryEntry represents a single chat message for history replay.
type HistoryEntry struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// PushChatHistory sends the full chat history to the mobile client.
func (b *Broker) PushChatHistory(messages []HistoryEntry) {
	b.send("chat_history", map[string]interface{}{
		"messages": messages,
	})
}

// ─── Internal ───

// send marshals and sends a typed message over the WebSocket.
// Also appends to sentLog for replay on client reconnect.
func (b *Broker) send(eventType string, data interface{}) {
	b.sendMu.Lock()
	defer b.sendMu.Unlock()

	dataBytes, err := json.Marshal(data)
	if err != nil {
		log.Printf("broker: marshal error for %s: %v", eventType, err)
		return
	}
	msg := GatewayMessage{
		Type: eventType,
		Data: dataBytes,
	}
	if err := b.session.Send(msg); err != nil {
		log.Printf("broker: send %s failed: %v", eventType, err)
	} else {
		log.Printf("[broker] send %s OK (%d bytes)", eventType, len(dataBytes))
	}

	// Record for replay (skip control messages that don't affect display)
	switch eventType {
	case "sharing_stopped":
		// don't log
	default:
		b.sentLog = append(b.sentLog, msg)
	}
}
