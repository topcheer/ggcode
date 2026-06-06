package main

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tunnel"
)

type fakeDaemonTunnelBroker struct {
	nextID     int
	calls      []string
	userMsgs   []tunnel.MessageData
	serverAcks []string
}

func (b *fakeDaemonTunnelBroker) NextMessageID() string {
	b.nextID++
	return "msg-" + strconv.Itoa(b.nextID)
}

func (b *fakeDaemonTunnelBroker) PushText(msgID, text string) {
	b.calls = append(b.calls, "text:"+msgID+":"+text)
}

func (b *fakeDaemonTunnelBroker) PushTextDone(msgID string) {
	b.calls = append(b.calls, "text_done:"+msgID)
}

func (b *fakeDaemonTunnelBroker) PushReasoning(msgID, text string) {
	b.calls = append(b.calls, "reasoning:"+msgID+":"+text)
}

func (b *fakeDaemonTunnelBroker) PushReasoningDone(msgID string) {
	b.calls = append(b.calls, "reasoning_done:"+msgID)
}

func (b *fakeDaemonTunnelBroker) PushToolCall(toolID, toolName, displayName, rawArgs, detail string) {
	b.calls = append(b.calls, "tool_call:"+toolID+":"+toolName+":"+displayName)
}

func (b *fakeDaemonTunnelBroker) PushToolResult(toolID, toolName, result string, isError bool) {
	b.calls = append(b.calls, "tool_result:"+toolID+":"+toolName+":"+result)
}

func (b *fakeDaemonTunnelBroker) PushStatus(status, message string) {
	b.calls = append(b.calls, "status:"+status+":"+message)
}

func (b *fakeDaemonTunnelBroker) PushError(message string) {
	b.calls = append(b.calls, "error:"+message)
}

func (b *fakeDaemonTunnelBroker) PushUserMessageData(data tunnel.MessageData) {
	b.userMsgs = append(b.userMsgs, data)
}

func (b *fakeDaemonTunnelBroker) PushServerAck(messageID string) {
	b.serverAcks = append(b.serverAcks, messageID)
}

type fakeDaemonTunnelTarget struct {
	controller     *daemonTunnelShareController
	sent           [][]provider.ContentBlock
	interruptCalls int
	interruptOK    bool
}

func (t *fakeDaemonTunnelTarget) SendUserMessage(content []provider.ContentBlock) {
	t.sent = append(t.sent, content)
	if t.controller != nil {
		t.controller.HandleUserMessage(content)
	}
}

func (t *fakeDaemonTunnelTarget) InterruptActiveRun() bool {
	t.interruptCalls++
	return t.interruptOK
}

func TestDaemonTunnelShareControllerKeepsStableMainStream(t *testing.T) {
	broker := &fakeDaemonTunnelBroker{}
	controller := newDaemonTunnelShareController(broker, nil, tunnel.SessionInfoData{}, nil)

	controller.HandleRunState(true)
	controller.HandleStreamEvent(provider.StreamEvent{Type: provider.StreamEventReasoning, Text: "thinking"})
	controller.HandleStreamEvent(provider.StreamEvent{Type: provider.StreamEventText, Text: "hello"})
	controller.HandleStreamEvent(provider.StreamEvent{Type: provider.StreamEventText, Text: " world"})
	controller.HandleStreamEvent(provider.StreamEvent{
		Type: provider.StreamEventToolCallDone,
		Tool: provider.ToolCallDelta{ID: "tool-1", Name: "read_file", Arguments: json.RawMessage(`{"path":"main.go"}`)},
	})
	controller.HandleStreamEvent(provider.StreamEvent{Type: provider.StreamEventText, Text: "next"})
	controller.HandleStreamEvent(provider.StreamEvent{Type: provider.StreamEventDone})

	got := strings.Join(broker.calls, "\n")
	if !strings.Contains(got, "reasoning:msg-1-reasoning:thinking") {
		t.Fatalf("missing reasoning stream: %s", got)
	}
	if !strings.Contains(got, "text:msg-1:hello") || !strings.Contains(got, "text:msg-1: world") {
		t.Fatalf("expected first round to reuse msg-1: %s", got)
	}
	if !strings.Contains(got, "text_done:msg-1") {
		t.Fatalf("expected first round finalization: %s", got)
	}
	if !strings.Contains(got, "tool_call:tool-1:read_file") {
		t.Fatalf("expected tool call forwarding: %s", got)
	}
	if !strings.Contains(got, "text:msg-2:next") {
		t.Fatalf("expected new round to use msg-2: %s", got)
	}
	if !strings.Contains(got, "text_done:msg-2") {
		t.Fatalf("expected second round finalization: %s", got)
	}
	if !strings.Contains(got, "status:busy:") || !strings.Contains(got, "status:idle:") {
		t.Fatalf("expected busy/idle status transitions: %s", got)
	}
}

