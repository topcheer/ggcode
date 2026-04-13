package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	ctxpkg "github.com/topcheer/ggcode/internal/context"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

type mockTool struct {
	name   string
	result tool.Result
}

func (t mockTool) Name() string                { return t.name }
func (t mockTool) Description() string         { return "mock tool" }
func (t mockTool) Parameters() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (t mockTool) Execute(ctx context.Context, input json.RawMessage) (tool.Result, error) {
	return t.result, nil
}

type blockingTool struct {
	name     string
	started  chan struct{}
	executed *int
}

func (t blockingTool) Name() string                { return t.name }
func (t blockingTool) Description() string         { return "blocking tool" }
func (t blockingTool) Parameters() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (t blockingTool) Execute(ctx context.Context, input json.RawMessage) (tool.Result, error) {
	if t.executed != nil {
		*t.executed = *t.executed + 1
	}
	if t.started != nil {
		select {
		case <-t.started:
		default:
			close(t.started)
		}
	}
	<-ctx.Done()
	return tool.Result{Content: ctx.Err().Error(), IsError: true}, nil
}

type countingTool struct {
	name     string
	executed *int
}

func (t countingTool) Name() string                { return t.name }
func (t countingTool) Description() string         { return "counting tool" }
func (t countingTool) Parameters() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (t countingTool) Execute(ctx context.Context, input json.RawMessage) (tool.Result, error) {
	if t.executed != nil {
		*t.executed = *t.executed + 1
	}
	return tool.Result{Content: "ok"}, nil
}

// mockProvider is a simple mock for testing agent basics.
type mockProvider struct {
	chatResp      *provider.ChatResponse
	chatResponses []*provider.ChatResponse
	chatErr       error
	streamEvents  [][]provider.StreamEvent
	streamErr     error
	tokenCount    int
	chatCalls     int
	streamCalls   int
}

func (m *mockProvider) Chat(ctx context.Context, messages []provider.Message, tools []provider.ToolDefinition) (*provider.ChatResponse, error) {
	m.chatCalls++
	if len(m.chatResponses) > 0 {
		resp := m.chatResponses[0]
		m.chatResponses = m.chatResponses[1:]
		return resp, m.chatErr
	}
	return m.chatResp, m.chatErr
}

func (m *mockProvider) ChatStream(ctx context.Context, messages []provider.Message, tools []provider.ToolDefinition) (<-chan provider.StreamEvent, error) {
	m.streamCalls++
	if m.streamErr != nil {
		return nil, m.streamErr
	}
	var events []provider.StreamEvent
	switch {
	case len(m.streamEvents) > 0:
		events = m.streamEvents[0]
		m.streamEvents = m.streamEvents[1:]
	case len(m.chatResponses) > 0:
		resp := m.chatResponses[0]
		m.chatResponses = m.chatResponses[1:]
		events = streamEventsFromResponse(resp)
	case m.chatResp != nil:
		events = streamEventsFromResponse(m.chatResp)
	}
	ch := make(chan provider.StreamEvent, len(events))
	for _, event := range events {
		ch <- event
	}
	close(ch)
	return ch, nil
}

func (m *mockProvider) CountTokens(ctx context.Context, messages []provider.Message) (int, error) {
	return m.tokenCount, nil
}

func (m *mockProvider) Name() string { return "mock" }

func streamEventsFromResponse(resp *provider.ChatResponse) []provider.StreamEvent {
	if resp == nil {
		return nil
	}
	events := make([]provider.StreamEvent, 0, len(resp.Message.Content)+1)
	for i, block := range resp.Message.Content {
		switch block.Type {
		case "text":
			events = append(events, provider.StreamEvent{Type: provider.StreamEventText, Text: block.Text})
		case "tool_use":
			events = append(events, provider.StreamEvent{
				Type: provider.StreamEventToolCallDone,
				Tool: provider.ToolCallDelta{
					ID:        block.ToolID,
					Index:     i,
					Name:      block.ToolName,
					Arguments: block.Input,
				},
			})
		}
	}
	events = append(events, provider.StreamEvent{Type: provider.StreamEventDone, Usage: &resp.Usage})
	return events
}

func joinTextEvents(events []provider.StreamEvent) string {
	var sb strings.Builder
	for _, event := range events {
		if event.Type == provider.StreamEventText {
			sb.WriteString(event.Text)
		}
	}
	return sb.String()
}

func TestNewAgent(t *testing.T) {
	mp := &mockProvider{
		chatResp: &provider.ChatResponse{
			Message: provider.Message{
				Role:    "assistant",
				Content: []provider.ContentBlock{{Type: "text", Text: "Hello!"}},
			},
			Usage: provider.TokenUsage{InputTokens: 10, OutputTokens: 5},
		},
	}
	registry := tool.NewRegistry()
	a := NewAgent(mp, registry, "Be helpful", 5)

	if a == nil {
		t.Fatal("NewAgent returned nil")
	}
}

func TestAgent_AddMessage(t *testing.T) {
	mp := &mockProvider{}
	a := NewAgent(mp, tool.NewRegistry(), "", 1)

	a.AddMessage(provider.Message{
		Role:    "user",
		Content: []provider.ContentBlock{{Type: "text", Text: "test"}},
	})

	msgs := a.ContextManager().Messages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].Role != "user" {
		t.Errorf("expected role 'user', got %s", msgs[0].Role)
	}
}

func TestAgent_SystemPrompt(t *testing.T) {
	a := NewAgent(&mockProvider{}, tool.NewRegistry(), "You are a test bot.", 1)
	msgs := a.ContextManager().Messages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 system message, got %d", len(msgs))
	}
	if msgs[0].Role != "system" {
		t.Errorf("expected system role, got %s", msgs[0].Role)
	}
}

