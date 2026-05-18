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
// Usage:
//
//	b := NewBroker(sess)
//	b.OnCommand(func(cmd GatewayMessage) { ... handle client commands ... })
//
//	// Push agent events:
//	b.PushText(id, "Hello, ")
//	b.PushText(id, "world!")
//	b.PushTextDone(id)
//	b.PushStatus(StatusRunning, "executing tool")
//	b.PushToolCall("read_file", `{path:"main.go"}`, "read_file(main.go)")
//	b.PushToolResult("read_file", "package main...", false)
//	b.PushApprovalRequest("123", "run_command", "rm -rf /")
type Broker struct {
	session   *Session
	onCommand func(cmd GatewayMessage)
	textMu    sync.Mutex
	msgCount  atomic.Int64
}

// NewBroker creates a broker bound to a tunnel session.
func NewBroker(sess *Session) *Broker {
	b := &Broker{session: sess}
	sess.OnMessage(func(msg GatewayMessage) {
		if b.onCommand != nil {
			b.onCommand(msg)
		}
	})
	return b
}

// OnCommand sets the handler for commands from the mobile client.
func (b *Broker) OnCommand(fn func(cmd GatewayMessage)) {
	b.onCommand = fn
}

// SendSessionInfo sends session metadata to the client.
func (b *Broker) SendSessionInfo(data SessionInfoData) {
	b.send(EventSessionInfo, data)
}

// PushText sends a streaming text chunk.
func (b *Broker) PushText(id, chunk string) {
	b.send(EventText, TextData{ID: id, Chunk: chunk})
}

// PushTextDone signals the end of a text stream.
func (b *Broker) PushTextDone(id string) {
	b.send(EventTextDone, TextData{ID: id, Done: true})
}

// PushStatus sends an agent status change.
func (b *Broker) PushStatus(status, message string) {
	b.send(EventStatus, StatusData{Status: status, Message: message})
}

// PushToolCall sends a tool call notification.
func (b *Broker) PushToolCall(toolName, args, detail string) {
	b.send(EventToolCall, ToolCallData{ToolName: toolName, Args: args, Detail: detail})
}

// PushToolResult sends a tool result.
func (b *Broker) PushToolResult(toolName, result string, isError bool) {
	b.send(EventToolResult, ToolResultData{ToolName: toolName, Result: result, IsError: isError})
}

// PushApprovalRequest sends an approval request to the mobile client.
func (b *Broker) PushApprovalRequest(id, toolName, input string) {
	b.send(EventApprovalRequest, ApprovalRequestData{ID: id, ToolName: toolName, Input: input})
}

// PushApprovalResult sends an approval result back.
func (b *Broker) PushApprovalResult(id, decision string) {
	b.send(EventApprovalResult, map[string]string{"id": id, "decision": decision})
}

// PushError sends an error to the client.
func (b *Broker) PushError(message string) {
	b.send(EventError, ErrorData{Message: message})
}

// ─── Ask User (structured questionnaire) ───

// PushAskUserRequest sends a structured questionnaire to the mobile client.
func (b *Broker) PushAskUserRequest(id, title string, questions []AskUserQuestion) {
	b.send(EventAskUserRequest, AskUserRequestData{ID: id, Title: title, Questions: questions})
}

// PushAskUserResponse sends the user's answers back (confirmation echo).
func (b *Broker) PushAskUserResponse(id, status string, answers []AskUserAnswer) {
	b.send(EventAskUserResponse, AskUserResponseData{ID: id, Status: status, Answers: answers})
}

// ─── Sub-agent / Teammate ───

// PushSubagentSpawn notifies mobile that a sub-agent has been created.
func (b *Broker) PushSubagentSpawn(agentID, name, task, color, parentID string) {
	b.send(EventSubagentSpawn, SubagentSpawnData{
		AgentID: agentID, Name: name, Task: task, Color: color, ParentID: parentID,
	})
}

// PushSubagentText sends streaming text from a sub-agent.
func (b *Broker) PushSubagentText(agentID, msgID, chunk string, done bool) {
	b.send(EventSubagentText, SubagentTextData{AgentID: agentID, ID: msgID, Chunk: chunk, Done: done})
}

// PushSubagentStatus sends a status update for a sub-agent.
func (b *Broker) PushSubagentStatus(agentID, status, message string) {
	b.send(EventSubagentStatus, SubagentStatusData{AgentID: agentID, Status: status, Message: message})
}

// PushSubagentComplete notifies that a sub-agent has finished.
func (b *Broker) PushSubagentComplete(agentID, name, summary string, success bool) {
	b.send(EventSubagentComplete, SubagentCompleteData{
		AgentID: agentID, Name: name, Summary: summary, Success: success,
	})
}

// NextMessageID generates a unique message ID for grouping text chunks.
func (b *Broker) NextMessageID() string {
	return fmt.Sprintf("msg-%d", b.msgCount.Add(1))
}

// send marshals and sends a typed message over the WebSocket.
func (b *Broker) send(eventType string, data interface{}) {
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
		// Client not connected — this is normal if mobile disconnected
		log.Printf("broker: send %s failed: %v", eventType, err)
	}
}
