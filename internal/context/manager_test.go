package context

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
	toolpkg "github.com/topcheer/ggcode/internal/tool"
)

type mockProvider struct {
	chatCalls int
}

func (m *mockProvider) Name() string { return "mock" }
func (m *mockProvider) Chat(ctx context.Context, msgs []provider.Message, tools []provider.ToolDefinition) (*provider.ChatResponse, error) {
	m.chatCalls++
	return &provider.ChatResponse{
		Message: provider.Message{
			Role: "assistant",
			Content: []provider.ContentBlock{
				{Type: "text", Text: "Summary: User asked about testing. Assistant responded with helpful information."},
			},
		},
		Usage: provider.TokenUsage{InputTokens: 100, OutputTokens: 50},
	}, nil
}
func (m *mockProvider) ChatStream(ctx context.Context, msgs []provider.Message, tools []provider.ToolDefinition) (<-chan provider.StreamEvent, error) {
	ch := make(chan provider.StreamEvent, 1)
	close(ch)
	return ch, nil
}
func (m *mockProvider) CountTokens(ctx context.Context, msgs []provider.Message) (int, error) {
	return 200, nil
}

type blockingCountProvider struct{}

func (b *blockingCountProvider) Name() string { return "blocking" }
func (b *blockingCountProvider) Chat(ctx context.Context, msgs []provider.Message, tools []provider.ToolDefinition) (*provider.ChatResponse, error) {
	return nil, errors.New("not implemented")
}
func (b *blockingCountProvider) ChatStream(ctx context.Context, msgs []provider.Message, tools []provider.ToolDefinition) (<-chan provider.StreamEvent, error) {
	ch := make(chan provider.StreamEvent)
	close(ch)
	return ch, nil
}
func (b *blockingCountProvider) CountTokens(ctx context.Context, msgs []provider.Message) (int, error) {
	<-ctx.Done()
	return 0, ctx.Err()
}

type promptTooLongOnceProvider struct {
	chatCalls int
}

func (p *promptTooLongOnceProvider) Name() string { return "ptl-once" }
func (p *promptTooLongOnceProvider) Chat(ctx context.Context, msgs []provider.Message, tools []provider.ToolDefinition) (*provider.ChatResponse, error) {
	p.chatCalls++
	if p.chatCalls == 1 {
		return nil, errors.New("prompt too long: exceeds the model's context window")
	}
	return &provider.ChatResponse{
		Message: provider.Message{
			Role:    "assistant",
			Content: []provider.ContentBlock{{Type: "text", Text: "Recovered summary after retry."}},
		},
	}, nil
}
func (p *promptTooLongOnceProvider) ChatStream(ctx context.Context, msgs []provider.Message, tools []provider.ToolDefinition) (<-chan provider.StreamEvent, error) {
	ch := make(chan provider.StreamEvent)
	close(ch)
	return ch, nil
}
func (p *promptTooLongOnceProvider) CountTokens(ctx context.Context, msgs []provider.Message) (int, error) {
	return 0, errors.New("not implemented")
}

func TestContextManager_Basic(t *testing.T) {
	cm := NewManager(1000)

	cm.Add(provider.Message{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "You are helpful."}}})
	if cm.TokenCount() == 0 {
		t.Error("TokenCount should be > 0 after adding message")
	}

	msgs := cm.Messages()
	if len(msgs) != 1 {
		t.Errorf("Expected 1 message, got %d", len(msgs))
	}

	if msgs[0].Role != "system" {
		t.Error("First message should be system")
	}
}

func TestContextManager_Clear(t *testing.T) {
	cm := NewManager(1000)
	cm.Add(provider.Message{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "System."}}})
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "Hello"}}})

	cm.Clear()

	msgs := cm.Messages()
	if len(msgs) != 1 {
		t.Errorf("Expected 1 message after clear (system kept), got %d", len(msgs))
	}
	if msgs[0].Role != "system" {
		t.Error("System message should be preserved")
	}
}

func TestContextManager_UsageRatio(t *testing.T) {
	cm := NewManager(100)

	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "Test message"}}})
	ratio := cm.UsageRatio()
	if ratio <= 0 || ratio > 1 {
		t.Errorf("UsageRatio should be 0..1, got %f", ratio)
	}
}

