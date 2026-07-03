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

func TestContextManager_Summarize_RollingCompactionSummarizesAll(t *testing.T) {
	ctx := context.Background()
	prov := &mockProvider{}

	cm := NewManager(100000)
	cm.Add(provider.Message{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "System prompt."}}})

	for i := 0; i < 50; i++ {
		cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: fmt.Sprintf("user-%d", i)}}})
		cm.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: fmt.Sprintf("assistant-%d", i)}}})
	}

	if err := cm.Summarize(ctx, prov); err != nil {
		t.Fatalf("summarize failed: %v", err)
	}

	msgs := cm.Messages()
	// Rolling compaction: [system][summary_system] — no recent conversation messages retained
	if len(msgs) != 2 {
		t.Fatalf("expected system + summary = 2 messages, got %d", len(msgs))
	}
	if msgs[0].Role != "system" {
		t.Fatalf("expected first message to be system, got %s", msgs[0].Role)
	}
	if msgs[1].Role != "system" || !strings.Contains(msgs[1].Content[0].Text, "[Previous conversation summary]") {
		t.Fatalf("expected second message to be summary, got role=%s", msgs[1].Role)
	}
	// No conversation messages should be retained verbatim
	retained := retainedConversationMessages(msgs)
	if retained != 0 {
		t.Fatalf("expected 0 retained conversation messages, got %d", retained)
	}
}

func TestContextManager_Summarize_BringsUsageBelowThreshold(t *testing.T) {
	cm := NewManager(100000)
	ctx := context.Background()
	prov := &mockProvider{}

	cm.Add(provider.Message{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "System prompt."}}})
	// Add enough messages to exceed recentBudgetFixed (15K tokens) so
	// some messages get summarized and others are retained as recent.
	for i := 0; i < 1200; i++ {
		cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: fmt.Sprintf("message-%d %s", i, strings.Repeat("z", 320))}}})
	}

	preCompact := cm.TokenCount()
	if preCompact <= cm.AutoCompactThreshold() {
		t.Fatalf("expected setup to exceed auto-compact threshold, got tokens=%d threshold=%d", preCompact, cm.AutoCompactThreshold())
	}

	if err := cm.Summarize(ctx, prov); err != nil {
		t.Fatalf("Summarize failed: %v", err)
	}

	postCompact := cm.TokenCount()
	// After summarization, token count must be significantly lower.
	if postCompact >= preCompact {
		t.Fatalf("expected summarized context to be smaller: pre=%d post=%d", preCompact, postCompact)
	}
}

func TestContextManager_ApplyCompactResultPreservesMessagesAppendedAfterSnapshot(t *testing.T) {
	cm := NewManager(1000)
	ctx := context.Background()
	prov := &mockProvider{}

	cm.Add(provider.Message{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "System prompt."}}})
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: strings.Repeat("old question ", 80)}}})
	cm.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: strings.Repeat("old answer ", 80)}}})
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "recent snapshot question"}}})
	cm.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "recent snapshot answer"}}})

	snapshot := cm.CompactSnapshot()
	result, err := snapshot.Compact(ctx, prov)
	if err != nil {
		t.Fatalf("snapshot compact failed: %v", err)
	}

	cm.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{provider.ToolUseBlock("call-1", "read", []byte(`{}`))}})
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "tool_result", ToolID: "call-1", Output: "tool output created while compacting"}}})

	applied, _ := cm.ApplyCompactResult(snapshot, result)
	if !applied {
		t.Fatal("expected compact result to apply")
	}
	msgs := cm.Messages()
	if !messageContainsTextInList(msgs, "[Previous conversation summary]") {
		t.Fatal("expected compacted summary to be inserted")
	}
	// Rolling compaction: snapshot messages go into summary, not preserved verbatim.
	// Only messages appended DURING the compaction window are preserved as extra.
	if !messageContainsTextInList(msgs, "tool output created while compacting") {
		t.Fatal("expected messages appended during async compaction to be preserved")
	}
}

