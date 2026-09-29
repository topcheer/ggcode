package context

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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

func TestContextManager_Summarize_RetainsRecentGroup(t *testing.T) {
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
	// Adaptive recent group retention: [system][summary][last user][last assistant]
	// The most recent interaction group is kept verbatim for working context.
	if len(msgs) != 4 {
		t.Fatalf("expected system + summary + 2 recent = 4 messages, got %d", len(msgs))
	}
	if msgs[0].Role != "system" {
		t.Fatalf("expected first message to be system, got %s", msgs[0].Role)
	}
	if !containsSummaryMarker(msgs[1]) {
		t.Fatalf("expected second message to be summary")
	}
	// Last 2 messages should be the most recent interaction group (verbatim)
	if msgs[2].Role != "user" || !strings.Contains(msgs[2].Content[0].Text, "user-49") {
		t.Fatalf("expected retained user-49, got role=%s text=%q", msgs[2].Role, msgs[2].Content[0].Text)
	}
	if msgs[3].Role != "assistant" || !strings.Contains(msgs[3].Content[0].Text, "assistant-49") {
		t.Fatalf("expected retained assistant-49, got role=%s text=%q", msgs[3].Role, msgs[3].Content[0].Text)
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

func TestContextManager_PinnedContextSurvivesCompaction(t *testing.T) {
	cm := NewManager(1000)
	ctx := context.Background()
	prov := &mockProvider{}

	// Pin a critical piece of context before compaction.
	pinID, err := cm.Pinned().Add("CRITICAL: build with go build -tags goolm ./...")
	if err != nil {
		t.Fatalf("pin add failed: %v", err)
	}
	if pinID == "" {
		t.Fatal("expected non-empty pin ID")
	}

	cm.Add(provider.Message{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "System prompt."}}})
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: strings.Repeat("old question ", 80)}}})
	cm.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: strings.Repeat("old answer ", 80)}}})
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "recent question"}}})

	// Trigger compaction.
	snapshot := cm.CompactSnapshot()
	result, err := snapshot.Compact(ctx, prov)
	if err != nil {
		t.Fatalf("snapshot compact failed: %v", err)
	}

	applied, _ := cm.ApplyCompactResult(snapshot, result)
	if !applied {
		t.Fatal("expected compact result to apply")
	}

	msgs := cm.Messages()
	// Verify the summary was inserted.
	if !messageContainsTextInList(msgs, "[Previous conversation summary]") {
		t.Fatal("expected compaction summary")
	}
	// Verify pinned context survived compaction.
	if !messageContainsTextInList(msgs, "[Pinned Context") {
		t.Fatal("expected pinned context to survive compaction")
	}
	if !messageContainsTextInList(msgs, "go build -tags goolm") {
		t.Fatal("expected pinned text content to be present after compaction")
	}

	// Verify pinned message is positioned after the summary.
	summaryIdx := -1
	pinnedIdx := -1
	for i, msg := range msgs {
		if msg.Role == "system" && len(msg.Content) > 0 {
			text := msg.Content[0].Text
			if strings.Contains(text, "[Previous conversation summary]") {
				summaryIdx = i
			}
			if strings.Contains(text, "[Pinned Context") {
				pinnedIdx = i
			}
		}
	}
	if summaryIdx < 0 || pinnedIdx < 0 {
		t.Fatalf("summary=%d pinned=%d (both should be found)", summaryIdx, pinnedIdx)
	}
	if pinnedIdx <= summaryIdx {
		t.Fatalf("pinned context (idx %d) should come after summary (idx %d)", pinnedIdx, summaryIdx)
	}

	// Verify clearing pins removes the pinned message.
	cm.Pinned().Clear()
	// Trigger another compaction to re-apply.
	snapshot2 := cm.CompactSnapshot()
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: strings.Repeat("more content ", 80)}}})
	result2, err := snapshot2.Compact(ctx, prov)
	if err != nil {
		t.Fatalf("second compact failed: %v", err)
	}
	cm.ApplyCompactResult(snapshot2, result2)

	msgs2 := cm.Messages()
	if messageContainsTextInList(msgs2, "[Pinned Context") {
		t.Fatal("expected pinned message removed after Clear + compaction")
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

	// Append after snapshot - version changes but first OrigLen messages unchanged.
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
		t.Fatal("expected stale snapshot to be REJECTED (#651): live shrank below snapshot size after Clear - applying the lossy summary would resurrect dropped content")
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
	// The summary is lossy compression - a slightly stale source is acceptable.
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
		t.Fatal("expected snapshot to be REJECTED when live has fewer messages than the snapshot (#651 live-shrunk): messages were removed during the compaction window")
	}
	// Rejected apply must not mutate the live context.
	if len(cm.Messages()) == 0 {
		t.Fatal("rejected apply must leave live messages intact")
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
	// Adaptive retention: the most recent interaction group is kept verbatim.
	// Messages produced during the async compaction window are also preserved.
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
	// [system, summary, recent_question, recent_tool_call, recent_tool_result, recent_answer]
	if len(msgs) != 6 {
		t.Fatalf("expected system + summary + 4 recent = 6 messages, got %d", len(msgs))
	}
	if !containsSummaryMarker(msgs[1]) {
		t.Fatal("expected summary marker after system prompt")
	}
	// The most recent interaction group must be retained verbatim
	if msgs[2].Role != "user" || !strings.Contains(msgs[2].Content[0].Text, "recent question") {
		t.Fatalf("expected retained 'recent question', got role=%s", msgs[2].Role)
	}
	if msgs[5].Role != "assistant" || !strings.Contains(msgs[5].Content[0].Text, "recent answer") {
		t.Fatalf("expected retained 'recent answer', got role=%s", msgs[5].Role)
	}
}