func TestDaemonTunnelShareControllerHandlesInboundUserMessage(t *testing.T) {
	broker := &fakeDaemonTunnelBroker{}
	controller := newDaemonTunnelShareController(broker, nil, tunnel.SessionInfoData{}, nil)
	target := &fakeDaemonTunnelTarget{controller: controller}

	payload, err := json.Marshal(tunnel.MessageData{
		MessageID:   "user-123",
		Text:        "hello from mobile",
		DisplayText: "hello",
		Kind:        tunnel.MessageKindShellCommand,
	})
	if err != nil {
		t.Fatal(err)
	}

	controller.HandleCommand(target, tunnel.GatewayMessage{Type: tunnel.CmdMessage, Data: payload})

	if len(target.sent) != 1 || len(target.sent[0]) != 1 || target.sent[0][0].Text != "hello from mobile" {
		t.Fatalf("unexpected forwarded content: %#v", target.sent)
	}
	if len(broker.userMsgs) != 1 {
		t.Fatalf("expected one authoritative user message, got %d", len(broker.userMsgs))
	}
	if broker.userMsgs[0].MessageID != "user-123" || broker.userMsgs[0].DisplayText != "hello" || broker.userMsgs[0].Kind != tunnel.MessageKindShellCommand {
		t.Fatalf("unexpected authoritative user message: %#v", broker.userMsgs[0])
	}
	if len(broker.serverAcks) != 1 || broker.serverAcks[0] != "user-123" {
		t.Fatalf("unexpected server ack: %#v", broker.serverAcks)
	}
}

func TestDaemonTunnelMessagesToHistoryPreservesReasoningAndTools(t *testing.T) {
	history := daemonTunnelMessagesToHistory([]provider.Message{
		{
			Role: "user",
			Content: []provider.ContentBlock{
				{Type: "text", Text: "question"},
			},
		},
		{
			Role: "assistant",
			Content: []provider.ContentBlock{
				{ReasoningContent: "thinking"},
				{Type: "text", Text: "answer"},
				{Type: "tool_use", ToolID: "tool-1", ToolName: "read_file", Input: json.RawMessage(`{"path":"main.go"}`)},
			},
		},
		{
			Role: "tool",
			Content: []provider.ContentBlock{
				{Type: "tool_result", ToolID: "tool-1", ToolName: "read_file", Output: "file contents"},
			},
		},
	})

	if len(history) != 5 {
		t.Fatalf("unexpected history length: %#v", history)
	}
	if history[0].Role != "user" || history[0].Content != "question" {
		t.Fatalf("unexpected user history: %#v", history[0])
	}
	if history[1].Role != "reasoning" || history[1].Content != "thinking" {
		t.Fatalf("unexpected reasoning history: %#v", history[1])
	}
	if history[2].Role != "assistant" || history[2].Content != "answer" {
		t.Fatalf("unexpected assistant history: %#v", history[2])
	}
	if history[3].Role != "tool_call" || history[3].ToolID != "tool-1" {
		t.Fatalf("unexpected tool history: %#v", history[3])
	}
	if history[4].Role != "tool_result" || history[4].ToolID != "tool-1" {
		t.Fatalf("unexpected tool result history: %#v", history[4])
	}
}