func TestContextManager_Summarize(t *testing.T) {
	cm := NewManager(10000)
	ctx := context.Background()
	prov := &mockProvider{}

	// Add enough messages to trigger summarization (need > 6 + system = 7)
	cm.Add(provider.Message{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "System prompt."}}})
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "First message."}}})
	cm.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "First response."}}})
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "Second message."}}})
	cm.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "Second response."}}})
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "Third message."}}})
	cm.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "Third response."}}})
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "Fourth message."}}})
	cm.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "Fourth response."}}})

	err := cm.Summarize(ctx, prov)
	if err != nil {
		t.Fatalf("Summarize failed: %v", err)
	}

	// Verify summary exists in system messages
	hasSummary := false
	hasOldMessage := false
	for _, m := range cm.Messages() {
		for _, b := range m.Content {
			if b.Type == "text" && strings.Contains(b.Text, "[Previous conversation summary]") {
				hasSummary = true
			}
			if b.Type == "text" && strings.Contains(b.Text, "First message.") {
				hasOldMessage = true
			}
		}
	}
	if !hasSummary {
		t.Error("Summary block not found in messages after summarization")
	}
	if hasOldMessage {
		t.Error("Expected old message content to be summarized away")
	}
}

func TestContextManager_Summarize_TooFewMessages(t *testing.T) {
	cm := NewManager(10000)
	ctx := context.Background()
	prov := &mockProvider{}

	cm.Add(provider.Message{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "System."}}})
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "Hi"}}})

	// Should not summarize with too few messages
	err := cm.Summarize(ctx, prov)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Message count should remain the same
	if len(cm.Messages()) != 2 {
		t.Errorf("Expected 2 messages (no summarization), got %d", len(cm.Messages()))
	}
}

func TestContextManager_Summarize_AdaptiveRetentionByTokenBudget(t *testing.T) {
	ctx := context.Background()
	prov := &mockProvider{}

	small := NewManager(1000)
	large := NewManager(1000)

	small.Add(provider.Message{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "System prompt."}}})
	large.Add(provider.Message{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "System prompt."}}})

	for i := 0; i < 10; i++ {
		small.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: fmt.Sprintf("small-user-%d %s", i, strings.Repeat("x", 40))}}})
		small.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: fmt.Sprintf("small-assistant-%d %s", i, strings.Repeat("y", 40))}}})
		large.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: fmt.Sprintf("large-user-%d %s", i, strings.Repeat("x", 280))}}})
		large.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: fmt.Sprintf("large-assistant-%d %s", i, strings.Repeat("y", 280))}}})
	}

	if err := small.Summarize(ctx, prov); err != nil {
		t.Fatalf("small summarize failed: %v", err)
	}
	if err := large.Summarize(ctx, prov); err != nil {
		t.Fatalf("large summarize failed: %v", err)
	}

	smallRetained := retainedConversationMessages(small.Messages())
	largeRetained := retainedConversationMessages(large.Messages())
	if smallRetained <= largeRetained {
		t.Fatalf("expected smaller messages to retain more recent history: small=%d large=%d", smallRetained, largeRetained)
	}
}

func TestContextManager_Summarize_BringsUsageBelowThreshold(t *testing.T) {
	cm := NewManager(1000)
	ctx := context.Background()
	prov := &mockProvider{}

	cm.Add(provider.Message{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "System prompt."}}})
	for i := 0; i < 12; i++ {
		cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: fmt.Sprintf("message-%d %s", i, strings.Repeat("z", 320))}}})
	}

	if cm.UsageRatio() < summarizeThreshold {
		t.Fatalf("expected setup to exceed summarize threshold, got %.2f", cm.UsageRatio())
	}

	if err := cm.Summarize(ctx, prov); err != nil {
		t.Fatalf("Summarize failed: %v", err)
	}

	if cm.UsageRatio() >= summarizeThreshold {
		t.Fatalf("expected summarized context to be below threshold, got %.2f", cm.UsageRatio())
	}
}

func TestContextManager_Microcompact_ReducesOldToolResults(t *testing.T) {
	cm := NewManager(300)

	cm.Add(provider.Message{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "System prompt."}}})
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "Need command output"}}})
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "tool_result", ToolID: "tool-1", Output: strings.Repeat("A", 1400)}}})
	cm.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "I inspected the output."}}})
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "Recent question"}}})
	cm.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "Recent answer"}}})

	before := cm.TokenCount()
	if !cm.Microcompact() {
		t.Fatal("expected microcompact to change old tool results")
	}
	if cm.TokenCount() >= before {
		t.Fatalf("expected token count to decrease after microcompact: before=%d after=%d", before, cm.TokenCount())
	}

	msgs := cm.Messages()
	foundPlaceholder := false
	for _, block := range msgs[2].Content {
		if block.Type == "tool_result" && strings.Contains(block.Output, "[tool result compacted:") {
			foundPlaceholder = true
		}
	}
	if !foundPlaceholder {
		t.Fatal("expected old tool result to be compacted into placeholder text")
	}
}