func TestAgent_SetProvider(t *testing.T) {
	a := NewAgent(&mockProvider{}, tool.NewRegistry(), "", 1)
	mp2 := &mockProvider{}
	a.SetProvider(mp2)
}

func TestAgent_ProviderAwareTokenCountingIsWired(t *testing.T) {
	a := NewAgent(&mockProvider{tokenCount: 7}, tool.NewRegistry(), "", 1)
	a.AddMessage(provider.Message{
		Role:    "user",
		Content: []provider.ContentBlock{{Type: "text", Text: "test"}},
	})
	if got := a.ContextManager().TokenCount(); got != 7 {
		t.Fatalf("expected token count 7 from provider, got %d", got)
	}
}

func TestAgent_SetProviderUpdatesContextManager(t *testing.T) {
	a := NewAgent(&mockProvider{tokenCount: 2}, tool.NewRegistry(), "", 1)
	a.AddMessage(provider.Message{
		Role:    "user",
		Content: []provider.ContentBlock{{Type: "text", Text: "one"}},
	})
	a.SetProvider(&mockProvider{tokenCount: 9})
	a.AddMessage(provider.Message{
		Role:    "user",
		Content: []provider.ContentBlock{{Type: "text", Text: "two"}},
	})
	if got := a.ContextManager().TokenCount(); got != 11 {
		t.Fatalf("expected mixed token count 11 after provider switch, got %d", got)
	}
}

func TestAgent_ContextManager(t *testing.T) {
	a := NewAgent(&mockProvider{}, tool.NewRegistry(), "", 1)
	cm := a.ContextManager()
	if cm == nil {
		t.Fatal("ContextManager is nil")
	}
	if cm.MaxTokens() != 128000 {
		t.Errorf("expected default MaxTokens 128000, got %d", cm.MaxTokens())
	}
}

func TestRunStreamWithContent_EmitsCompactionProgressMessages(t *testing.T) {
	mp := &mockProvider{
		chatResponses: []*provider.ChatResponse{
			{
				Message: provider.Message{
					Role:    "assistant",
					Content: []provider.ContentBlock{{Type: "text", Text: "Summary text."}},
				},
			},
			{
				Message: provider.Message{
					Role:    "assistant",
					Content: []provider.ContentBlock{{Type: "text", Text: "Summary text again."}},
				},
			},
		},
		streamEvents: [][]provider.StreamEvent{{
			{
				Type: provider.StreamEventText,
				Text: "Final answer.",
			},
			{Type: provider.StreamEventDone},
		}},
	}
	a := NewAgent(mp, tool.NewRegistry(), "System prompt", 1)
	a.ContextManager().SetMaxTokens(80)
	a.AddMessage(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: strings.Repeat("a", 120)}}})
	a.AddMessage(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: strings.Repeat("b", 120)}}})
	a.AddMessage(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: strings.Repeat("c", 120)}}})
	a.AddMessage(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: strings.Repeat("d", 120)}}})

	var texts []string
	err := a.RunStreamWithContent(context.Background(), []provider.ContentBlock{{Type: "text", Text: "new request that should compact"}}, func(event provider.StreamEvent) {
		if event.Type == provider.StreamEventText {
			texts = append(texts, event.Text)
		}
	})
	if err != nil {
		t.Fatalf("RunStreamWithContent() error = %v", err)
	}

	joined := strings.Join(texts, "\n")
	if !strings.Contains(joined, "[compacting conversation to stay within context window]") {
		t.Fatalf("expected compaction progress message, got %q", joined)
	}
	if !strings.Contains(joined, "[conversation compacted]") {
		t.Fatalf("expected compaction completion message, got %q", joined)
	}
	if !strings.Contains(joined, "Final answer.") {
		t.Fatalf("expected assistant response after compaction, got %q", joined)
	}
}

func TestReplaceFirst(t *testing.T) {
	tests := []struct {
		s        string
		old      string
		new      string
		expected string
	}{
		{"hello world", "world", "go", "hello go"},
		{"aaa", "a", "b", "baa"},
		{"hello", "x", "y", "hello"},
		{"", "a", "b", ""},
	}
	for _, tt := range tests {
		got := replaceFirst(tt.s, tt.old, tt.new)
		if got != tt.expected {
			t.Errorf("replaceFirst(%q, %q, %q) = %q, want %q", tt.s, tt.old, tt.new, got, tt.expected)
		}
	}
}

func TestContextManagerTokenEstimation(t *testing.T) {
	cm := ctxpkg.NewManager(1000)
	cm.Add(provider.Message{
		Role:    "user",
		Content: []provider.ContentBlock{{Type: "text", Text: "你好世界hello"}},
	})
	if cm.TokenCount() == 0 {
		t.Error("TokenCount should not be 0 after adding a message")
	}
}

func TestRunStreamUsesStreamingChat(t *testing.T) {
	mp := &mockProvider{
		chatResp: &provider.ChatResponse{
			Message: provider.Message{
				Role: "assistant",
				Content: []provider.ContentBlock{
					{Type: "text", Text: "# Hello\n\nWorld"},
				},
			},
			Usage: provider.TokenUsage{InputTokens: 12, OutputTokens: 7},
		},
	}
	a := NewAgent(mp, tool.NewRegistry(), "", 1)

	var events []provider.StreamEvent
	var gotUsage provider.TokenUsage
	a.SetUsageHandler(func(usage provider.TokenUsage) {
		gotUsage = usage
	})

	err := a.RunStream(context.Background(), "hi", func(event provider.StreamEvent) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatalf("RunStream failed: %v", err)
	}
	if mp.chatCalls != 0 {
		t.Fatalf("expected Chat to be unused, got %d", mp.chatCalls)
	}
	if mp.streamCalls != 1 {
		t.Fatalf("expected ChatStream to be called once, got %d calls", mp.streamCalls)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].Type != provider.StreamEventText || events[0].Text != "# Hello\n\nWorld" {
		t.Fatalf("unexpected text event: %#v", events[0])
	}
	if events[1].Type != provider.StreamEventDone || events[1].Usage == nil {
		t.Fatalf("expected done event with usage, got %#v", events[1])
	}
	if gotUsage != (provider.TokenUsage{InputTokens: 12, OutputTokens: 7}) {
		t.Fatalf("unexpected usage callback: %#v", gotUsage)
	}
}

