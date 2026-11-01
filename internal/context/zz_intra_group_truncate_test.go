package context

import (
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func zz4Msg(role, text string) provider.Message {
	return provider.Message{Role: role, Content: []provider.ContentBlock{{Type: "text", Text: text}}}
}

func zz4ToolResult(text string) provider.Message {
	return provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "tool_result", Text: text}}}
}

// Scenario 1: single group + system - the current whole-group code returns
// false here even though the group has an evictable tail.
func TestTruncateIntraGroup_SingleGroupEvictsTail(t *testing.T) {
	msgs := []provider.Message{
		zz4Msg("system", "sys"),
		zz4Msg("user", "prompt-A"),
		zz4Msg("assistant", "a1"),
		zz4ToolResult("r1"),
		zz4Msg("assistant", "a2"),
	}
	got, ok := truncateGroupsForPTLRetry(msgs)
	if !ok {
		t.Fatalf("single group with evictable tail: expected ok=true, got false")
	}
	if len(got) != len(msgs)-1 {
		t.Fatalf("expected exactly one message evicted, got len %d -> %d", len(msgs), len(got))
	}
	if got[1].Role != "user" || got[1].Content[0].Text != "prompt-A" {
		t.Fatalf("prompt head must survive, got role=%s", got[1].Role)
	}
}

// Scenario 2: two real-prompt groups - the oldest prompt head survives; only
// its tail is evicted (one-message granularity).
func TestTruncateIntraGroup_TwoGroupsKeepsOldestPromptHead(t *testing.T) {
	msgs := []provider.Message{
		zz4Msg("user", "prompt-A"),
		zz4Msg("assistant", "a1-old"),
		zz4Msg("user", "prompt-B"),
		zz4Msg("assistant", "a2-new"),
	}
	got, ok := truncateGroupsForPTLRetry(msgs)
	if !ok {
		t.Fatalf("expected ok=true")
	}
	if got[0].Content[0].Text != "prompt-A" {
		t.Fatalf("oldest prompt head evicted: first msg = %+v", got[0])
	}
	if len(got) != len(msgs)-1 {
		t.Fatalf("expected one-message granularity, got len %d -> %d", len(msgs), len(got))
	}
}

// Scenario 3: carrier tail (no prompt head) is evicted before any prompt.
func TestTruncateIntraGroup_CarrierGroupFullyEvictable(t *testing.T) {
	msgs := []provider.Message{
		zz4Msg("user", "prompt-A"),
		zz4ToolResult("r1"),
		zz4ToolResult("r2"),
		zz4Msg("user", "prompt-B"),
		zz4Msg("assistant", "a2"),
	}
	got, ok := truncateGroupsForPTLRetry(msgs)
	if !ok {
		t.Fatalf("expected ok=true")
	}
	// The prompt head stays first; the evicted message is the oldest
	// carrier in its tail (r1).
	if got[0].Content[0].Type != "text" || got[0].Content[0].Text != "prompt-A" {
		t.Fatalf("prompt head must stay first, got %+v", got[0])
	}
	if len(got) != len(msgs)-1 {
		t.Fatalf("expected one-message granularity, got len %d -> %d", len(msgs), len(got))
	}
	sawR1 := false
	for _, m := range got {
		for _, b := range m.Content {
			if b.Text == "r1" {
				sawR1 = true
			}
		}
	}
	if sawR1 {
		t.Fatal("the oldest carrier tail message (r1) must be the eviction target")
	}
}

// Scenario 4: nothing evictable -> false (termination guarantee).
func TestTruncateIntraGroup_AllPinnedReturnsFalse(t *testing.T) {
	if _, ok := truncateGroupsForPTLRetry([]provider.Message{zz4Msg("user", "prompt")}); ok {
		t.Fatalf("nothing evictable: expected false")
	}
	if _, ok := truncateGroupsForPTLRetry([]provider.Message{zz4Msg("system", "s"), zz4Msg("user", "prompt")}); ok {
		t.Fatalf("system+prompt only: expected false")
	}
	if _, ok := truncateGroupsForPTLRetry(nil); ok {
		t.Fatalf("nil: expected false")
	}
}