func TestContextManager_Summarize_SingleGroupCanBeSummarized(t *testing.T) {
	// With minRecentGroups=1, a single group still gets summarized because
	// there must be at least minRecentGroups+1 groups (something to summarize + something to keep).
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
	summary, err := summarizeMessages(ctx, prov, msgs, nil, 10000, "")
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

	// Apply checkpoint baseline - simulates session restore with known token count.
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

	// Now add post-checkpoint messages - these should increment baselineDelta.
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

	// Add another message - delta should grow.
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

func TestFormatToolInputForSummary(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		maxLen  int
		wantHas []string // substrings that must be present
		wantNot []string // substrings that must NOT be present
	}{
		{
			name:    "read_file with path",
			input:   `{"path":"/internal/context/manager.go"}`,
			maxLen:  300,
			wantHas: []string{"path=", "manager.go"},
		},
		{
			name:    "run_command with command",
			input:   `{"command":"go test -race ./..."}`,
			maxLen:  300,
			wantHas: []string{"command=", "go test"},
		},
		{
			name:    "edit_file with old_text and new_text",
			input:   `{"file_path":"main.go","old_text":"old","new_text":"new"}`,
			maxLen:  300,
			wantHas: []string{"file_path=", "main.go"},
		},
		{
			name:    "empty input",
			input:   `{}`,
			maxLen:  300,
			wantHas: nil,
		},
		{
			name:    "long input truncated",
			input:   `{"path":"` + strings.Repeat("x", 200) + `"}`,
			maxLen:  50,
			wantHas: []string{"..."},
		},
		{
			name:    "nil input",
			input:   ``,
			maxLen:  300,
			wantHas: nil,
		},
		{
			name:    "search with pattern and query",
			input:   `{"pattern":"func.*Summarize","query":"context engineering"}`,
			maxLen:  300,
			wantHas: []string{"pattern=", "query="},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatToolInputForSummary([]byte(tt.input), tt.maxLen)
			for _, s := range tt.wantHas {
				if !strings.Contains(result, s) {
					t.Errorf("expected result to contain %q, got: %s", s, result)
				}
			}
			for _, s := range tt.wantNot {
				if strings.Contains(result, s) {
					t.Errorf("expected result to NOT contain %q, got: %s", s, result)
				}
			}
			if len(result) > tt.maxLen+10 { // allow small overhead
				t.Errorf("result length %d exceeds maxLen %d: %s", len(result), tt.maxLen, result)
			}
		})
	}
}

func TestBuildSummaryPayloadIncludesToolInputs(t *testing.T) {
	msgs := []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "Read the file"}}},
		{Role: "assistant", Content: []provider.ContentBlock{
			provider.ToolUseBlock("call-1", "read_file", []byte(`{"path":"internal/agent/agent.go"}`)),
		}},
		{Role: "user", Content: []provider.ContentBlock{{Type: "tool_result", ToolID: "call-1", Output: "file contents here"}}},
	}

	payload := buildSummaryPayload(msgs)

	// The payload must include the tool name AND the input path
	if !strings.Contains(payload, "read_file") {
		t.Error("payload missing tool name 'read_file'")
	}
	if !strings.Contains(payload, "agent.go") {
		t.Error("payload missing tool input path 'agent.go'")
	}
	if !strings.Contains(payload, "path=") {
		t.Error("payload missing 'path=' key from tool input")
	}
}

