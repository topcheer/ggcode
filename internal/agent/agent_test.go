package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	ctxpkg "github.com/topcheer/ggcode/internal/context"
	"github.com/topcheer/ggcode/internal/metrics"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

func init() {
	// Zero the precompact start delay so tests don't wait 6 seconds.
	precompactDelay = 0
}

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
	mu            sync.Mutex
	chatResp      *provider.ChatResponse
	chatResponses []*provider.ChatResponse
	chatErr       error
	streamEvents  [][]provider.StreamEvent
	streamErr     error
	tokenCount    int
	chatCalls     int
	streamCalls   int
}

type blockingSummaryProvider struct {
	mu           sync.Mutex
	chatStarted  chan struct{}
	streamEvents [][]provider.StreamEvent
	chatCalls    int
	streamCalls  int
}

func newBlockingSummaryProvider() *blockingSummaryProvider {
	return &blockingSummaryProvider{chatStarted: make(chan struct{})}
}

func (m *blockingSummaryProvider) Name() string { return "blocking-summary" }

func (m *blockingSummaryProvider) Chat(ctx context.Context, messages []provider.Message, tools []provider.ToolDefinition) (*provider.ChatResponse, error) {
	m.mu.Lock()
	m.chatCalls++
	m.mu.Unlock()
	select {
	case <-m.chatStarted:
	default:
		close(m.chatStarted)
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

func (m *blockingSummaryProvider) ChatStream(ctx context.Context, messages []provider.Message, tools []provider.ToolDefinition) (<-chan provider.StreamEvent, error) {
	m.mu.Lock()
	m.streamCalls++
	var events []provider.StreamEvent
	if len(m.streamEvents) > 0 {
		events = m.streamEvents[0]
		m.streamEvents = m.streamEvents[1:]
	} else {
		events = streamEventsFromResponse(&provider.ChatResponse{
			Message: provider.Message{
				Role:    "assistant",
				Content: []provider.ContentBlock{provider.TextBlock("answered without waiting")},
			},
		})
	}
	m.mu.Unlock()

	ch := make(chan provider.StreamEvent, len(events))
	for _, event := range events {
		ch <- event
	}
	close(ch)
	return ch, nil
}

func (m *blockingSummaryProvider) CountTokens(ctx context.Context, messages []provider.Message) (int, error) {
	return 0, errors.New("not implemented")
}

func (m *blockingSummaryProvider) calls() (int, int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.chatCalls, m.streamCalls
}

type delayedSummaryProvider struct {
	mu              sync.Mutex
	releaseSummary  chan struct{}
	summaryReturned chan struct{}
	streamCalls     int
	streamMessages  [][]provider.Message
}

func newDelayedSummaryProvider() *delayedSummaryProvider {
	return &delayedSummaryProvider{
		releaseSummary:  make(chan struct{}),
		summaryReturned: make(chan struct{}),
	}
}

func (m *delayedSummaryProvider) Name() string { return "delayed-summary" }

func (m *delayedSummaryProvider) Chat(ctx context.Context, messages []provider.Message, tools []provider.ToolDefinition) (*provider.ChatResponse, error) {
	select {
	case <-m.releaseSummary:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer close(m.summaryReturned)
	return &provider.ChatResponse{
		Message: provider.Message{
			Role:    "assistant",
			Content: []provider.ContentBlock{provider.TextBlock("compressed summary")},
		},
	}, nil
}

func (m *delayedSummaryProvider) ChatStream(ctx context.Context, messages []provider.Message, tools []provider.ToolDefinition) (<-chan provider.StreamEvent, error) {
	m.mu.Lock()
	m.streamCalls++
	call := m.streamCalls
	snapshot := append([]provider.Message(nil), messages...)
	m.streamMessages = append(m.streamMessages, snapshot)
	m.mu.Unlock()

	var events []provider.StreamEvent
	if call == 1 {
		events = streamEventsFromResponse(&provider.ChatResponse{
			Message: provider.Message{
				Role:    "assistant",
				Content: []provider.ContentBlock{provider.ToolUseBlock("release-compact", "release_compact", []byte(`{}`))},
			},
		})
	} else {
		events = streamEventsFromResponse(&provider.ChatResponse{
			Message: provider.Message{
				Role:    "assistant",
				Content: []provider.ContentBlock{provider.TextBlock("final after loop-boundary compaction")},
			},
		})
	}
	ch := make(chan provider.StreamEvent, len(events))
	for _, event := range events {
		ch <- event
	}
	close(ch)
	return ch, nil
}

func (m *delayedSummaryProvider) CountTokens(ctx context.Context, messages []provider.Message) (int, error) {
	return 0, errors.New("not implemented")
}

func (m *delayedSummaryProvider) messagesForStreamCall(call int) []provider.Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	if call <= 0 || call > len(m.streamMessages) {
		return nil
	}
	return append([]provider.Message(nil), m.streamMessages[call-1]...)
}

type releaseCompactTool struct {
	release chan struct{}
	done    <-chan struct{}
	wait    func()
}

func (t releaseCompactTool) Name() string                { return "release_compact" }
func (t releaseCompactTool) Description() string         { return "release compact" }
func (t releaseCompactTool) Parameters() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (t releaseCompactTool) Execute(ctx context.Context, input json.RawMessage) (tool.Result, error) {
	if t.release != nil {
		select {
		case <-t.release:
		default:
			close(t.release)
		}
	}
	if t.done != nil {
		select {
		case <-t.done:
		case <-ctx.Done():
			return tool.Result{Content: ctx.Err().Error(), IsError: true}, nil
		}
	}
	if t.wait != nil {
		t.wait()
	}
	return tool.Result{Content: "tool output created while async compaction ran"}, nil
}

func (m *mockProvider) Chat(ctx context.Context, messages []provider.Message, tools []provider.ToolDefinition) (*provider.ChatResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.chatCalls++
	if len(m.chatResponses) > 0 {
		resp := m.chatResponses[0]
		m.chatResponses = m.chatResponses[1:]
		return resp, m.chatErr
	}
	return m.chatResp, m.chatErr
}

func (m *mockProvider) ChatStream(ctx context.Context, messages []provider.Message, tools []provider.ToolDefinition) (<-chan provider.StreamEvent, error) {
	m.mu.Lock()
	m.streamCalls++
	if m.streamErr != nil {
		err := m.streamErr
		m.mu.Unlock()
		return nil, err
	}
	var events []provider.StreamEvent
	switch {
	case len(m.streamEvents) > 0:
		events = append([]provider.StreamEvent(nil), m.streamEvents[0]...)
		m.streamEvents = m.streamEvents[1:]
	case len(m.chatResponses) > 0:
		resp := m.chatResponses[0]
		m.chatResponses = m.chatResponses[1:]
		events = streamEventsFromResponse(resp)
	case m.chatResp != nil:
		events = streamEventsFromResponse(m.chatResp)
	}
	m.mu.Unlock()
	ch := make(chan provider.StreamEvent, len(events))
	for _, event := range events {
		ch <- event
	}
	close(ch)
	return ch, nil
}

func (m *mockProvider) CountTokens(ctx context.Context, messages []provider.Message) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
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

func TestAgentRunResultHandler(t *testing.T) {
	mp := &mockProvider{
		chatResp: &provider.ChatResponse{
			Message: provider.Message{
				Role:    "assistant",
				Content: []provider.ContentBlock{{Type: "text", Text: "done"}},
			},
			Usage: provider.TokenUsage{InputTokens: 10, OutputTokens: 2},
		},
	}
	a := NewAgent(mp, tool.NewRegistry(), "", 1)
	called := false
	a.SetRunResultHandler(func(err error) {
		called = true
		if err != nil {
			t.Fatalf("run result error = %v, want nil", err)
		}
	})

	if err := a.RunStream(context.Background(), "hello", func(provider.StreamEvent) {}); err != nil {
		t.Fatalf("RunStream() error = %v", err)
	}
	if !called {
		t.Fatal("expected run result handler to be called")
	}
}

func TestAgentRunResultWithContentHandler(t *testing.T) {
	mp := &mockProvider{
		chatResp: &provider.ChatResponse{
			Message: provider.Message{
				Role:    "assistant",
				Content: []provider.ContentBlock{{Type: "text", Text: "done"}},
			},
			Usage: provider.TokenUsage{InputTokens: 10, OutputTokens: 2},
		},
	}
	a := NewAgent(mp, tool.NewRegistry(), "", 1)
	var got []provider.ContentBlock
	a.SetRunResultWithContentHandler(func(content []provider.ContentBlock, err error) {
		if err != nil {
			t.Fatalf("run result error = %v, want nil", err)
		}
		got = content
	})

	content := []provider.ContentBlock{provider.TextBlock("hello with context"), provider.ImageBlock("image/png", "base64")}
	if err := a.RunStreamWithContent(context.Background(), content, func(provider.StreamEvent) {}); err != nil {
		t.Fatalf("RunStreamWithContent() error = %v", err)
	}
	if len(got) != 2 || got[0].Text != "hello with context" || got[1].ImageMIME != "image/png" {
		t.Fatalf("unexpected handler content: %#v", got)
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
	if cm.ContextWindow() != 128000 {
		t.Errorf("expected default MaxTokens 128000, got %d", cm.ContextWindow())
	}
}

func TestAgent_SetUsageHandlerIncludesSummarizeUsage(t *testing.T) {
	mp := &mockProvider{
		chatResp: &provider.ChatResponse{
			Message: provider.Message{
				Role: "assistant",
				Content: []provider.ContentBlock{
					{Type: "text", Text: "Summary text"},
				},
			},
			Usage: provider.TokenUsage{InputTokens: 42, OutputTokens: 9},
		},
		tokenCount: 10,
	}
	a := NewAgent(mp, tool.NewRegistry(), "system", 1)
	a.AddMessage(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "hello"}}})
	a.AddMessage(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "world"}}})
	a.AddMessage(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "second round"}}})
	a.AddMessage(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "second answer"}}})

	var got provider.TokenUsage
	a.SetUsageHandler(func(usage provider.TokenUsage) {
		got = usage
	})

	if err := a.ContextManager().Summarize(context.Background(), mp); err != nil {
		t.Fatalf("Summarize failed: %v", err)
	}
	if got != (provider.TokenUsage{InputTokens: 42, OutputTokens: 9}) {
		t.Fatalf("expected summarize usage to be reported, got %+v", got)
	}
}