func TestContextManager_VersionCounter_IncrementsOnMutation(t *testing.T) {
	cm := NewManager(1000)

	// Initial version is 0.
	if v := cm.version; v != 0 {
		t.Fatalf("initial version = %d, want 0", v)
	}

	// Add increments version.
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "hello"}}})
	if v := cm.version; v != 1 {
		t.Fatalf("after Add: version = %d, want 1", v)
	}

	// PrependSystem (via UpdateFirstSystemMessage when no system exists) increments version.
	cm.UpdateFirstSystemMessage(provider.Message{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "sys prompt"}}})
	if v := cm.version; v != 2 {
		t.Fatalf("after UpdateFirstSystemMessage prepend: version = %d, want 2", v)
	}

	// UpdateFirstSystemMessage replacing existing system message does NOT increment version.
	cm.UpdateFirstSystemMessage(provider.Message{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "new sys"}}})
	if v := cm.version; v != 2 {
		t.Fatalf("after UpdateFirstSystemMessage replace: version = %d, want 2 (no increment)", v)
	}

	// Clear increments version.
	cm.Clear()
	if v := cm.version; v != 3 {
		t.Fatalf("after Clear: version = %d, want 3", v)
	}
}

func TestContextManager_ApplyCompactResult_VersionMatch_FastPath(t *testing.T) {
	// When version matches snapshot exactly, ApplyCompactResult should
	// succeed without any deep inspection of messages.
	cm := NewManager(1000)
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: strings.Repeat("x ", 50)}}})
	cm.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: strings.Repeat("y ", 50)}}})

	snapshot := cm.CompactSnapshot()
	// snapshot.Version == cm.version, no messages appended after snapshot.

	result := CompactResult{
		Messages:   []provider.Message{{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "summary"}}}},
		TokenCount: 5,
		Changed:    true,
	}

	applied, _ := cm.ApplyCompactResult(snapshot, result)
	if !applied {
		t.Fatal("expected compact result to apply when version matches")
	}
}

func TestContextManager_ApplyCompactResult_VersionMismatch_AppendAllowed(t *testing.T) {
	// If version changed only because messages were appended after snapshot,
	// the snapshot is still valid and should apply.
	cm := NewManager(1000)
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "q1"}}})
	cm.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "a1"}}})

	snapshot := cm.CompactSnapshot()

	// Append after snapshot — version changes but first OrigLen messages unchanged.
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "q2"}}})
	if cm.version == snapshot.Version {
		t.Fatal("expected version to change after Add")
	}

	result := CompactResult{
		Messages:   []provider.Message{{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "summary"}}}},
		TokenCount: 5,
		Changed:    true,
	}

	applied, _ := cm.ApplyCompactResult(snapshot, result)
	if !applied {
		t.Fatal("expected compact result to apply when only append happened after snapshot")
	}
	msgs := cm.Messages()
	// Should have: [summary, q2]
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages after apply, got %d", len(msgs))
	}
	if msgs[1].Content[0].Text != "q2" {
		t.Fatal("expected appended message q2 to be preserved")
	}
}

func TestContextManager_ApplyCompactResult_StaleSnapshot_Rejected(t *testing.T) {
	// If messages within the snapshot range are modified (not just appended),
	// the snapshot is stale and should be rejected.
	cm := NewManager(1000)
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "q1"}}})
	cm.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "a1"}}})

	snapshot := cm.CompactSnapshot()

	// Simulate stale snapshot: clear and re-add with different roles.
	cm.Clear()
	cm.Add(provider.Message{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "new system"}}})

	result := CompactResult{
		Messages:   []provider.Message{{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "summary"}}}},
		TokenCount: 5,
		Changed:    true,
	}

	applied, _ := cm.ApplyCompactResult(snapshot, result)
	if applied {
		t.Fatal("expected stale snapshot to be rejected")
	}
}

func TestContextManager_ApplyCompactResult_AppendAfterSnapshot_RoleFallback(t *testing.T) {
	// Version mismatch but roles match within OrigLen range → should apply.
	cm := NewManager(1000)
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "q1"}}})
	cm.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "a1"}}})

	snapshot := cm.CompactSnapshot()

	// Append changes version, but first 2 messages still have same roles.
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "q2"}}})

	result := CompactResult{
		Messages:   []provider.Message{{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "summary"}}}},
		TokenCount: 5,
		Changed:    true,
	}

	applied, _ := cm.ApplyCompactResult(snapshot, result)
	if !applied {
		t.Fatal("expected snapshot to apply when roles match within OrigLen range")
	}
}