func TestRunStreamEmitsToolProgressFromChatResponse(t *testing.T) {
	mp := &mockProvider{
		chatResponses: []*provider.ChatResponse{
			{
				Message: provider.Message{
					Role: "assistant",
					Content: []provider.ContentBlock{
						provider.ToolUseBlock("call_1", "echo", []byte(`{"text":"hi"}`)),
					},
				},
				Usage: provider.TokenUsage{InputTokens: 20, OutputTokens: 4},
			},
			{
				Message: provider.Message{
					Role:    "assistant",
					Content: []provider.ContentBlock{provider.TextBlock("done")},
				},
				Usage: provider.TokenUsage{InputTokens: 8, OutputTokens: 2},
			},
		},
	}
	registry := tool.NewRegistry()
	if err := registry.Register(mockTool{name: "echo", result: tool.Result{Content: "ok"}}); err != nil {
		t.Fatalf("register mock tool: %v", err)
	}

	a := NewAgent(mp, registry, "", 2)
	var events []provider.StreamEvent
	err := a.RunStream(context.Background(), "hi", func(event provider.StreamEvent) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatalf("RunStream failed: %v", err)
	}
	if mp.streamCalls != 2 {
		t.Fatalf("expected 2 stream calls, got %d", mp.streamCalls)
	}
	if len(events) != 5 {
		t.Fatalf("expected 5 events, got %d", len(events))
	}
	if events[0].Type != provider.StreamEventToolCallDone || events[0].Tool.Name != "echo" {
		t.Fatalf("unexpected first event: %#v", events[0])
	}
	if events[1].Type != provider.StreamEventDone || events[1].Usage == nil || events[1].Usage.InputTokens != 20 {
		t.Fatalf("unexpected second event: %#v", events[1])
	}
	if events[2].Type != provider.StreamEventToolResult || events[2].Result != "ok" {
		t.Fatalf("unexpected tool result event: %#v", events[2])
	}
	if events[3].Type != provider.StreamEventText || events[3].Text != "done" {
		t.Fatalf("unexpected assistant text event: %#v", events[3])
	}
	if events[4].Type != provider.StreamEventDone || events[4].Usage == nil || events[4].Usage.OutputTokens != 2 {
		t.Fatalf("unexpected final done event: %#v", events[4])
	}
}

func TestRunStreamInterruptReplansBeforeRemainingToolCalls(t *testing.T) {
	mp := &mockProvider{
		chatResponses: []*provider.ChatResponse{
			{
				Message: provider.Message{
					Role: "assistant",
					Content: []provider.ContentBlock{
						provider.ToolUseBlock("call_1", "first", []byte(`{}`)),
						provider.ToolUseBlock("call_2", "second", []byte(`{}`)),
					},
				},
			},
			{
				Message: provider.Message{
					Role:    "assistant",
					Content: []provider.ContentBlock{provider.TextBlock("replanned")},
				},
			},
		},
	}
	registry := tool.NewRegistry()
	var firstCount, secondCount int
	if err := registry.Register(countingTool{name: "first", executed: &firstCount}); err != nil {
		t.Fatalf("register first tool: %v", err)
	}
	if err := registry.Register(countingTool{name: "second", executed: &secondCount}); err != nil {
		t.Fatalf("register second tool: %v", err)
	}

	a := NewAgent(mp, registry, "", 3)
	interruptCalls := 0
	a.SetInterruptionHandler(func() string {
		interruptCalls++
		if interruptCalls == 2 {
			return "skip the second tool and revise"
		}
		return ""
	})

	var events []provider.StreamEvent
	if err := a.RunStream(context.Background(), "start", func(event provider.StreamEvent) {
		events = append(events, event)
	}); err != nil {
		t.Fatalf("RunStream failed: %v", err)
	}

	if firstCount != 1 {
		t.Fatalf("expected first tool to run once, got %d", firstCount)
	}
	if secondCount != 0 {
		t.Fatalf("expected interrupt to skip remaining tool calls, got %d", secondCount)
	}
	if mp.streamCalls != 2 {
		t.Fatalf("expected replanning to trigger a fresh model turn, got %d stream calls", mp.streamCalls)
	}
	if len(events) != 6 {
		t.Fatalf("expected 6 events, got %d", len(events))
	}
	if events[0].Type != provider.StreamEventToolCallDone || events[0].Tool.Name != "first" {
		t.Fatalf("unexpected first event: %#v", events[0])
	}
	if events[1].Type != provider.StreamEventToolCallDone || events[1].Tool.Name != "second" {
		t.Fatalf("expected queued second tool call event, got %#v", events[1])
	}
	if events[2].Type != provider.StreamEventDone {
		t.Fatalf("expected first turn done event, got %#v", events[2])
	}
	if events[3].Type != provider.StreamEventToolResult || events[3].Tool.Name != "first" {
		t.Fatalf("expected first tool result event, got %#v", events[3])
	}
	if events[4].Type != provider.StreamEventText || events[4].Text != "replanned" {
		t.Fatalf("expected replanned response, got %#v", events[4])
	}
	if events[5].Type != provider.StreamEventDone {
		t.Fatalf("expected final done event, got %#v", events[5])
	}
}