// ── ClearOldToolUseInputs tests ──

func TestBuildSummaryPayload_IncludesUserRequests(t *testing.T) {
	msgs := []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "Fix the memory leak in agent.go"}}},
		{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "I'll investigate."}}},
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "Also add tests for the fix"}}},
		{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "Done."}}},
	}

	payload := buildSummaryPayload(msgs)

	if !strings.Contains(payload, "VERBATIM USER REQUESTS") {
		t.Error("payload missing VERBATIM USER REQUESTS section")
	}
	if !strings.Contains(payload, "Fix the memory leak in agent.go") {
		t.Error("payload missing first user request")
	}
	if !strings.Contains(payload, "Also add tests for the fix") {
		t.Error("payload missing second user request")
	}
}

func TestBuildSummaryPayload_UserRequestsTruncated(t *testing.T) {
	longText := strings.Repeat("A", 600)
	msgs := []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: longText}}},
	}

	payload := buildSummaryPayload(msgs)

	if !strings.Contains(payload, "...") {
		t.Error("payload should contain truncation marker for long user message")
	}
}

// TestBuildSummaryPayload_ImageBlockMarker verifies that image content blocks
// are NOT silently dropped during summarization. Previously images were erased
// with no trace; now a textual marker is emitted so the summarizer (and the
// downstream agent) knows an image was part of the conversation.
func TestBuildSummaryPayload_ImageBlockMarker(t *testing.T) {
	msgs := []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{
			{Type: "text", Text: "Look at this screenshot"},
			provider.ImageBlock("image/png", strings.Repeat("A", 4096)), // ~3KB decoded
		}},
		{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "I see the error."}}},
	}

	payload := buildSummaryPayload(msgs)

	if !strings.Contains(payload, "IMAGE attached") {
		t.Error("payload should contain an IMAGE marker for image content blocks")
	}
	if !strings.Contains(payload, "image/png") {
		t.Error("payload should record the image MIME type")
	}
	if !strings.Contains(payload, "NOT preserved") {
		t.Error("payload should warn that visual content is not preserved")
	}
}

// TestBuildSummaryPayload_ToolResultImages verifies that images embedded in a
// tool_result (e.g. read_file returning an image) are surfaced as a marker.
func TestBuildSummaryPayload_ToolResultImages(t *testing.T) {
	msgs := []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{
			{Type: "tool_result", ToolID: "c1", ToolName: "read_file", Output: "binary data",
				Images: []provider.ContentImage{{MIME: "image/png", Base64: "AAAA"}}},
		}},
	}

	payload := buildSummaryPayload(msgs)

	if !strings.Contains(payload, "1 image") {
		t.Errorf("payload should note the tool result included images, got:\n%s", payload)
	}
}

// TestApproxImageKB verifies the cheap base64-length → KB estimate.
func TestApproxImageKB(t *testing.T) {
	if approxImageKB("") != 0 {
		t.Error("empty data should yield 0 KB")
	}
	if approxImageKB("short") != 0 {
		t.Error("short data should yield 0 KB")
	}
	// 4096 base64 chars → 3072 bytes → 3 KB
	if got := approxImageKB(strings.Repeat("A", 4096)); got != 3 {
		t.Errorf("expected 3 KB for 4096 base64 chars, got %d", got)
	}
}

// TestEstimateMessagesTokensCountsImages verifies images contribute to the
// budget-aware recent retention estimate (previously ignored).
func TestEstimateMessagesTokensCountsImages(t *testing.T) {
	textMsgs := []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "hello"}}},
	}
	textOnly := estimateMessagesTokens(textMsgs)

	imageMsgs := []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{
			{Type: "text", Text: "hello"},
			provider.ImageBlock("image/png", strings.Repeat("A", 100)),
		}},
	}
	withImage := estimateMessagesTokens(imageMsgs)

	if withImage <= textOnly {
		t.Errorf("image message should cost more tokens than text-only (text=%d, withImg=%d)", textOnly, withImage)
	}
	// Images add ~170 tokens (same heuristic as estimateTokens).
	if withImage-textOnly < 160 {
		t.Errorf("expected ~170 token image overhead, got %d", withImage-textOnly)
	}
}

func TestExtractUserRequests(t *testing.T) {
	msgs := []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "Hello"}}},
		{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "Hi there"}}},
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "  "}}}, // whitespace only
		{Role: "user", Content: []provider.ContentBlock{{Type: "tool_result", ToolID: "c1", Output: "result"}}},
	}

	requests := extractUserRequests(msgs, 500)

	if len(requests) != 1 {
		t.Fatalf("expected 1 user request, got %d", len(requests))
	}
	if requests[0] != "Hello" {
		t.Errorf("expected 'Hello', got %q", requests[0])
	}
}