func TestContextManager_Compact_VersionDetectsChange(t *testing.T) {
	// Compact() on a scratch manager should detect changes via version counter
	// instead of reflect.DeepEqual.
	ctx := context.Background()
	prov := &mockProvider{}

	cm := NewManager(200)
	cm.SetOutputReserve(50)
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: strings.Repeat("old question ", 30)}}})
	cm.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: strings.Repeat("old answer ", 30)}}})
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "recent q"}}})

	snapshot := cm.CompactSnapshot()
	result, err := snapshot.Compact(ctx, prov)
	if err != nil {
		t.Fatalf("Compact failed: %v", err)
	}
	if !result.Changed {
		t.Fatal("expected Compact to detect changes via version counter")
	}
}

func TestContextManager_CheckAndSummarize_VersionDetectsChange(t *testing.T) {
	// CheckAndSummarize should detect changes via version counter.
	ctx := context.Background()
	prov := &mockProvider{}

	cm := NewManager(300)
	cm.SetOutputReserve(50)
	cm.SetProvider(prov)

	// Add enough messages to exceed the auto-compact threshold.
	// threshold = (300-50) * 0.75 ≈ 187 tokens
	// Each message ~ 30*9/4 ≈ 67 tokens, need at least 3 pairs (6 messages ≈ 400 tokens).
	for i := 0; i < 6; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		cm.Add(provider.Message{Role: role, Content: []provider.ContentBlock{{Type: "text", Text: strings.Repeat(fmt.Sprintf("message%d ", i), 30)}}})
	}

	changed, err := cm.CheckAndSummarize(ctx, prov)
	if err != nil {
		t.Fatalf("CheckAndSummarize failed: %v", err)
	}
	if !changed {
		t.Fatal("expected CheckAndSummarize to detect changes via version counter")
	}
}

func TestContextManager_CompactSnapshot_CapturesVersion(t *testing.T) {
	cm := NewManager(1000)
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "q1"}}})

	snapshot := cm.CompactSnapshot()
	if snapshot.Version != 1 {
		t.Fatalf("snapshot.Version = %d, want 1", snapshot.Version)
	}

	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "q2"}}})
	snapshot2 := cm.CompactSnapshot()
	if snapshot2.Version != 2 {
		t.Fatalf("snapshot2.Version = %d, want 2", snapshot2.Version)
	}
}

func TestContextManager_ApplyCompactResult_StaleSnapshotStillApplied(t *testing.T) {
	// If messages within the snapshot range are modified after the snapshot
	// was taken, the compaction result should STILL be applied.
	// The summary is lossy compression — a slightly stale source is acceptable.
	cm := NewManager(1000)
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "q1"}}})
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{
		{Type: "tool_result", ToolID: "c1", Output: strings.Repeat("long output ", 100)},
	}})
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "q2"}}})

	snapshot := cm.CompactSnapshot()

	// Manually compact the tool result in-place (simulating Microcompact).
	cm.mu.Lock()
	msg := cm.messages[1]
	msg.Content[0].Output = "[compacted]"
	cm.messages[1] = msg
	cm.version++
	cm.recalcTokens()
	cm.mu.Unlock()

	result := CompactResult{
		Messages:   []provider.Message{{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "summary"}}}},
		TokenCount: 5,
		Changed:    true,
	}

	applied, _ := cm.ApplyCompactResult(snapshot, result)
	if !applied {
		t.Fatal("expected compact result to be applied even with stale snapshot (lossy summary is acceptable)")
	}
	msgs := cm.Messages()
	if len(msgs) != 1 || msgs[0].Content[0].Text != "summary" {
		t.Fatalf("expected only summary message, got %d messages", len(msgs))
	}
}