func TestRunStreamInjectsPathScopedProjectMemoryBeforeExecutingNestedTool(t *testing.T) {
	tmpDir := t.TempDir()
	repoDir := filepath.Join(tmpDir, "repo")
	nestedDir := filepath.Join(repoDir, "internal", "feature")
	targetFile := filepath.Join(nestedDir, "main.go")
	for _, dir := range []string{repoDir, nestedDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	rootMem := filepath.Join(repoDir, "GGCODE.md")
	nestedMem := filepath.Join(nestedDir, "AGENTS.md")
	if err := os.WriteFile(rootMem, []byte("root guidance"), 0644); err != nil {
		t.Fatalf("write root memory: %v", err)
	}
	if err := os.WriteFile(nestedMem, []byte("nested guidance"), 0644); err != nil {
		t.Fatalf("write nested memory: %v", err)
	}
	if err := os.WriteFile(targetFile, []byte("package main\n"), 0644); err != nil {
		t.Fatalf("write target file: %v", err)
	}

	mp := &mockProvider{
		chatResponses: []*provider.ChatResponse{
			{
				Message: provider.Message{
					Role: "assistant",
					Content: []provider.ContentBlock{
						provider.ToolUseBlock("call_1", "read_file", []byte(`{"path":"internal/feature/main.go"}`)),
					},
				},
			},
			{
				Message: provider.Message{
					Role: "assistant",
					Content: []provider.ContentBlock{
						provider.ToolUseBlock("call_2", "read_file", []byte(`{"path":"internal/feature/main.go"}`)),
					},
				},
			},
			{
				Message: provider.Message{
					Role:    "assistant",
					Content: []provider.ContentBlock{provider.TextBlock("done")},
				},
			},
		},
	}

	registry := tool.NewRegistry()
	var readCount int
	if err := registry.Register(countingTool{name: "read_file", executed: &readCount}); err != nil {
		t.Fatalf("register read_file: %v", err)
	}

	a := NewAgent(mp, registry, "", 4)
	a.SetWorkingDir(repoDir)
	a.SetProjectMemoryFiles([]string{rootMem})

	if err := a.RunStream(context.Background(), "inspect it", func(event provider.StreamEvent) {}); err != nil {
		t.Fatalf("RunStream failed: %v", err)
	}

	if mp.streamCalls != 3 {
		t.Fatalf("expected project memory replan to trigger 3 stream calls, got %d", mp.streamCalls)
	}
	if readCount != 1 {
		t.Fatalf("expected nested tool to execute once after memory injection, got %d", readCount)
	}
	msgs := a.Messages()
	var found bool
	for _, msg := range msgs {
		if msg.Role != "system" || len(msg.Content) == 0 {
			continue
		}
		if strings.Contains(msg.Content[0].Text, "nested guidance") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected injected nested project memory in messages, got %#v", msgs)
	}
}

func TestRunStreamCancellationStopsRemainingToolCalls(t *testing.T) {
	mp := &mockProvider{
		chatResp: &provider.ChatResponse{
			Message: provider.Message{
				Role: "assistant",
				Content: []provider.ContentBlock{
					provider.ToolUseBlock("call_1", "block", []byte(`{}`)),
					provider.ToolUseBlock("call_2", "count", []byte(`{}`)),
				},
			},
			Usage: provider.TokenUsage{InputTokens: 5, OutputTokens: 2},
		},
	}

	registry := tool.NewRegistry()
	var blockCount, countCount int
	started := make(chan struct{})
	if err := registry.Register(blockingTool{name: "block", started: started, executed: &blockCount}); err != nil {
		t.Fatalf("register blocking tool: %v", err)
	}
	if err := registry.Register(countingTool{name: "count", executed: &countCount}); err != nil {
		t.Fatalf("register counting tool: %v", err)
	}

	a := NewAgent(mp, registry, "", 2)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- a.RunStream(ctx, "hi", func(event provider.StreamEvent) {})
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("expected blocking tool to start")
	}
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("expected canceled run to stop promptly")
	}

	if blockCount != 1 {
		t.Fatalf("expected blocking tool to execute once, got %d", blockCount)
	}
	if countCount != 0 {
		t.Fatalf("expected later tool calls to be skipped after cancellation, got %d", countCount)
	}
	if mp.streamCalls != 1 {
		t.Fatalf("expected cancellation to stop before another stream call, got %d", mp.streamCalls)
	}
}

func TestRunStreamAutopilotContinuesClarificationTurn(t *testing.T) {
	mp := &mockProvider{
		chatResponses: []*provider.ChatResponse{
			{
				Message: provider.Message{
					Role:    "assistant",
					Content: []provider.ContentBlock{provider.TextBlock("Should I inspect the tests first or jump straight into the implementation?")},
				},
			},
			{
				Message: provider.Message{
					Role:    "assistant",
					Content: []provider.ContentBlock{provider.TextBlock("I inspected the tests first and found the issue.")},
				},
			},
			{
				Message: provider.Message{
					Role:    "assistant",
					Content: []provider.ContentBlock{provider.TextBlock("Completed the implementation after inspecting the tests first.")},
				},
			},
		},
	}

	a := NewAgent(mp, tool.NewRegistry(), "", 3)
	a.SetPermissionPolicy(permission.NewConfigPolicyWithMode(nil, []string{"."}, permission.AutopilotMode))

	var events []provider.StreamEvent
	if err := a.RunStream(context.Background(), "debug this", func(event provider.StreamEvent) {
		events = append(events, event)
	}); err != nil {
		t.Fatalf("RunStream failed: %v", err)
	}

	if mp.streamCalls != 3 {
		t.Fatalf("expected autopilot to continue until an explicit completion turn, got %d", mp.streamCalls)
	}
	if got := a.Messages(); len(got) < 4 {
		t.Fatalf("expected autopilot to append a synthetic user continuation, got %d messages", len(got))
	}
	lastUser := a.Messages()[2]
	if lastUser.Role != "user" || len(lastUser.Content) == 0 || !strings.Contains(lastUser.Content[0].Text, "Autopilot is enabled") {
		t.Fatalf("expected synthetic autopilot continuation message, got %#v", lastUser)
	}
	if len(events) < 5 || events[len(events)-2].Type != provider.StreamEventText || events[len(events)-2].Text != "Completed the implementation after inspecting the tests first." {
		t.Fatalf("expected explicit completion text after autopilot continuation, got %#v", events)
	}
}