func TestExtractUserRequests_Empty(t *testing.T) {
	msgs := []provider.Message{
		{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "Hello"}}},
		{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "System message"}}},
	}

	requests := extractUserRequests(msgs, 500)
	if len(requests) != 0 {
		t.Errorf("expected 0 user requests, got %d", len(requests))
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// --- CompactSupersededReads tests ---

func TestCompactSupersededReads_SingleRead(t *testing.T) {
	m := NewManager(100000)

	// Single read - nothing to compact.
	m.Add(provider.Message{
		Role: "assistant",
		Content: []provider.ContentBlock{{
			Type:     "tool_use",
			ToolID:   "read-1",
			ToolName: "read_file",
			Input:    json.RawMessage(`{"path":"/foo.go"}`),
		}},
	})
	m.Add(provider.Message{
		Role: "user",
		Content: []provider.ContentBlock{
			provider.ToolResultBlock("read-1", strings.Repeat("x", 500), false),
		},
	})

	freed := m.CompactSupersededReads()
	if freed != 0 {
		t.Errorf("expected 0 freed for single read, got %d", freed)
	}
}

func TestCompactSupersededReads_DuplicateRead(t *testing.T) {
	m := NewManager(100000)

	// Two reads of the same file - first should be compacted.
	m.Add(provider.Message{
		Role: "assistant",
		Content: []provider.ContentBlock{{
			Type:     "tool_use",
			ToolID:   "read-1",
			ToolName: "read_file",
			Input:    json.RawMessage(`{"path":"/foo.go"}`),
		}},
	})
	m.Add(provider.Message{
		Role: "user",
		Content: []provider.ContentBlock{
			provider.ToolResultBlock("read-1", strings.Repeat("a", 500), false),
		},
	})
	m.Add(provider.Message{
		Role: "assistant",
		Content: []provider.ContentBlock{{
			Type:     "tool_use",
			ToolID:   "read-2",
			ToolName: "read_file",
			Input:    json.RawMessage(`{"path":"/foo.go"}`),
		}},
	})
	m.Add(provider.Message{
		Role: "user",
		Content: []provider.ContentBlock{
			provider.ToolResultBlock("read-2", strings.Repeat("b", 500), false),
		},
	})

	freed := m.CompactSupersededReads()
	if freed <= 0 {
		t.Fatal("expected tokens freed for superseded read")
	}

	msgs := m.Messages()
	for _, msg := range msgs {
		for _, b := range msg.Content {
			if b.Type == "tool_result" && b.ToolID == "read-1" {
				if !strings.HasPrefix(b.Output, "[superseded:") {
					t.Errorf("expected [superseded: prefix for read-1, got: %s", b.Output[:min(50, len(b.Output))])
				}
			}
			if b.Type == "tool_result" && b.ToolID == "read-2" {
				if strings.HasPrefix(b.Output, "[superseded:") {
					t.Error("read-2 should NOT be compacted (latest read)")
				}
			}
		}
	}
}

func TestCompactSupersededReads_DifferentFiles(t *testing.T) {
	m := NewManager(100000)

	// Reads of different files - nothing to compact.
	m.Add(provider.Message{
		Role: "assistant",
		Content: []provider.ContentBlock{{
			Type:     "tool_use",
			ToolID:   "read-1",
			ToolName: "read_file",
			Input:    json.RawMessage(`{"path":"/foo.go"}`),
		}},
	})
	m.Add(provider.Message{
		Role: "user",
		Content: []provider.ContentBlock{
			provider.ToolResultBlock("read-1", strings.Repeat("a", 500), false),
		},
	})
	m.Add(provider.Message{
		Role: "assistant",
		Content: []provider.ContentBlock{{
			Type:     "tool_use",
			ToolID:   "read-2",
			ToolName: "read_file",
			Input:    json.RawMessage(`{"path":"/bar.go"}`),
		}},
	})
	m.Add(provider.Message{
		Role: "user",
		Content: []provider.ContentBlock{
			provider.ToolResultBlock("read-2", strings.Repeat("b", 500), false),
		},
	})

	freed := m.CompactSupersededReads()
	if freed != 0 {
		t.Errorf("expected 0 freed for different files, got %d", freed)
	}
}

func TestCompactSupersededReads_MultiFileRead(t *testing.T) {
	m := NewManager(100000)

	// multi_file_read reads multiple files. If one of those files was
	// previously read via read_file, the earlier read is superseded.
	m.Add(provider.Message{
		Role: "assistant",
		Content: []provider.ContentBlock{{
			Type:     "tool_use",
			ToolID:   "read-1",
			ToolName: "read_file",
			Input:    json.RawMessage(`{"path":"/shared.go"}`),
		}},
	})
	m.Add(provider.Message{
		Role: "user",
		Content: []provider.ContentBlock{
			provider.ToolResultBlock("read-1", strings.Repeat("a", 500), false),
		},
	})
	m.Add(provider.Message{
		Role: "assistant",
		Content: []provider.ContentBlock{{
			Type:     "tool_use",
			ToolID:   "read-2",
			ToolName: "multi_file_read",
			Input:    json.RawMessage(`{"files":[{"path":"/shared.go"},{"path":"/other.go"}]}`),
		}},
	})
	m.Add(provider.Message{
		Role: "user",
		Content: []provider.ContentBlock{
			provider.ToolResultBlock("read-2", strings.Repeat("b", 500), false),
		},
	})

	freed := m.CompactSupersededReads()
	if freed <= 0 {
		t.Fatal("expected tokens freed when multi_file_read supersedes read_file")
	}

	// Verify read-1 is compacted, read-2 is not.
	msgs := m.Messages()
	for _, msg := range msgs {
		for _, b := range msg.Content {
			if b.Type == "tool_result" && b.ToolID == "read-1" {
				if !strings.HasPrefix(b.Output, "[superseded:") {
					t.Errorf("expected [superseded: prefix for read-1")
				}
			}
		}
	}
}

// TestCompactSupersededReads_PartialMultiFileRead verifies that a multi_file_read
// result is NOT compacted when only one of its files is re-read later.
// Compacting it would lose content for the other files that were NOT re-read.
func TestCompactSupersededReads_PartialMultiFileRead(t *testing.T) {
	m := NewManager(100000)

	// multi_file_read reads files A, B, C
	m.Add(provider.Message{
		Role: "assistant",
		Content: []provider.ContentBlock{{
			Type:     "tool_use",
			ToolID:   "mfr-1",
			ToolName: "multi_file_read",
			Input:    json.RawMessage(`{"files":[{"path":"/a.go"},{"path":"/b.go"},{"path":"/c.go"}]}`),
		}},
	})
	m.Add(provider.Message{
		Role: "user",
		Content: []provider.ContentBlock{
			provider.ToolResultBlock("mfr-1", strings.Repeat("x", 500), false),
		},
	})

	// Later: read_file reads only file A
	m.Add(provider.Message{
		Role: "assistant",
		Content: []provider.ContentBlock{{
			Type:     "tool_use",
			ToolID:   "rf-1",
			ToolName: "read_file",
			Input:    json.RawMessage(`{"path":"/a.go"}`),
		}},
	})
	m.Add(provider.Message{
		Role: "user",
		Content: []provider.ContentBlock{
			provider.ToolResultBlock("rf-1", strings.Repeat("y", 500), false),
		},
	})

	freed := m.CompactSupersededReads()

	// mfr-1 should NOT be compacted - files B and C were not re-read.
	msgs := m.Messages()
	for _, msg := range msgs {
		for _, b := range msg.Content {
			if b.Type == "tool_result" && b.ToolID == "mfr-1" {
				if strings.HasPrefix(b.Output, "[superseded:") {
					t.Errorf("multi_file_read mfr-1 should NOT be compacted: files B and C were not re-read (output prefix=%q)", b.Output[:min(50, len(b.Output))])
				}
			}
		}
	}
	_ = freed // may or may not free tokens, but must not compact mfr-1
}

func TestCompactSupersededReads_Idempotent(t *testing.T) {
	m := NewManager(100000)

	// Two reads of same file.
	m.Add(provider.Message{
		Role: "assistant",
		Content: []provider.ContentBlock{{
			Type:     "tool_use",
			ToolID:   "read-1",
			ToolName: "read_file",
			Input:    json.RawMessage(`{"path":"/dup.go"}`),
		}},
	})
	m.Add(provider.Message{
		Role: "user",
		Content: []provider.ContentBlock{
			provider.ToolResultBlock("read-1", strings.Repeat("a", 500), false),
		},
	})
	m.Add(provider.Message{
		Role: "assistant",
		Content: []provider.ContentBlock{{
			Type:     "tool_use",
			ToolID:   "read-2",
			ToolName: "read_file",
			Input:    json.RawMessage(`{"path":"/dup.go"}`),
		}},
	})
	m.Add(provider.Message{
		Role: "user",
		Content: []provider.ContentBlock{
			provider.ToolResultBlock("read-2", strings.Repeat("b", 500), false),
		},
	})

	freed1 := m.CompactSupersededReads()
	if freed1 <= 0 {
		t.Fatal("expected first call to free tokens")
	}
	// Second call should be a no-op (already compacted).
	freed2 := m.CompactSupersededReads()
	if freed2 != 0 {
		t.Errorf("expected 0 freed on second call (idempotent), got %d", freed2)
	}
}

func TestCompactSupersededReads_SkipsSmallResults(t *testing.T) {
	m := NewManager(100000)

	// Two reads of same file, but results are small (<200 chars).
	m.Add(provider.Message{
		Role: "assistant",
		Content: []provider.ContentBlock{{
			Type:     "tool_use",
			ToolID:   "read-1",
			ToolName: "read_file",
			Input:    json.RawMessage(`{"path":"/small.go"}`),
		}},
	})
	m.Add(provider.Message{
		Role: "user",
		Content: []provider.ContentBlock{
			provider.ToolResultBlock("read-1", "small content", false),
		},
	})
	m.Add(provider.Message{
		Role: "assistant",
		Content: []provider.ContentBlock{{
			Type:     "tool_use",
			ToolID:   "read-2",
			ToolName: "read_file",
			Input:    json.RawMessage(`{"path":"/small.go"}`),
		}},
	})
	m.Add(provider.Message{
		Role: "user",
		Content: []provider.ContentBlock{
			provider.ToolResultBlock("read-2", "small content 2", false),
		},
	})

	freed := m.CompactSupersededReads()
	if freed != 0 {
		t.Errorf("expected 0 freed for small results, got %d", freed)
	}
}

func TestCompactSupersededReads_PathNormalization(t *testing.T) {
	m := NewManager(100000)

	// Read with "./" prefix and without - should be treated as same file.
	m.Add(provider.Message{
		Role: "assistant",
		Content: []provider.ContentBlock{{
			Type:     "tool_use",
			ToolID:   "read-1",
			ToolName: "read_file",
			Input:    json.RawMessage(`{"path":"./src/main.go"}`),
		}},
	})
	m.Add(provider.Message{
		Role: "user",
		Content: []provider.ContentBlock{
			provider.ToolResultBlock("read-1", strings.Repeat("a", 500), false),
		},
	})
	m.Add(provider.Message{
		Role: "assistant",
		Content: []provider.ContentBlock{{
			Type:     "tool_use",
			ToolID:   "read-2",
			ToolName: "read_file",
			Input:    json.RawMessage(`{"path":"src/main.go"}`),
		}},
	})
	m.Add(provider.Message{
		Role: "user",
		Content: []provider.ContentBlock{
			provider.ToolResultBlock("read-2", strings.Repeat("b", 500), false),
		},
	})

	freed := m.CompactSupersededReads()
	if freed <= 0 {
		t.Fatal("expected tokens freed - ./prefix normalization should match")
	}
}

func TestCompactSupersededReads_ThreeReadsSameFile(t *testing.T) {
	m := NewManager(100000)

	// Three reads of the same file - first two should be compacted.
	for i := 0; i < 3; i++ {
		toolID := fmt.Sprintf("read-%d", i+1)
		m.Add(provider.Message{
			Role: "assistant",
			Content: []provider.ContentBlock{{
				Type:     "tool_use",
				ToolID:   toolID,
				ToolName: "read_file",
				Input:    json.RawMessage(`{"path":"/triple.go"}`),
			}},
		})
		m.Add(provider.Message{
			Role: "user",
			Content: []provider.ContentBlock{
				provider.ToolResultBlock(toolID, strings.Repeat(string(rune('a'+i)), 500), false),
			},
		})
	}

	freed := m.CompactSupersededReads()
	if freed <= 0 {
		t.Fatal("expected tokens freed for 3 reads of same file")
	}

	// Verify read-3 is NOT compacted, read-1 and read-2 ARE.
	msgs := m.Messages()
	compactedCount := 0
	aliveCount := 0
	for _, msg := range msgs {
		for _, b := range msg.Content {
			if b.Type != "tool_result" || !strings.HasPrefix(b.Output, "[superseded:") {
				continue
			}
			compactedCount++
		}
	}
	// Count alive (non-superseded, non-cleared) read results for /triple.go
	for _, msg := range msgs {
		for _, b := range msg.Content {
			if b.Type != "tool_result" {
				continue
			}
			if !strings.HasPrefix(b.Output, "[superseded:") && !strings.HasPrefix(b.Output, "[cleared:") {
				aliveCount++
			}
		}
	}
	if compactedCount != 2 {
		t.Errorf("expected 2 compacted results, got %d", compactedCount)
	}
	if aliveCount != 1 {
		t.Errorf("expected 1 alive result (latest read), got %d", aliveCount)
	}
}

func TestNormalizeFilePath(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"/abs/path.go", "/abs/path.go"},
		{"./rel/path.go", "rel/path.go"},
		{"foo//bar.go", "foo/bar.go"},
		{"/foo/./bar.go", "/foo/./bar.go"}, // we only strip leading ./
		{"", ""},
	}
	// Case folding only applies on case-insensitive platforms (#195);
	// Linux paths are case-sensitive and keep their original case.
	wantUpper := "upper.case"
	if runtime.GOOS == "linux" {
		wantUpper = "UPPER.CASE"
	}
	tests = append(tests, struct {
		input    string
		expected string
	}{"UPPER.CASE", wantUpper})
	for _, tc := range tests {
		got := normalizeFilePath(tc.input)
		if got != tc.expected {
			t.Errorf("normalizeFilePath(%q) = %q, want %q", tc.input, got, tc.expected)
		}
	}
}