func TestContextManager_ApplyCompactResult_SystemMessageChangeAllowed(t *testing.T) {
	// The system message is dynamically updated (lanchat peers, memory, etc.)
	// via UpdateFirstSystemMessage which replaces messages[0] without incrementing
	// version. ApplyCompactResult should NOT reject the snapshot when the system
	// message content changed, and should preserve the LIVE system message.
	cm := NewManager(1000)
	cm.Add(provider.Message{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "original system prompt"}}})
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "q1"}}})
	cm.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "a1"}}})

	snapshot := cm.CompactSnapshot()

	// Simulate dynamic system prompt update (lanchat peers changed, etc.)
	cm.UpdateFirstSystemMessage(provider.Message{
		Role:    "system",
		Content: []provider.ContentBlock{{Type: "text", Text: "updated system prompt with dynamic content"}},
	})

	// Simulate agent loop appending messages after snapshot.
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "q2"}}})

	result := CompactResult{
		Messages: []provider.Message{
			{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "old system from snapshot"}}},
			{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "summary"}}},
		},
		TokenCount: 5,
		Changed:    true,
	}

	applied, _ := cm.ApplyCompactResult(snapshot, result)
	if !applied {
		t.Fatal("expected compact result to apply despite system message change")
	}

	msgs := cm.Messages()
	if len(msgs) == 0 || msgs[0].Role != "system" {
		t.Fatal("expected first message to be system")
	}
	if msgs[0].Content[0].Text != "updated system prompt with dynamic content" {
		t.Fatalf("expected live system message to be preserved, got: %s", msgs[0].Content[0].Text)
	}
	// Appended message should still be there.
	if !messageContainsTextInList(msgs, "q2") {
		t.Fatal("expected appended message q2 to be preserved")
	}
}

func TestContextManager_ApplyCompactResult_LiveShorterThanSnapshot_Rejected(t *testing.T) {
	// If messages were removed (live shorter than snapshot), the snapshot is
	// structurally incompatible and must be rejected.
	cm := NewManager(1000)
	cm.Add(provider.Message{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "sys"}}})
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "q1"}}})
	cm.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "a1"}}})

	snapshot := cm.CompactSnapshot()

	// Structural change: clear and re-add fewer messages.
	cm.Clear()
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "only one message"}}})

	result := CompactResult{
		Messages:   []provider.Message{{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "summary"}}}},
		TokenCount: 5,
		Changed:    true,
	}

	applied, _ := cm.ApplyCompactResult(snapshot, result)
	if applied {
		t.Fatal("expected snapshot to be rejected when live has fewer messages than snapshot")
	}
}

func messageContainsTextInList(messages []provider.Message, want string) bool {
	for _, msg := range messages {
		if messageContainsText(msg, want) {
			return true
		}
	}
	return false
}

func TestContextManager_Summarize_PreservesExtraMessages(t *testing.T) {
	// Rolling compaction: ALL snapshot messages are summarized.
	// Messages produced during the async compaction window are preserved.
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
	// Rolling compaction: [system, summary_system] only.
	// No conversation messages retained verbatim.
	if len(msgs) != 2 {
		t.Fatalf("expected system + summary = 2 messages, got %d", len(msgs))
	}
	if !containsSummaryMarker(msgs[1]) {
		t.Fatal("expected summary marker after system prompt")
	}
	retained := retainedConversationMessages(msgs)
	if retained != 0 {
		t.Fatalf("expected 0 retained conversation messages, got %d", retained)
	}
}

func TestContextManager_Summarize_SingleGroupCanBeSummarized(t *testing.T) {
	// With rolling compaction (minRecentGroups=0), even a single group
	// can be summarized. This test verifies the behavior.
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
	// Single group can now be summarized with rolling compaction.
	if !summarized {
		t.Fatal("expected CheckAndSummarize to summarize even a single group")
	}
	if prov.chatCalls == 0 {
		t.Fatal("expected at least one summarization chat call")
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
	summary, err := summarizeMessages(ctx, prov, msgs, nil, 10000)
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
	sessionID := "test-compact-session"
	todoPath := toolpkg.TodoFilePath(sessionID)
	if err := os.MkdirAll(filepath.Dir(todoPath), 0755); err != nil {
		t.Fatalf("mkdir todos dir: %v", err)
	}
	todos := []map[string]string{
		{"id": "todo-1", "content": "Finish retry logic", "status": "in_progress"},
		{"id": "todo-2", "content": "Write docs", "status": "pending"},
	}
	data, err := json.Marshal(todos)
	if err != nil {
		t.Fatalf("marshal todos: %v", err)
	}
	if err := os.WriteFile(todoPath, data, 0644); err != nil {
		t.Fatalf("write todos: %v", err)
	}

	cm := NewManager(500)
	cm.SetTodoFilePath(todoPath)
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

func TestContextManager_RecordUsageUsesBaselinePlusDelta(t *testing.T) {
	cm := NewManager(1000)
	cm.RecordUsage(provider.TokenUsage{InputTokens: 620})

	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: strings.Repeat("x", 80)}}})

	if got := cm.TokenCount(); got <= 620 {
		t.Fatalf("expected token count to grow from recorded baseline, got %d", got)
	}
	if got := cm.AutoCompactThreshold(); got <= 0 {
		t.Fatalf("expected positive threshold, got %d", got)
	}
}