func TestRunStreamInterruptOverridesAutopilotContinuation(t *testing.T) {
	mp := &mockProvider{
		chatResponses: []*provider.ChatResponse{
			{
				Message: provider.Message{
					Role:    "assistant",
					Content: []provider.ContentBlock{provider.TextBlock("Should I keep going with option A or B?")},
				},
			},
			{
				Message: provider.Message{
					Role:    "assistant",
					Content: []provider.ContentBlock{provider.TextBlock("Completed the implementation by switching to option B.")},
				},
			},
		},
	}

	a := NewAgent(mp, tool.NewRegistry(), "", 3)
	a.SetPermissionPolicy(permission.NewConfigPolicyWithMode(nil, []string{"."}, permission.AutopilotMode))
	interruptCalls := 0
	a.SetInterruptionHandler(func() string {
		interruptCalls++
		if interruptCalls == 2 {
			return "Use option B."
		}
		return ""
	})

	if err := a.RunStream(context.Background(), "start", func(event provider.StreamEvent) {}); err != nil {
		t.Fatalf("RunStream failed: %v", err)
	}

	if mp.streamCalls != 2 {
		t.Fatalf("expected interrupt to drive the second turn, got %d stream calls", mp.streamCalls)
	}
	msgs := a.Messages()
	var sawInterrupt, sawAutopilotInstruction bool
	for _, msg := range msgs {
		for _, block := range msg.Content {
			if block.Type != "text" {
				continue
			}
			if strings.Contains(block.Text, "Use option B.") {
				sawInterrupt = true
			}
			if strings.Contains(block.Text, "Autopilot is enabled. Do not wait for user confirmation.") {
				sawAutopilotInstruction = true
			}
		}
	}
	if !sawInterrupt {
		t.Fatal("expected interruption guidance to be recorded in context")
	}
	if sawAutopilotInstruction {
		t.Fatal("expected interruption to take precedence over autopilot continuation prompt")
	}
}