func TestExtractReadPaths(t *testing.T) {
	// read_file
	paths := extractReadPaths("read_file", json.RawMessage(`{"path":"/foo.go"}`))
	if len(paths) != 1 || paths[0] != "/foo.go" {
		t.Errorf("read_file: expected [/foo.go], got %v", paths)
	}

	// multi_file_read
	paths = extractReadPaths("multi_file_read", json.RawMessage(`{"files":[{"path":"/a.go"},{"path":"/b.go"}]}`))
	if len(paths) != 2 || paths[0] != "/a.go" || paths[1] != "/b.go" {
		t.Errorf("multi_file_read: expected [/a.go /b.go], got %v", paths)
	}

	// non-read tool
	paths = extractReadPaths("edit_file", json.RawMessage(`{"file_path":"/foo.go"}`))
	if len(paths) != 0 {
		t.Errorf("edit_file: expected [], got %v", paths)
	}

	// empty input
	paths = extractReadPaths("read_file", nil)
	if len(paths) != 0 {
		t.Errorf("empty input: expected [], got %v", paths)
	}
}

func TestContextManager_Summarize_RecentGroupBudgetExceeded(t *testing.T) {
	// Issue #324 fix: even when the most recent group exceeds the token
	// budget (15% of context window), the last group is still kept verbatim
	// so the current user request is never folded into the lossy summary.
	ctx := context.Background()
	prov := &mockProvider{}

	cm := NewManager(100) // maxRecentTokens = int(100*0.15) = 15
	cm.Add(provider.Message{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "System prompt."}}})
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "old question"}}})
	cm.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "old answer"}}})
	// Recent group is large enough to exceed 15-token budget
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: strings.Repeat("x", 200)}}})
	cm.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: strings.Repeat("y", 200)}}})

	if err := cm.Summarize(ctx, prov); err != nil {
		t.Fatalf("Summarize failed: %v", err)
	}

	msgs := cm.Messages()
	// Budget exceeded but last group guaranteed kept: [system, summary, user, assistant]
	if len(msgs) != 4 {
		t.Fatalf("expected system + summary + kept last group = 4 messages, got %d", len(msgs))
	}
	if !containsSummaryMarker(msgs[1]) {
		t.Fatal("expected summary marker")
	}
	if msgs[2].Role != "user" || !strings.Contains(msgs[2].Content[0].Text, strings.Repeat("x", 50)) {
		t.Fatal("expected last user message kept verbatim after summary")
	}
}