func TestContextManager_SetCheckpointBaseline(t *testing.T) {
	cm := NewManager(256000)

	// Add messages that would normally produce a rough estimate.
	// Use enough text to create a meaningful divergence from the checkpoint value.
	for i := 0; i < 50; i++ {
		cm.Add(provider.Message{
			Role:    "user",
			Content: []provider.ContentBlock{{Type: "text", Text: strings.Repeat("x", 400)}},
		})
	}

	// Before SetCheckpointBaseline, token count is from local estimation.
	estimate := cm.TokenCount()
	if estimate == 0 {
		t.Fatal("expected non-zero local estimate before baseline")
	}

	// Apply checkpoint baseline — simulates session restore with known token count.
	cm.SetCheckpointBaseline(158811)

	// Token count should now reflect the checkpoint, not the estimate.
	got := cm.TokenCount()
	if got != 158811 {
		t.Fatalf("expected 158811 after checkpoint baseline, got %d (estimate was %d)", got, estimate)
	}
}

func TestContextManager_SetCheckpointBaselineZeroIgnored(t *testing.T) {
	cm := NewManager(1000)
	cm.Add(provider.Message{
		Role:    "user",
		Content: []provider.ContentBlock{{Type: "text", Text: "hello world"}},
	})

	original := cm.TokenCount()

	// Zero or negative should be ignored.
	cm.SetCheckpointBaseline(0)
	cm.SetCheckpointBaseline(-1)

	if got := cm.TokenCount(); got != original {
		t.Fatalf("expected %d (unchanged), got %d", original, got)
	}
}

func TestContextManager_SetCheckpointBaselineThenAddMessages(t *testing.T) {
	cm := NewManager(256000)

	// Simulate checkpoint messages loaded first.
	cm.Add(provider.Message{
		Role:    "system",
		Content: []provider.ContentBlock{{Type: "text", Text: strings.Repeat("s", 50000)}},
	})
	// Set baseline AFTER checkpoint messages, BEFORE post-checkpoint messages.
	cm.SetCheckpointBaseline(100000)

	// Now add post-checkpoint messages — these should increment baselineDelta.
	postMsg := provider.Message{
		Role:    "user",
		Content: []provider.ContentBlock{{Type: "text", Text: "new message after restore"}},
	}
	cm.Add(postMsg)

	got := cm.TokenCount()
	// With baseline available, TokenCount = baselineTokens + baselineDelta.
	// baselineDelta should include the post-checkpoint message tokens.
	if got <= 100000 {
		t.Fatalf("expected > 100000 after baseline + new message, got %d", got)
	}
	delta := got - 100000
	if delta < 1 {
		t.Fatalf("expected positive delta for post-checkpoint message, got %d", delta)
	}

	// Add another message — delta should grow.
	cm.Add(provider.Message{
		Role:    "assistant",
		Content: []provider.ContentBlock{{Type: "text", Text: "assistant response"}},
	})
	got2 := cm.TokenCount()
	if got2 <= got {
		t.Fatalf("expected token count to grow after second post-checkpoint message: %d -> %d", got, got2)
	}
}