func TestRunStreamInterruptsStreamingTurnForReplan(t *testing.T) {
	mp := &mockProvider{
		streamEvents: [][]provider.StreamEvent{
			{
				{Type: provider.StreamEventText, Text: "Starting long answer..."},
			},
			streamEventsFromResponse(&provider.ChatResponse{
				Message: provider.Message{
					Role:    "assistant",
					Content: []provider.ContentBlock{provider.TextBlock("Switched direction and finished.")},
				},
			}),
		},
	}

	a := NewAgent(mp, tool.NewRegistry(), "", 3)
	interruptCalls := 0
	a.SetInterruptionHandler(func() string {
		interruptCalls++
		if interruptCalls == 2 {
			return "Actually, switch direction now."
		}
		return ""
	})

	var events []provider.StreamEvent
	if err := a.RunStream(context.Background(), "start", func(event provider.StreamEvent) {
		events = append(events, event)
	}); err != nil {
		t.Fatalf("RunStream failed: %v", err)
	}

	if mp.streamCalls != 2 {
		t.Fatalf("expected streaming turn to be interrupted and retried, got %d stream calls", mp.streamCalls)
	}
	joined := joinTextEvents(events)
	if !strings.Contains(joined, "Starting long answer...") {
		t.Fatalf("expected first partial stream text, got %q", joined)
	}
	if !strings.Contains(joined, "Switched direction and finished.") {
		t.Fatalf("expected replanned answer after interrupt, got %q", joined)
	}
	msgs := a.Messages()
	found := false
	for _, msg := range msgs {
		for _, block := range msg.Content {
			if block.Type == "text" && strings.Contains(block.Text, "Actually, switch direction now.") {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("expected streamed interrupt guidance to be recorded in context")
	}
}

func TestRunStreamAutopilotContinuesAfterPartialProgressUpdate(t *testing.T) {
	mp := &mockProvider{
		chatResponses: []*provider.ChatResponse{
			{
				Message: provider.Message{
					Role:    "assistant",
					Content: []provider.ContentBlock{provider.TextBlock("I fixed the obvious lint issue and identified two more hotspots to optimize next.")},
				},
			},
			{
				Message: provider.Message{
					Role:    "assistant",
					Content: []provider.ContentBlock{provider.TextBlock("Completed the optimization pass and updated the related code paths.")},
				},
			},
		},
	}

	a := NewAgent(mp, tool.NewRegistry(), "", 3)
	a.SetPermissionPolicy(permission.NewConfigPolicyWithMode(nil, []string{"."}, permission.AutopilotMode))

	if err := a.RunStream(context.Background(), "optimize the project", func(event provider.StreamEvent) {}); err != nil {
		t.Fatalf("RunStream failed: %v", err)
	}

	if mp.streamCalls != 2 {
		t.Fatalf("expected autopilot to continue after partial progress update, got %d stream calls", mp.streamCalls)
	}
	lastUser := a.Messages()[2]
	if lastUser.Role != "user" || len(lastUser.Content) == 0 || !strings.Contains(lastUser.Content[0].Text, "partial progress") {
		t.Fatalf("expected stronger synthetic continuation message, got %#v", lastUser)
	}
}

func TestRunStreamAutopilotStopsOnExplicitCompletion(t *testing.T) {
	mp := &mockProvider{
		chatResponses: []*provider.ChatResponse{
			{
				Message: provider.Message{
					Role:    "assistant",
					Content: []provider.ContentBlock{provider.TextBlock("Completed the requested optimization pass and updated the relevant files.")},
				},
			},
			{
				Message: provider.Message{
					Role:    "assistant",
					Content: []provider.ContentBlock{provider.TextBlock("unexpected extra turn")},
				},
			},
		},
	}

	a := NewAgent(mp, tool.NewRegistry(), "", 3)
	a.SetPermissionPolicy(permission.NewConfigPolicyWithMode(nil, []string{"."}, permission.AutopilotMode))

	if err := a.RunStream(context.Background(), "optimize the project", func(event provider.StreamEvent) {}); err != nil {
		t.Fatalf("RunStream failed: %v", err)
	}

	if mp.streamCalls != 1 {
		t.Fatalf("expected autopilot to stop on explicit completion, got %d stream calls", mp.streamCalls)
	}
}

func TestRunStreamAutopilotEscalatesExternalBlockerToAskUser(t *testing.T) {
	mp := &mockProvider{
		chatResponses: []*provider.ChatResponse{
			{
				Message: provider.Message{
					Role: "assistant",
					Content: []provider.ContentBlock{provider.TextBlock(
						"All changes are complete. Gateway restart needed to validate the fix. No remaining work. Awaiting gateway restart and test results. Blocked until user restarts the gateway.",
					)},
				},
			},
			{
				Message: provider.Message{
					Role: "assistant",
					Content: []provider.ContentBlock{
						provider.ToolUseBlock("tool-1", "ask_user", json.RawMessage(`{
							"questions":[
								{
									"title":"Restart gateway",
									"prompt":"Please restart the gateway and share the latest diagnostics output.",
									"kind":"text"
								}
							]
						}`)),
					},
				},
			},
			{
				Message: provider.Message{
					Role:    "assistant",
					Content: []provider.ContentBlock{provider.TextBlock("Asked the user to restart the gateway and share the latest diagnostics output.")},
				},
			},
		},
	}

	registry := tool.NewRegistry()
	askCalls := 0
	askTool := tool.NewAskUserTool()
	askTool.SetHandler(func(ctx context.Context, req tool.AskUserRequest) (tool.AskUserResponse, error) {
		askCalls++
		return tool.AskUserResponse{
			Status:        tool.AskUserStatusSubmitted,
			QuestionCount: len(req.Questions),
			AnsweredCount: 0,
		}, nil
	})
	if err := registry.Register(askTool); err != nil {
		t.Fatalf("register ask_user: %v", err)
	}

	a := NewAgent(mp, registry, "", 4)
	a.SetPermissionPolicy(permission.NewConfigPolicyWithMode(nil, []string{"."}, permission.AutopilotMode))

	if err := a.RunStream(context.Background(), "fix the gateway issue", func(event provider.StreamEvent) {}); err != nil {
		t.Fatalf("RunStream failed: %v", err)
	}

	if mp.streamCalls != 3 {
		t.Fatalf("expected autopilot to escalate blocker into ask_user flow, got %d stream calls", mp.streamCalls)
	}
	if askCalls != 1 {
		t.Fatalf("expected ask_user to be invoked once, got %d", askCalls)
	}
}

func TestRunStreamAutopilotStopsOnChineseHandoffClosure(t *testing.T) {
	mp := &mockProvider{
		chatResp: &provider.ChatResponse{
			Message: provider.Message{
				Role:    "assistant",
				Content: []provider.ContentBlock{provider.TextBlock("这是一个 ggcode 项目的开发截图，使用 Warp 终端 + AI 编码助手（GPT-5.4），正在实现图片中的相关功能。如果你有关于这个功能或其他方面的具体任务需要我帮忙，随时告诉我！")},
			},
		},
	}

	a := NewAgent(mp, tool.NewRegistry(), "", 3)
	a.SetPermissionPolicy(permission.NewConfigPolicyWithMode(nil, []string{"."}, permission.AutopilotMode))

	if err := a.RunStream(context.Background(), "看看图里是什么", func(event provider.StreamEvent) {}); err != nil {
		t.Fatalf("RunStream failed: %v", err)
	}

	if mp.streamCalls != 1 {
		t.Fatalf("expected autopilot to stop on Chinese handoff closure, got %d stream calls", mp.streamCalls)
	}
}

func TestRunStreamAutopilotStopsOnChineseCompletionQuestion(t *testing.T) {
	mp := &mockProvider{
		chatResp: &provider.ChatResponse{
			Message: provider.Message{
				Role: "assistant",
				Content: []provider.ContentBlock{provider.TextBlock(
					"所有任务已完成，等待新指令。需要我继续做什么吗？",
				)},
			},
		},
	}

	a := NewAgent(mp, tool.NewRegistry(), "", 3)
	a.SetPermissionPolicy(permission.NewConfigPolicyWithMode(nil, []string{"."}, permission.AutopilotMode))

	if err := a.RunStream(context.Background(), "继续看看", func(event provider.StreamEvent) {}); err != nil {
		t.Fatalf("RunStream failed: %v", err)
	}

	if mp.streamCalls != 1 {
		t.Fatalf("expected autopilot to stop on Chinese completion question, got %d stream calls", mp.streamCalls)
	}
}

func TestRunStreamAutopilotStopsOnEnglishIdleClosure(t *testing.T) {
	mp := &mockProvider{
		chatResp: &provider.ChatResponse{
			Message: provider.Message{
				Role:    "assistant",
				Content: []provider.ContentBlock{provider.TextBlock("All done. Waiting for your next request.")},
			},
		},
	}

	a := NewAgent(mp, tool.NewRegistry(), "", 0)
	a.SetPermissionPolicy(permission.NewConfigPolicyWithMode(nil, []string{"."}, permission.AutopilotMode))

	if err := a.RunStream(context.Background(), "fix the route issue", func(event provider.StreamEvent) {}); err != nil {
		t.Fatalf("RunStream failed: %v", err)
	}

	if mp.streamCalls != 1 {
		t.Fatalf("expected autopilot to stop on english idle closure, got %d stream calls", mp.streamCalls)
	}
}

func TestRunStreamReactiveCompactRetriesPromptTooLong(t *testing.T) {
	mp := &mockProvider{
		chatResp: &provider.ChatResponse{
			Message: provider.Message{
				Role:    "assistant",
				Content: []provider.ContentBlock{provider.TextBlock("compressed summary")},
			},
		},
		streamEvents: [][]provider.StreamEvent{
			{
				{
					Type:  provider.StreamEventError,
					Error: errors.New("prompt too long: model context window exceeded"),
				},
			},
			streamEventsFromResponse(&provider.ChatResponse{
				Message: provider.Message{
					Role:    "assistant",
					Content: []provider.ContentBlock{provider.TextBlock("Recovered after compaction.")},
				},
			}),
		},
	}

	a := NewAgent(mp, tool.NewRegistry(), "", 3)
	a.ContextManager().SetMaxTokens(80)
	for i := 0; i < 6; i++ {
		a.ContextManager().Add(provider.Message{
			Role:    "user",
			Content: []provider.ContentBlock{provider.TextBlock(strings.Repeat("old context ", 8))},
		})
		a.ContextManager().Add(provider.Message{
			Role:    "assistant",
			Content: []provider.ContentBlock{provider.TextBlock(strings.Repeat("assistant reply ", 8))},
		})
	}

	var events []provider.StreamEvent
	if err := a.RunStream(context.Background(), "latest request", func(event provider.StreamEvent) {
		events = append(events, event)
	}); err != nil {
		t.Fatalf("RunStream failed: %v", err)
	}

	if mp.streamCalls != 2 {
		t.Fatalf("expected prompt-too-long recovery to retry once, got %d stream calls", mp.streamCalls)
	}
	joined := joinTextEvents(events)
	if !strings.Contains(joined, "[compacting conversation to stay within context window]") {
		t.Fatalf("expected reactive compaction status, got %q", joined)
	}
	if !strings.Contains(joined, "[conversation compacted]") {
		t.Fatalf("expected reactive compact completion, got %q", joined)
	}
	if strings.Count(joined, "[conversation compacted]") > 2 {
		t.Fatalf("expected at most one preflight compact and one reactive compact completion event, got %q", joined)
	}
	if !strings.Contains(joined, "Recovered after compaction.") {
		t.Fatalf("expected final response after reactive compact retry, got %q", joined)
	}
}

func TestRunStreamIgnoresTransientAutoCompactFailure(t *testing.T) {
	mp := &mockProvider{
		chatErr: errors.New("unexpected EOF"),
		streamEvents: [][]provider.StreamEvent{
			streamEventsFromResponse(&provider.ChatResponse{
				Message: provider.Message{
					Role:    "assistant",
					Content: []provider.ContentBlock{provider.TextBlock("Still answered after compact skip.")},
				},
			}),
		},
	}

	a := NewAgent(mp, tool.NewRegistry(), "", 2)
	a.ContextManager().SetMaxTokens(80)
	for i := 0; i < 6; i++ {
		a.ContextManager().Add(provider.Message{
			Role:    "user",
			Content: []provider.ContentBlock{provider.TextBlock(strings.Repeat("old context ", 8))},
		})
		a.ContextManager().Add(provider.Message{
			Role:    "assistant",
			Content: []provider.ContentBlock{provider.TextBlock(strings.Repeat("assistant reply ", 8))},
		})
	}

	var events []provider.StreamEvent
	if err := a.RunStream(context.Background(), "latest request", func(event provider.StreamEvent) {
		events = append(events, event)
	}); err != nil {
		t.Fatalf("RunStream failed: %v", err)
	}

	joined := joinTextEvents(events)
	if !strings.Contains(joined, "[conversation compaction skipped due to transient provider error: unexpected EOF]") {
		t.Fatalf("expected transient compact skip message, got %q", joined)
	}
	if !strings.Contains(joined, "Still answered after compact skip.") {
		t.Fatalf("expected run to continue after compact skip, got %q", joined)
	}
	for _, event := range events {
		if event.Type == provider.StreamEventError {
			t.Fatalf("expected no terminal error event, got %#v", event.Error)
		}
	}
}

func TestRunStreamAutopilotLoopGuardCompactsAndPauses(t *testing.T) {
	mp := &mockProvider{
		chatResp: &provider.ChatResponse{
			Message: provider.Message{
				Role:    "assistant",
				Content: []provider.ContentBlock{provider.TextBlock("compressed summary")},
			},
		},
		streamEvents: [][]provider.StreamEvent{
			streamEventsFromResponse(&provider.ChatResponse{
				Message: provider.Message{
					Role:    "assistant",
					Content: []provider.ContentBlock{provider.TextBlock("需要我继续做什么吗？")},
				},
			}),
			streamEventsFromResponse(&provider.ChatResponse{
				Message: provider.Message{
					Role:    "assistant",
					Content: []provider.ContentBlock{provider.TextBlock("请告诉我是否继续。")},
				},
			}),
			{
				{Type: provider.StreamEventDone},
			},
		},
	}

	a := NewAgent(mp, tool.NewRegistry(), "", 5)
	a.SetPermissionPolicy(permission.NewConfigPolicyWithMode(nil, []string{"."}, permission.AutopilotMode))
	a.ContextManager().SetMaxTokens(80)
	for i := 0; i < 6; i++ {
		a.ContextManager().Add(provider.Message{
			Role:    "user",
			Content: []provider.ContentBlock{provider.TextBlock(strings.Repeat("old context ", 8))},
		})
		a.ContextManager().Add(provider.Message{
			Role:    "assistant",
			Content: []provider.ContentBlock{provider.TextBlock(strings.Repeat("assistant reply ", 8))},
		})
	}

	var events []provider.StreamEvent
	if err := a.RunStream(context.Background(), "你还好么？", func(event provider.StreamEvent) {
		events = append(events, event)
	}); err != nil {
		t.Fatalf("RunStream failed: %v", err)
	}

	if mp.streamCalls != 2 {
		t.Fatalf("expected loop guard to stop before a third empty turn, got %d stream calls", mp.streamCalls)
	}
	joined := joinTextEvents(events)
	if !strings.Contains(joined, "[autopilot loop guard triggered; compacting and pausing]") {
		t.Fatalf("expected loop guard trigger message, got %q", joined)
	}
	if !strings.Contains(joined, "[conversation compacted]") {
		t.Fatalf("expected forced compaction message, got %q", joined)
	}
	if !strings.Contains(joined, "[autopilot paused to prevent an idle loop]") {
		t.Fatalf("expected idle-loop pause message, got %q", joined)
	}
}

func TestRunStreamAutopilotStopsOnChineseIdleClosure(t *testing.T) {
	mp := &mockProvider{
		chatResp: &provider.ChatResponse{
			Message: provider.Message{
				Role:    "assistant",
				Content: []provider.ContentBlock{provider.TextBlock("没有待处理任务，等待你的下一条指令。")},
			},
		},
	}

	a := NewAgent(mp, tool.NewRegistry(), "", 0)
	a.SetPermissionPolicy(permission.NewConfigPolicyWithMode(nil, []string{"."}, permission.AutopilotMode))

	if err := a.RunStream(context.Background(), "修复这个问题", func(event provider.StreamEvent) {}); err != nil {
		t.Fatalf("RunStream failed: %v", err)
	}

	if mp.streamCalls != 1 {
		t.Fatalf("expected autopilot to stop on chinese idle closure, got %d stream calls", mp.streamCalls)
	}
}

func TestRunStreamAutoModeNeverUsesAutopilotContinuation(t *testing.T) {
	mp := &mockProvider{
		chatResponses: []*provider.ChatResponse{
			{
				Message: provider.Message{
					Role:    "assistant",
					Content: []provider.ContentBlock{provider.TextBlock("What would you like me to do next?")},
				},
			},
			{
				Message: provider.Message{
					Role:    "assistant",
					Content: []provider.ContentBlock{provider.TextBlock("unexpected extra turn")},
				},
			},
		},
	}

	a := NewAgent(mp, tool.NewRegistry(), "", 0)
	a.SetPermissionPolicy(permission.NewConfigPolicyWithMode(nil, []string{"."}, permission.AutoMode))

	if err := a.RunStream(context.Background(), "hello", func(event provider.StreamEvent) {}); err != nil {
		t.Fatalf("RunStream failed: %v", err)
	}

	if mp.streamCalls != 1 {
		t.Fatalf("expected non-autopilot mode to stop after one turn, got %d stream calls", mp.streamCalls)
	}
}

func TestRunStreamWithZeroMaxIterationsDoesNotCapAutopilot(t *testing.T) {
	mp := &mockProvider{
		chatResponses: []*provider.ChatResponse{
			{
				Message: provider.Message{
					Role:    "assistant",
					Content: []provider.ContentBlock{provider.TextBlock("I fixed one part and still need to update the remaining UI pieces.")},
				},
			},
			{
				Message: provider.Message{
					Role:    "assistant",
					Content: []provider.ContentBlock{provider.TextBlock("Completed the requested UI updates.")},
				},
			},
		},
	}

	a := NewAgent(mp, tool.NewRegistry(), "", 0)
	a.SetPermissionPolicy(permission.NewConfigPolicyWithMode(nil, []string{"."}, permission.AutopilotMode))

	if err := a.RunStream(context.Background(), "refactor the UI", func(event provider.StreamEvent) {}); err != nil {
		t.Fatalf("RunStream failed: %v", err)
	}
	if mp.streamCalls != 2 {
		t.Fatalf("expected zero max iterations to allow continued autopilot turns, got %d", mp.streamCalls)
	}
}

func TestRunStreamEmitsErrorWhenMaxIterationsReached(t *testing.T) {
	mp := &mockProvider{
		chatResp: &provider.ChatResponse{
			Message: provider.Message{
				Role: "assistant",
				Content: []provider.ContentBlock{
					provider.ToolUseBlock("tool-1", "mock", json.RawMessage(`{}`)),
				},
			},
		},
	}
	registry := tool.NewRegistry()
	if err := registry.Register(mockTool{name: "mock", result: tool.Result{Content: "ok"}}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	a := NewAgent(mp, registry, "", 1)
	var gotErr error
	err := a.RunStream(context.Background(), "keep going", func(event provider.StreamEvent) {
		if event.Type == provider.StreamEventError {
			gotErr = event.Error
		}
	})
	if err == nil {
		t.Fatal("expected max iterations error")
	}
	if gotErr == nil || !strings.Contains(gotErr.Error(), "max iterations") {
		t.Fatalf("expected stream error event for max iterations, got %v", gotErr)
	}
}