// addCommandRun appends an assistant tool_use + user tool_result pair for a
// run_command invocation with the given command/working dir.
func addCommandRun(m *Manager, toolID, command, workingDir, output string) {
	input := map[string]string{"command": command}
	if workingDir != "" {
		input["working_dir"] = workingDir
	}
	raw, _ := json.Marshal(input)
	m.Add(provider.Message{
		Role: "assistant",
		Content: []provider.ContentBlock{{
			Type:     "tool_use",
			ToolID:   toolID,
			ToolName: "run_command",
			Input:    json.RawMessage(raw),
		}},
	})
	m.Add(provider.Message{
		Role:    "user",
		Content: []provider.ContentBlock{provider.ToolResultBlock(toolID, output, false)},
	})
}

func TestCompactSupersededCommands_Rerun(t *testing.T) {
	m := NewManager(100000)

	// Same command run twice - first run's output should be compacted.
	addCommandRun(m, "cmd-1", "go build ./...", "", strings.Repeat("a", 500))
	addCommandRun(m, "cmd-2", "go build ./...", "", strings.Repeat("b", 500))

	freed := m.CompactSupersededCommands()
	if freed <= 0 {
		t.Fatal("expected tokens freed for superseded command run")
	}

	for _, msg := range m.Messages() {
		for _, b := range msg.Content {
			if b.Type != "tool_result" {
				continue
			}
			switch b.ToolID {
			case "cmd-1":
				if !strings.HasPrefix(b.Output, "[superseded:") {
					t.Errorf("expected [superseded: prefix for cmd-1, got: %s", b.Output[:min(50, len(b.Output))])
				}
			case "cmd-2":
				if strings.HasPrefix(b.Output, "[superseded:") {
					t.Error("cmd-2 (latest run) should NOT be compacted")
				}
			}
		}
	}
}