func TestContextManager_CheckAndSummarize_UsesMicrocompactBeforeSummary(t *testing.T) {
	cm := NewManager(300)
	ctx := context.Background()
	prov := &mockProvider{}

	cm.Add(provider.Message{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "System prompt."}}})
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "Show me the tool output"}}})
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "tool_result", ToolID: "tool-1", Output: strings.Repeat("B", 1600)}}})
	cm.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "Processed."}}})
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "Recent question"}}})
	cm.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "Recent answer"}}})

	summarized, err := cm.CheckAndSummarize(ctx, prov)
	if err != nil {
		t.Fatalf("CheckAndSummarize failed: %v", err)
	}
	if !summarized {
		t.Fatal("expected CheckAndSummarize to compact context")
	}
	if prov.chatCalls != 0 {
		t.Fatalf("expected microcompact-only path without summary call, got %d Chat calls", prov.chatCalls)
	}
	if cm.UsageRatio() >= summarizeThreshold {
		t.Fatalf("expected microcompact to bring usage below threshold, got %.2f", cm.UsageRatio())
	}
}

func TestContextManager_Microcompact_PreservesMostRecentGroup(t *testing.T) {
	cm := NewManager(500)

	cm.Add(provider.Message{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "System prompt."}}})
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "old question"}}})
	cm.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{provider.ToolUseBlock("call-old", "read_file", []byte(`{}`))}})
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "tool_result", ToolID: "call-old", Output: strings.Repeat("O", 1200)}}})
	cm.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "old answer"}}})
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "recent question"}}})
	cm.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{provider.ToolUseBlock("call-recent", "read_file", []byte(`{}`))}})
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "tool_result", ToolID: "call-recent", Output: strings.Repeat("R", 1200)}}})
	cm.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "recent answer"}}})

	if !cm.Microcompact() {
		t.Fatal("expected microcompact to change old group")
	}

	msgs := cm.Messages()
	oldCompacted := false
	recentStillRaw := false
	for _, block := range msgs[3].Content {
		if block.Type == "tool_result" && strings.Contains(block.Output, "[tool result compacted:") {
			oldCompacted = true
		}
	}
	for _, block := range msgs[7].Content {
		if block.Type == "tool_result" && strings.Contains(block.Output, strings.Repeat("R", 200)) {
			recentStillRaw = true
		}
	}
	if !oldCompacted {
		t.Fatal("expected old group tool result to be compacted")
	}
	if !recentStillRaw {
		t.Fatal("expected most recent group tool result to remain intact")
	}
}

func TestContextManager_Summarize_PreservesWholeRecentGroup(t *testing.T) {
	cm := NewManager(500)
	ctx := context.Background()
	prov := &mockProvider{}

	cm.Add(provider.Message{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "System prompt."}}})
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "old question"}}})
	cm.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{provider.ToolUseBlock("call-old", "grep", []byte(`{}`))}})
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "tool_result", ToolID: "call-old", Output: strings.Repeat("O", 800)}}})
	cm.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "old answer"}}})
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "recent question"}}})
	cm.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{provider.ToolUseBlock("call-recent", "grep", []byte(`{}`))}})
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "tool_result", ToolID: "call-recent", Output: "recent tool output"}}})
	cm.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "recent answer"}}})

	if err := cm.Summarize(ctx, prov); err != nil {
		t.Fatalf("Summarize failed: %v", err)
	}

	msgs := cm.Messages()
	if len(msgs) < 6 {
		t.Fatalf("expected summary + preserved recent group, got %d messages", len(msgs))
	}
	if !containsSummaryMarker(msgs[1]) {
		t.Fatal("expected summary marker after system prompt")
	}
	foundRecentQuestion := false
	foundRecentTool := false
	foundRecentAnswer := false
	for _, msg := range msgs {
		foundRecentQuestion = foundRecentQuestion || messageContainsText(msg, "recent question")
		foundRecentTool = foundRecentTool || messageContainsText(msg, "recent tool output")
		foundRecentAnswer = foundRecentAnswer || messageContainsText(msg, "recent answer")
	}
	if !foundRecentQuestion || !foundRecentTool || !foundRecentAnswer {
		t.Fatal("expected whole recent group to remain after summarization")
	}
}

func TestContextManager_CheckAndSummarizeReturnsFalseWhenNothingCanBeCompacted(t *testing.T) {
	cm := NewManager(120)
	ctx := context.Background()
	prov := &mockProvider{}

	cm.Add(provider.Message{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "System prompt."}}})
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: strings.Repeat("recent question ", 20)}}})
	cm.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: strings.Repeat("recent answer ", 20)}}})

	summarized, err := cm.CheckAndSummarize(ctx, prov)
	if err != nil {
		t.Fatalf("CheckAndSummarize failed: %v", err)
	}
	if summarized {
		t.Fatal("expected CheckAndSummarize to report no change when only the current group remains")
	}
	if prov.chatCalls != 0 {
		t.Fatalf("expected no summarization chat call, got %d", prov.chatCalls)
	}
}