func TestRunStreamWithContent_CompactsSilentlyAndProducesResponse(t *testing.T) {
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
	a.ContextManager().SetContextWindow(80)
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
	// Compaction is now silent — no "[compacting conversation...]" or "[conversation compacted]" text events.
	if strings.Contains(joined, "[compacting conversation to stay within context window]") {
		t.Fatalf("compaction should be silent, but found progress message in %q", joined)
	}
	if strings.Contains(joined, "[conversation compacted]") {
		t.Fatalf("compaction should be silent, but found completion message in %q", joined)
	}
	if !strings.Contains(joined, "Final answer.") {
		t.Fatalf("expected assistant response after silent compaction, got %q", joined)
	}
}

func TestRunStreamDoesNotWaitForInFlightPreCompact(t *testing.T) {
	mp := newBlockingSummaryProvider()
	a := NewAgent(mp, tool.NewRegistry(), "", 1)
	defer a.Close()
	a.ContextManager().SetContextWindow(80)
	for i := 0; i < 6; i++ {
		a.AddMessage(provider.Message{Role: "user", Content: []provider.ContentBlock{provider.TextBlock(strings.Repeat("old context ", 8))}})
		a.AddMessage(provider.Message{Role: "assistant", Content: []provider.ContentBlock{provider.TextBlock(strings.Repeat("assistant reply ", 8))}})
	}

	a.StartPreCompact()
	select {
	case <-mp.chatStarted:
	case <-time.After(time.Second):
		t.Fatal("expected background precompact to start summarization")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	var texts []string
	if err := a.RunStream(ctx, "continue without waiting", func(event provider.StreamEvent) {
		if event.Type == provider.StreamEventText {
			texts = append(texts, event.Text)
		}
	}); err != nil {
		t.Fatalf("RunStream should not wait for background precompact: %v", err)
	}
	if !strings.Contains(strings.Join(texts, ""), "answered without waiting") {
		t.Fatalf("expected normal response while precompact is still running, got %q", strings.Join(texts, ""))
	}
	_, streamCalls := mp.calls()
	if streamCalls != 1 {
		t.Fatalf("expected one LLM stream call, got %d", streamCalls)
	}
}

func TestPreCompactAppliesCompletedSnapshotAtRunBoundary(t *testing.T) {
	mp := &mockProvider{
		chatResp: &provider.ChatResponse{
			Message: provider.Message{
				Role:    "assistant",
				Content: []provider.ContentBlock{provider.TextBlock("compressed summary")},
			},
		},
		streamEvents: [][]provider.StreamEvent{streamEventsFromResponse(&provider.ChatResponse{
			Message: provider.Message{
				Role:    "assistant",
				Content: []provider.ContentBlock{provider.TextBlock("response after applying compacted context")},
			},
		})},
	}
	a := NewAgent(mp, tool.NewRegistry(), "", 1)
	defer a.Close()
	a.ContextManager().SetContextWindow(80)
	for i := 0; i < 6; i++ {
		a.AddMessage(provider.Message{Role: "user", Content: []provider.ContentBlock{provider.TextBlock(strings.Repeat("old context ", 8))}})
		a.AddMessage(provider.Message{Role: "assistant", Content: []provider.ContentBlock{provider.TextBlock(strings.Repeat("assistant reply ", 8))}})
	}
	before := a.ContextManager().TokenCount()

	a.StartPreCompact()
	waitForPrecompactDone(t, a)
	if got := a.ContextManager().TokenCount(); got != before {
		t.Fatalf("background precompact should not mutate live context before a run boundary: before=%d got=%d", before, got)
	}

	if err := a.RunStream(context.Background(), "next request", func(event provider.StreamEvent) {}); err != nil {
		t.Fatalf("RunStream failed: %v", err)
	}
	after := a.ContextManager().TokenCount()
	if after >= before {
		t.Fatalf("expected completed precompact snapshot to reduce live context at run boundary: before=%d after=%d", before, after)
	}
	if !messagesContainText(a.ContextManager().Messages(), "[Previous conversation summary]") {
		t.Fatal("expected compacted summary to be present after run-boundary apply")
	}
}

func TestPreCompactAppliesBetweenLLMTurnsAndPreservesNewDialogue(t *testing.T) {
	mp := newDelayedSummaryProvider()
	reg := tool.NewRegistry()
	var a *Agent
	reg.Register(releaseCompactTool{release: mp.releaseSummary, done: mp.summaryReturned, wait: func() {
		waitForPrecompactDone(t, a)
	}})
	a = NewAgent(mp, reg, "", 2)
	defer a.Close()
	a.ContextManager().SetContextWindow(50000)
	a.ContextManager().SetOutputReserve(100)
	for i := 0; i < 200; i++ {
		a.AddMessage(provider.Message{Role: "user", Content: []provider.ContentBlock{provider.TextBlock(strings.Repeat("old context ", 50))}})
		a.AddMessage(provider.Message{Role: "assistant", Content: []provider.ContentBlock{provider.TextBlock(strings.Repeat("assistant reply ", 50))}})
	}

	// Pre-release the summary so the background precompact's Chat() returns immediately.
	close(mp.releaseSummary)

	a.StartPreCompact()
	// Wait for the background precompact to complete so consumeReadyPreCompact
	// can apply it at the top of the first loop iteration (before any hard guard fires).
	a.mu.RLock()
	pc := a.precompact
	a.mu.RUnlock()
	if pc != nil {
		select {
		case <-pc.done:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for precompact")
		}
	}
	if err := a.RunStream(context.Background(), "first user message while compacting", func(event provider.StreamEvent) {}); err != nil {
		t.Fatalf("RunStream failed: %v", err)
	}

	secondTurnMessages := mp.messagesForStreamCall(2)
	if len(secondTurnMessages) == 0 {
		t.Fatal("expected second LLM turn")
	}
	if !messagesContainText(secondTurnMessages, "[Previous conversation summary]") {
		t.Fatal("expected completed compacted context to be applied before second LLM turn")
	}
	if !messagesContainText(secondTurnMessages, "first user message while compacting") {
		t.Fatal("expected user message appended after snapshot to be preserved")
	}
	if !messagesContainText(secondTurnMessages, "tool output created while async compaction ran") {
		t.Fatal("expected tool result from dialogue created while compacting to be preserved")
	}
}

func waitForPrecompactDone(t *testing.T, a *Agent) {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		a.mu.RLock()
		pc := a.precompact
		a.mu.RUnlock()
		if pc == nil {
			// Precompact was already consumed (completed and applied).
			return
		}
		select {
		case <-pc.done:
			return
		case <-deadline:
			t.Fatal("timed out waiting for precompact")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func messagesContainText(messages []provider.Message, want string) bool {
	for _, msg := range messages {
		for _, block := range msg.Content {
			if strings.Contains(block.Text, want) || strings.Contains(block.Output, want) {
				return true
			}
		}
	}
	return false
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

	// New behavior: ALL tool calls execute before user interruption is injected.
	// This ensures every tool_call has a matching tool_result (required by APIs).
	if firstCount != 1 {
		t.Fatalf("expected first tool to run once, got %d", firstCount)
	}
	if secondCount != 1 {
		t.Fatalf("expected second tool to run once (all tools execute before interrupt), got %d", secondCount)
	}
	if mp.streamCalls != 2 {
		t.Fatalf("expected replanning to trigger a fresh model turn, got %d stream calls", mp.streamCalls)
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
	// New behavior: all tool calls execute before memory injection.
	// read_file is called in iteration 1 + iteration 2 (after memory injection) = 2 times.
	if readCount != 2 {
		t.Fatalf("expected read_file to execute twice (before and after memory injection), got %d", readCount)
	}
	msgs := a.Messages()
	var found bool
	for _, msg := range msgs {
		for _, block := range msg.Content {
			if block.Type == "text" && strings.Contains(block.Text, "nested guidance") {
				found = true
				break
			}
		}
		if found {
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
	// With the strategist-based autopilot, agent needs a confirmed goal.
	// Flow: stream→question | Chat strategist→guidance | stream→fixed | Chat strategist→GOAL_ACHIEVED
	mp := &mockProvider{
		chatResponses: []*provider.ChatResponse{
			// [0] ChatStream call 1: agent asks a question (no tool calls)
			{Message: provider.Message{Role: "assistant", Content: []provider.ContentBlock{provider.TextBlock("Should I inspect the tests first or jump straight into the implementation?")}}},
			// [1] Chat call 1 (strategist): tells agent to inspect tests
			{Message: provider.Message{Role: "assistant", Content: []provider.ContentBlock{provider.TextBlock("Start by inspecting the tests, then fix the root cause.")}}},
			// [2] ChatStream call 2: agent reports completion
			{Message: provider.Message{Role: "assistant", Content: []provider.ContentBlock{provider.TextBlock("I inspected the tests first and fixed the root cause.")}}},
			// [3] Chat call 2 (strategist): goal achieved
			{Message: provider.Message{Role: "assistant", Content: []provider.ContentBlock{provider.TextBlock("GOAL_ACHIEVED\nThe agent fixed the root cause.")}}},
		},
	}

	a := NewAgent(mp, tool.NewRegistry(), "", 3)
	a.SetPermissionPolicy(permission.NewConfigPolicyWithMode(nil, []string{"."}, permission.AutopilotMode))
	a.SetAutopilotGoal("debug this issue")

	var events []provider.StreamEvent
	if err := a.RunStream(context.Background(), "debug this", func(event provider.StreamEvent) {
		events = append(events, event)
	}); err != nil {
		t.Fatalf("RunStream failed: %v", err)
	}

	if mp.streamCalls != 2 {
		t.Fatalf("expected 2 stream calls (question + completion), got %d", mp.streamCalls)
	}
	if mp.chatCalls != 2 {
		t.Fatalf("expected 2 strategist Chat calls, got %d", mp.chatCalls)
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
			if strings.Contains(block.Text, "Autopilot: continue working") {
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
	// Strategist-based autopilot: agent needs goal, strategist drives continuation.
	mp := &mockProvider{
		chatResponses: []*provider.ChatResponse{
			// [0] stream 1: partial progress
			{Message: provider.Message{Role: "assistant", Content: []provider.ContentBlock{provider.TextBlock("I fixed the obvious lint issue and identified two more hotspots to optimize next.")}}},
			// [1] Chat strategist: continue
			{Message: provider.Message{Role: "assistant", Content: []provider.ContentBlock{provider.TextBlock("Continue optimizing the remaining hotspots.")}}},
			// [2] stream 2: completion
			{Message: provider.Message{Role: "assistant", Content: []provider.ContentBlock{provider.TextBlock("Completed the optimization pass and updated the related code paths.")}}},
			// [3] Chat strategist: goal achieved
			{Message: provider.Message{Role: "assistant", Content: []provider.ContentBlock{provider.TextBlock("GOAL_ACHIEVED\nOptimization complete.")}}},
		},
	}

	a := NewAgent(mp, tool.NewRegistry(), "", 3)
	a.SetPermissionPolicy(permission.NewConfigPolicyWithMode(nil, []string{"."}, permission.AutopilotMode))
	a.SetAutopilotGoal("optimize the project")

	if err := a.RunStream(context.Background(), "optimize the project", func(event provider.StreamEvent) {}); err != nil {
		t.Fatalf("RunStream failed: %v", err)
	}

	if mp.streamCalls != 2 {
		t.Fatalf("expected 2 stream calls, got %d", mp.streamCalls)
	}
	if mp.chatCalls != 2 {
		t.Fatalf("expected 2 strategist Chat calls, got %d", mp.chatCalls)
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
	// The deterministic blocker escalation is replaced by the strategist.
	// Skip: strategist-based escalation depends on LLM reasoning, not deterministic patterns.
	t.Skip("blocker escalation now handled by strategist LLM, not deterministic text matching")
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
	a.ContextManager().SetContextWindow(80)
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
	// Compaction is now silent — no visible compression messages emitted.
	if strings.Contains(joined, "[compacting conversation to stay within context window]") {
		t.Fatalf("compaction should be silent, but found progress message in %q", joined)
	}
	if strings.Contains(joined, "[conversation compacted]") {
		t.Fatalf("compaction should be silent, but found completion message in %q", joined)
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
	a.ContextManager().SetContextWindow(80)
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
	// Transient compact errors are now logged silently via debug.Log instead of emitted as text events.
	if strings.Contains(joined, "[conversation compaction skipped due to transient provider error:") {
		t.Fatalf("transient compact skip should be silent, but found message in %q", joined)
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
	// The loop guard mechanism is removed — replaced by the strategist.
	t.Skip("loop guard removed, strategist replaces deterministic autopilot loop detection")
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
	a.ContextManager().SetContextWindow(80)
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
	// Compression is now silent — no visible compact/loop-guard messages.
	if strings.Contains(joined, "[autopilot loop guard triggered") {
		t.Fatalf("loop guard should be silent, but found message in %q", joined)
	}
	if strings.Contains(joined, "[conversation compacted]") {
		t.Fatalf("compaction should be silent, but found message in %q", joined)
	}
	if strings.Contains(joined, "[autopilot paused") {
		t.Fatalf("autopilot pause should be silent, but found message in %q", joined)
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
	// With strategist-based autopilot, zero max iterations still allows continuation.
	mp := &mockProvider{
		chatResponses: []*provider.ChatResponse{
			// [0] stream 1
			{Message: provider.Message{Role: "assistant", Content: []provider.ContentBlock{provider.TextBlock("I fixed one part and still need to update the remaining UI pieces.")}}},
			// [1] Chat strategist: continue
			{Message: provider.Message{Role: "assistant", Content: []provider.ContentBlock{provider.TextBlock("Continue updating the remaining UI pieces.")}}},
			// [2] stream 2
			{Message: provider.Message{Role: "assistant", Content: []provider.ContentBlock{provider.TextBlock("Completed the requested UI updates.")}}},
			// [3] Chat strategist: goal achieved
			{Message: provider.Message{Role: "assistant", Content: []provider.ContentBlock{provider.TextBlock("GOAL_ACHIEVED\nUI updates complete.")}}},
		},
	}

	a := NewAgent(mp, tool.NewRegistry(), "", 0)
	a.SetPermissionPolicy(permission.NewConfigPolicyWithMode(nil, []string{"."}, permission.AutopilotMode))
	a.SetAutopilotGoal("refactor the UI")

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

func TestRunStreamEmitsLLMMetric(t *testing.T) {
	mp := &mockProvider{
		streamEvents: [][]provider.StreamEvent{{
			{Type: provider.StreamEventText, Text: "Hello"},
			{Type: provider.StreamEventDone, Usage: &provider.TokenUsage{InputTokens: 10, OutputTokens: 5}},
		}},
	}
	a := NewAgent(mp, tool.NewRegistry(), "", 5)

	var collectedMetrics []metrics.MetricEvent
	a.SetMetricHandler(func(m metrics.MetricEvent) {
		collectedMetrics = append(collectedMetrics, m)
	})

	err := a.RunStream(context.Background(), "hi", func(event provider.StreamEvent) {})
	if err != nil {
		t.Fatalf("RunStream() error = %v", err)
	}

	if len(collectedMetrics) == 0 {
		t.Fatal("expected at least 1 metric event")
	}

	// Find the LLM metric
	var llmMetric *metrics.MetricEvent
	for i := range collectedMetrics {
		if collectedMetrics[i].Type == "llm" {
			llmMetric = &collectedMetrics[i]
			break
		}
	}
	if llmMetric == nil {
		t.Fatal("expected an LLM metric event")
	}
	if llmMetric.TTFT <= 0 {
		t.Errorf("expected positive TTFT, got %v", llmMetric.TTFT)
	}
	if llmMetric.Duration <= 0 {
		t.Errorf("expected positive Duration, got %v", llmMetric.Duration)
	}
	if llmMetric.Duration < llmMetric.TTFT {
		t.Errorf("Duration %v should be >= TTFT %v", llmMetric.Duration, llmMetric.TTFT)
	}
}

func TestRunStreamEmitsToolMetric(t *testing.T) {
	mp := &mockProvider{
		streamEvents: [][]provider.StreamEvent{
			{
				{Type: provider.StreamEventToolCallDone, Tool: provider.ToolCallDelta{ID: "tc1", Name: "mock", Arguments: json.RawMessage(`{}`)}},
				{Type: provider.StreamEventDone, Usage: &provider.TokenUsage{InputTokens: 10, OutputTokens: 5}},
			},
			{
				{Type: provider.StreamEventText, Text: "done"},
				{Type: provider.StreamEventDone, Usage: &provider.TokenUsage{InputTokens: 20, OutputTokens: 10}},
			},
		},
	}
	registry := tool.NewRegistry()
	if err := registry.Register(mockTool{name: "mock", result: tool.Result{Content: "ok"}}); err != nil {
		t.Fatal(err)
	}

	a := NewAgent(mp, registry, "", 5)

	var collectedMetrics []metrics.MetricEvent
	a.SetMetricHandler(func(m metrics.MetricEvent) {
		collectedMetrics = append(collectedMetrics, m)
	})

	err := a.RunStream(context.Background(), "use tool", func(event provider.StreamEvent) {})
	if err != nil {
		t.Fatalf("RunStream() error = %v", err)
	}

	// Find tool metric
	var toolMetric *metrics.MetricEvent
	for i := range collectedMetrics {
		if collectedMetrics[i].Type == "tool" {
			toolMetric = &collectedMetrics[i]
			break
		}
	}
	if toolMetric == nil {
		t.Fatal("expected a tool metric event")
	}
	if toolMetric.ToolName != "mock" {
		t.Errorf("tool name: got %q, want mock", toolMetric.ToolName)
	}
	if !toolMetric.ToolSuccess {
		t.Error("expected tool success = true")
	}
	if toolMetric.ToolDuration <= 0 {
		t.Errorf("expected positive tool duration, got %v", toolMetric.ToolDuration)
	}
}

func TestRunStreamEmitsToolMetricOnFailure(t *testing.T) {
	errTool := mockTool{
		name:   "fail-tool",
		result: tool.Result{Content: "something broke", IsError: true},
	}
	mp := &mockProvider{
		streamEvents: [][]provider.StreamEvent{
			{
				{Type: provider.StreamEventToolCallDone, Tool: provider.ToolCallDelta{ID: "tc1", Name: "fail-tool", Arguments: json.RawMessage(`{}`)}},
				{Type: provider.StreamEventDone, Usage: &provider.TokenUsage{InputTokens: 10, OutputTokens: 5}},
			},
			{
				{Type: provider.StreamEventText, Text: "recovered"},
				{Type: provider.StreamEventDone, Usage: &provider.TokenUsage{InputTokens: 20, OutputTokens: 10}},
			},
		},
	}
	registry := tool.NewRegistry()
	if err := registry.Register(errTool); err != nil {
		t.Fatal(err)
	}

	a := NewAgent(mp, registry, "", 5)

	var collectedMetrics []metrics.MetricEvent
	a.SetMetricHandler(func(m metrics.MetricEvent) {
		collectedMetrics = append(collectedMetrics, m)
	})

	err := a.RunStream(context.Background(), "use failing tool", func(event provider.StreamEvent) {})
	if err != nil {
		t.Fatalf("RunStream() error = %v", err)
	}

	var toolMetric *metrics.MetricEvent
	for i := range collectedMetrics {
		if collectedMetrics[i].Type == "tool" {
			toolMetric = &collectedMetrics[i]
			break
		}
	}
	if toolMetric == nil {
		t.Fatal("expected a tool metric event")
	}
	if toolMetric.ToolSuccess {
		t.Error("expected tool success = false for error tool")
	}
	if toolMetric.ToolError == "" {
		t.Error("expected non-empty tool error")
	}
}

func TestRunStreamEmitsReasoningMetric(t *testing.T) {
	mp := &mockProvider{
		streamEvents: [][]provider.StreamEvent{
			{
				{Type: provider.StreamEventReasoning, Text: "thinking..."},
				{Type: provider.StreamEventReasoning, Text: " more thinking"},
				{Type: provider.StreamEventText, Text: "answer"},
				{Type: provider.StreamEventDone, Usage: &provider.TokenUsage{InputTokens: 10, OutputTokens: 5}},
			},
		},
	}
	a := NewAgent(mp, tool.NewRegistry(), "", 5)

	var collectedMetrics []metrics.MetricEvent
	a.SetMetricHandler(func(m metrics.MetricEvent) {
		collectedMetrics = append(collectedMetrics, m)
	})

	err := a.RunStream(context.Background(), "think about it", func(event provider.StreamEvent) {})
	if err != nil {
		t.Fatalf("RunStream() error = %v", err)
	}

	var llmMetric *metrics.MetricEvent
	for i := range collectedMetrics {
		if collectedMetrics[i].Type == "llm" {
			llmMetric = &collectedMetrics[i]
			break
		}
	}
	if llmMetric == nil {
		t.Fatal("expected an LLM metric event")
	}
	if llmMetric.ThinkTime <= 0 {
		t.Errorf("expected positive think time, got %v", llmMetric.ThinkTime)
	}
}

func TestAutopilotGoalLifecycle(t *testing.T) {
	// Test the full lifecycle: goal not set → set → check → complete → cleared.
	a := NewAgent(&mockProvider{}, tool.NewRegistry(), "", 1)
	a.SetPermissionPolicy(permission.NewConfigPolicyWithMode(nil, []string{"."}, permission.AutopilotMode))

	// Initially no goal
	if a.hasAutopilotGoal() {
		t.Fatal("expected no goal initially")
	}
	if a.isAutopilotGoalComplete("anything") {
		t.Fatal("isAutopilotGoalComplete should be false when no goal")
	}

	// Set goal
	a.SetAutopilotGoal("Fix all failing tests in the auth module")
	if !a.hasAutopilotGoal() {
		t.Fatal("expected goal to be set")
	}
	if a.getAutopilotGoal() != "Fix all failing tests in the auth module" {
		t.Fatalf("unexpected goal: %s", a.getAutopilotGoal())
	}

	// GOAL_COMPLETE detection
	if !a.isAutopilotGoalComplete("All tests pass.\nGOAL_COMPLETE") {
		t.Fatal("expected GOAL_COMPLETE to be detected")
	}
	if a.isAutopilotGoalComplete("Still working on it") {
		t.Fatal("expected no false positive on GOAL_COMPLETE")
	}

	// Clear goal
	a.clearAutopilotGoal()
	if a.hasAutopilotGoal() {
		t.Fatal("expected goal to be cleared")
	}
}

func TestAutopilotGoalClearedOnModeSwitch(t *testing.T) {
	a := NewAgent(&mockProvider{}, tool.NewRegistry(), "", 1)

	// Enter autopilot and set a goal
	a.SetPermissionPolicy(permission.NewConfigPolicyWithMode(nil, []string{"."}, permission.AutopilotMode))
	a.SetAutopilotGoal("Do something autonomous")
	if !a.hasAutopilotGoal() {
		t.Fatal("expected goal to be set")
	}

	// Switch to auto mode — goal should be cleared
	a.SetPermissionPolicy(permission.NewConfigPolicyWithMode(nil, []string{"."}, permission.AutoMode))
	if a.hasAutopilotGoal() {
		t.Fatal("expected goal to be cleared after leaving autopilot")
	}

	// Switch back to autopilot — goal should still be empty
	a.SetPermissionPolicy(permission.NewConfigPolicyWithMode(nil, []string{"."}, permission.AutopilotMode))
	if a.hasAutopilotGoal() {
		t.Fatal("expected no goal after re-entering autopilot")
	}
}