func TestCompactSupersededCommands_DifferentCommands(t *testing.T) {
	m := NewManager(100000)

	// Different commands / working dirs - none supersede each other.
	addCommandRun(m, "cmd-1", "go build ./...", "", strings.Repeat("a", 500))
	addCommandRun(m, "cmd-2", "go test ./...", "", strings.Repeat("b", 500))
	addCommandRun(m, "cmd-3", "go build ./...", "/other", strings.Repeat("c", 500))

	if freed := m.CompactSupersededCommands(); freed != 0 {
		t.Fatalf("expected 0 freed for distinct commands, got %d", freed)
	}
}

func TestCompactSupersededCommands_SkipsSmallResults(t *testing.T) {
	m := NewManager(100000)

	// Re-run but the superseded output is tiny - not worth compacting.
	addCommandRun(m, "cmd-1", "echo hi", "", "ok")
	addCommandRun(m, "cmd-2", "echo hi", "", "ok")

	if freed := m.CompactSupersededCommands(); freed != 0 {
		t.Fatalf("expected 0 freed for small results, got %d", freed)
	}
}

func TestCompactSupersededCommands_Idempotent(t *testing.T) {
	m := NewManager(100000)

	addCommandRun(m, "cmd-1", "go build ./...", "", strings.Repeat("a", 500))
	addCommandRun(m, "cmd-2", "go build ./...", "", strings.Repeat("b", 500))

	if first := m.CompactSupersededCommands(); first <= 0 {
		t.Fatalf("expected tokens freed on first pass, got %d", first)
	}
	if second := m.CompactSupersededCommands(); second != 0 {
		t.Fatalf("second pass should free nothing (idempotent), got %d", second)
	}
}