func TestContextManager_RecordUsageOverridesCheckpointBaseline(t *testing.T) {
	cm := NewManager(256000)

	cm.Add(provider.Message{
		Role:    "system",
		Content: []provider.ContentBlock{{Type: "text", Text: strings.Repeat("s", 50000)}},
	})
	cm.SetCheckpointBaseline(100000)

	// First real LLM call should override with actual token count.
	cm.RecordUsage(provider.TokenUsage{InputTokens: 95000})

	got := cm.TokenCount()
	if got != 95000 {
		t.Fatalf("expected 95000 after RecordUsage override, got %d", got)
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

func TestRemoveLastAssistantGroup(t *testing.T) {
	m := NewManager(100000)

	// Add a conversation: user → assistant → tool → assistant → user → assistant
	m.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "Hello"}}})
	m.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "Hi there!"}}})
	m.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "What is 2+2?"}}})
	m.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "It's 4."}}})
	m.Add(provider.Message{Role: "tool", Content: []provider.ContentBlock{{Type: "text", Text: "calculator result: 4"}}})

	beforeTokens := m.TokenCount()
	beforeMsgs := len(m.Messages())

	// Remove last assistant group
	userText := m.RemoveLastAssistantGroup()

	// Should return the last user message text
	if userText != "What is 2+2?" {
		t.Errorf("expected last user text 'What is 2+2?', got %q", userText)
	}

	// Should have removed 2 messages (assistant + tool)
	afterMsgs := len(m.Messages())
	if afterMsgs != beforeMsgs-2 {
		t.Errorf("expected %d messages after removal, got %d", beforeMsgs-2, afterMsgs)
	}

	// Last message should now be the user message
	msgs := m.Messages()
	if msgs[len(msgs)-1].Role != "user" {
		t.Errorf("expected last message to be user, got %s", msgs[len(msgs)-1].Role)
	}

	// Tokens should have decreased
	if m.TokenCount() >= beforeTokens {
		t.Error("expected token count to decrease after removal")
	}
}

func TestRemoveLastAssistantGroup_NoAssistant(t *testing.T) {
	m := NewManager(100000)
	m.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "Hello"}}})

	result := m.RemoveLastAssistantGroup()
	if result != "" {
		t.Errorf("expected empty string when no assistant message, got %q", result)
	}
	if len(m.Messages()) != 1 {
		t.Error("expected messages unchanged")
	}
}

func TestRemoveLastAssistantGroup_Empty(t *testing.T) {
	m := NewManager(100000)
	result := m.RemoveLastAssistantGroup()
	if result != "" {
		t.Errorf("expected empty string for empty context, got %q", result)
	}
}

func TestClearOldToolResults_BasicClearing(t *testing.T) {
	m := NewManager(100000)
	// Add 4 assistant→tool_result pairs with large outputs.
	for i := 0; i < 4; i++ {
		m.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{
			provider.ToolUseBlock(fmt.Sprintf("call-%d", i), "read_file", []byte(`{}`)),
		}})
		m.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{
			{Type: "tool_result", ToolID: fmt.Sprintf("call-%d", i), Output: strings.Repeat("x", 1000)},
		}})
	}
	beforeTokens := m.TokenCount()
	// keepN=2: should clear the first 2 results, keep last 2.
	freed := m.ClearOldToolResults(2)
	if freed <= 0 {
		t.Fatal("expected positive tokens freed")
	}
	if m.TokenCount() >= beforeTokens {
		t.Error("expected token count to decrease after clearing")
	}
	msgs := m.Messages()
	// Verify first 2 results are cleared
	for i := 0; i < 2; i++ {
		result := findToolResult(t, msgs, fmt.Sprintf("call-%d", i))
		if !strings.HasPrefix(result.Output, "[cleared:") {
			t.Errorf("expected call-%d output to be cleared, got %q", i, result.Output[:min(50, len(result.Output))])
		}
	}
	// Verify last 2 results are intact
	for i := 2; i < 4; i++ {
		result := findToolResult(t, msgs, fmt.Sprintf("call-%d", i))
		if strings.HasPrefix(result.Output, "[cleared:") {
			t.Errorf("expected call-%d output to be intact", i)
		}
		if len(result.Output) != 1000 {
			t.Errorf("expected call-%d output to be 1000 chars, got %d", i, len(result.Output))
		}
	}
}

func TestClearOldToolResults_Idempotent(t *testing.T) {
	m := NewManager(100000)
	for i := 0; i < 4; i++ {
		m.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{
			provider.ToolUseBlock(fmt.Sprintf("call-%d", i), "read_file", []byte(`{}`)),
		}})
		m.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{
			{Type: "tool_result", ToolID: fmt.Sprintf("call-%d", i), Output: strings.Repeat("y", 1000)},
		}})
	}
	// First call clears some
	first := m.ClearOldToolResults(2)
	if first <= 0 {
		t.Fatal("expected tokens freed on first call")
	}
	// Second call should be a no-op (all clearable results already cleared)
	second := m.ClearOldToolResults(2)
	if second != 0 {
		t.Errorf("expected 0 tokens freed on second call, got %d", second)
	}
}