func TestContextManager_Summarize_RetriesPromptTooLongByDroppingOldestGroup(t *testing.T) {
	ctx := context.Background()
	prov := &promptTooLongOnceProvider{}
	msgs := []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "oldest question"}}},
		{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "oldest answer"}}},
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "middle question"}}},
		{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "middle answer"}}},
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "recent question"}}},
		{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "recent answer"}}},
	}
	summary, err := summarizeMessages(ctx, prov, msgs)
	if err != nil {
		t.Fatalf("summarizeMessages failed: %v", err)
	}
	if prov.chatCalls != 2 {
		t.Fatalf("expected one PTL retry, got %d Chat calls", prov.chatCalls)
	}
	if summary == "" {
		t.Fatal("expected non-empty summary after retry")
	}
}

func TestContextManager_Summarize_ReinjectsPostCompactState(t *testing.T) {
	workspace := t.TempDir()
	ggcodeDir := filepath.Join(workspace, ".ggcode")
	if err := os.MkdirAll(ggcodeDir, 0755); err != nil {
		t.Fatalf("mkdir .ggcode: %v", err)
	}
	todos := []map[string]string{
		{"id": "todo-1", "content": "Finish retry logic", "status": "in_progress"},
		{"id": "todo-2", "content": "Write docs", "status": "pending"},
	}
	data, err := json.Marshal(todos)
	if err != nil {
		t.Fatalf("marshal todos: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ggcodeDir, "todos.json"), data, 0644); err != nil {
		t.Fatalf("write todos: %v", err)
	}

	cm := NewManager(500)
	cm.SetTodoFilePath(toolpkg.TodoFilePath(workspace))
	ctx := context.Background()
	prov := &mockProvider{}

	cm.Add(provider.Message{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "System prompt."}}})
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "Please inspect the files"}}})
	cm.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{
		provider.ToolUseBlock("call-1", "read_file", []byte(`{"path":"internal/context/manager.go"}`)),
	}})
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "tool_result", ToolID: "call-1", Output: "file contents"}}})
	cm.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{
		provider.ToolUseBlock("call-2", "edit_file", []byte(`{"file_path":"internal/agent/agent.go","old_text":"x","new_text":"y"}`)),
	}})
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "tool_result", ToolID: "call-2", Output: "edit complete"}}})
	cm.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "Done."}}})
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "What changed?"}}})
	cm.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "Summary incoming."}}})

	if err := cm.Summarize(ctx, prov); err != nil {
		t.Fatalf("Summarize failed: %v", err)
	}

	msgs := cm.Messages()
	foundState := false
	for _, msg := range msgs {
		for _, block := range msg.Content {
			if block.Type != "text" {
				continue
			}
			if strings.Contains(block.Text, "[Post-compact state]") {
				foundState = true
				if !strings.Contains(block.Text, "internal/context/manager.go") || !strings.Contains(block.Text, "internal/agent/agent.go") {
					t.Fatalf("expected recent file paths in reinjected state: %q", block.Text)
				}
				if !strings.Contains(block.Text, "Todo state: 2 total") || !strings.Contains(block.Text, "todo-1") {
					t.Fatalf("expected todo summary in reinjected state: %q", block.Text)
				}
			}
		}
	}
	if !foundState {
		t.Fatal("expected post-compact state message to be reinjected")
	}
}

func TestContextManager_UsesProviderTokenCount(t *testing.T) {
	cm := NewManager(1000)
	cm.SetProvider(&mockProvider{})
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "hello"}}})
	if got := cm.TokenCount(); got != 200 {
		t.Fatalf("expected provider token count 200, got %d", got)
	}
}

func TestContextManager_CountTokensTimeoutFallsBack(t *testing.T) {
	cm := NewManager(1000)
	cm.SetProvider(&blockingCountProvider{})
	start := time.Now()
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "hello world"}}})
	elapsed := time.Since(start)
	if elapsed > 500*time.Millisecond {
		t.Fatalf("expected Add to return quickly after timeout, took %v", elapsed)
	}
	if cm.TokenCount() == 0 {
		t.Fatal("expected fallback heuristic token count")
	}
}

func retainedConversationMessages(msgs []provider.Message) int {
	count := 0
	for i, msg := range msgs {
		if msg.Role == "system" {
			if i == 0 {
				continue
			}
			if containsSummaryMarker(msg) {
				continue
			}
		}
		count++
	}
	return count
}

func containsSummaryMarker(msg provider.Message) bool {
	for _, block := range msg.Content {
		if block.Type == "text" && strings.Contains(block.Text, "[Previous conversation summary]") {
			return true
		}
	}
	return false
}

func messageContainsText(msg provider.Message, needle string) bool {
	for _, block := range msg.Content {
		if strings.Contains(block.Text, needle) || strings.Contains(block.Output, needle) {
			return true
		}
	}
	return false
}