func TestCompactSupersededCommands_ThreeRunsSameCommand(t *testing.T) {
	m := NewManager(100000)

	addCommandRun(m, "cmd-1", "go test ./...", "", strings.Repeat("a", 500))
	addCommandRun(m, "cmd-2", "go test ./...", "", strings.Repeat("b", 500))
	addCommandRun(m, "cmd-3", "go test ./...", "", strings.Repeat("c", 500))

	if freed := m.CompactSupersededCommands(); freed <= 0 {
		t.Fatal("expected tokens freed")
	}
	compacted := 0
	for _, msg := range m.Messages() {
		for _, b := range msg.Content {
			if b.Type == "tool_result" && strings.HasPrefix(b.Output, "[superseded:") {
				compacted++
			}
		}
	}
	if compacted != 2 {
		t.Errorf("expected 2 superseded runs (all but latest), got %d", compacted)
	}
}

func TestCompactSupersededCommands_IgnoresOtherTools(t *testing.T) {
	m := NewManager(100000)

	// Non-run_command tools must never be treated as commands.
	m.Add(provider.Message{
		Role: "assistant",
		Content: []provider.ContentBlock{{
			Type:     "tool_use",
			ToolID:   "g-1",
			ToolName: "grep",
			Input:    json.RawMessage(`{"command":"go build ./...","pattern":"x"}`),
		}},
	})
	m.Add(provider.Message{
		Role:    "user",
		Content: []provider.ContentBlock{provider.ToolResultBlock("g-1", strings.Repeat("a", 500), false)},
	})

	if freed := m.CompactSupersededCommands(); freed != 0 {
		t.Fatalf("expected 0 freed for non-run_command tools, got %d", freed)
	}
}