func TestClearOldToolResults_ErrorResultsPreserved(t *testing.T) {
	m := NewManager(100000)
	// Error result should NOT be cleared
	m.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{
		provider.ToolUseBlock("err-call", "grep", []byte(`{}`)),
	}})
	m.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{
		{Type: "tool_result", ToolID: "err-call", Output: strings.Repeat("e", 1000), IsError: true},
	}})
	// Normal result that should be cleared
	m.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{
		provider.ToolUseBlock("ok-call", "read_file", []byte(`{}`)),
	}})
	m.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{
		{Type: "tool_result", ToolID: "ok-call", Output: strings.Repeat("f", 1000)},
	}})

	freed := m.ClearOldToolResults(0) // clear everything possible
	// Error result should be preserved
	errResult := findToolResult(t, m.Messages(), "err-call")
	if strings.HasPrefix(errResult.Output, "[cleared:") {
		t.Error("error result should not be cleared")
	}
	// Normal result should be cleared (keepN=0, so everything clearable gets cleared)
	okResult := findToolResult(t, m.Messages(), "ok-call")
	if !strings.HasPrefix(okResult.Output, "[cleared:") {
		t.Error("normal result should be cleared")
	}
	if freed <= 0 {
		t.Error("expected tokens freed")
	}
}

func TestClearOldToolResults_SmallResultsPreserved(t *testing.T) {
	m := NewManager(100000)
	// Small result (< 500 chars) should NOT be cleared
	m.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{
		provider.ToolUseBlock("small-call", "run_command", []byte(`{}`)),
	}})
	m.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{
		{Type: "tool_result", ToolID: "small-call", Output: "ok"},
	}})

	freed := m.ClearOldToolResults(0)
	if freed != 0 {
		t.Errorf("expected 0 tokens freed (result too small), got %d", freed)
	}
}

func TestClearOldToolResults_TooFewResults(t *testing.T) {
	m := NewManager(100000)
	// Only 2 results, keepN=5 → nothing to clear
	for i := 0; i < 2; i++ {
		m.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{
			provider.ToolUseBlock(fmt.Sprintf("call-%d", i), "read_file", []byte(`{}`)),
		}})
		m.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{
			{Type: "tool_result", ToolID: fmt.Sprintf("call-%d", i), Output: strings.Repeat("z", 1000)},
		}})
	}
	freed := m.ClearOldToolResults(5)
	if freed != 0 {
		t.Errorf("expected 0 freed (too few results), got %d", freed)
	}
}

func TestClearOldToolResults_ToolUsePreserved(t *testing.T) {
	m := NewManager(100000)
	// Verify tool_use blocks are never modified
	m.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{
		provider.ToolUseBlock("call-1", "read_file", []byte(`{"path":"/foo.go"}`)),
	}})
	m.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{
		{Type: "tool_result", ToolID: "call-1", Output: strings.Repeat("a", 1000)},
	}})

	m.ClearOldToolResults(0)
	msgs := m.Messages()
	// Find the assistant message with tool_use
	for _, msg := range msgs {
		if msg.Role != "assistant" {
			continue
		}
		for _, b := range msg.Content {
			if b.Type == "tool_use" {
				if b.ToolName != "read_file" {
					t.Errorf("tool_use name should be preserved, got %q", b.ToolName)
				}
				if string(b.Input) != `{"path":"/foo.go"}` {
					t.Errorf("tool_use input should be preserved, got %q", string(b.Input))
				}
			}
		}
	}
}

// findToolResult finds a tool_result block by tool_id in the message list.
func findToolResult(t *testing.T, msgs []provider.Message, toolID string) provider.ContentBlock {
	t.Helper()
	for _, msg := range msgs {
		for _, b := range msg.Content {
			if b.Type == "tool_result" && b.ToolID == toolID {
				return b
			}
		}
	}
	t.Fatalf("tool_result with id %s not found", toolID)
	return provider.ContentBlock{}
}
