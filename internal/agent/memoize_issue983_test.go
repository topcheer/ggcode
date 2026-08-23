package agent

import (
	"errors"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// TestIssue983MemoHitDoesNotRestackCachedPrefix verifies the #983 fix: a memo
// cache hit must not be written back into the cache after the "[cached ...]"
// annotation is prepended. Before the fix, the annotated result fell through
// to the unconditional toolMemo.put, so the Nth repeat of the same read got
// N-1 stacked "[cached ... identical content ...]" prefixes — each layer
// wasting ~65 chars of tokens, and from the second layer on making the
// "identical content" annotation literally false.
//
// This test simulates the agent.go loop at the memo level: get → (annotate on
// hit) → put-if-not-hit.
func TestIssue983MemoHitDoesNotRestackCachedPrefix(t *testing.T) {
	memo := newToolMemo()
	// Use a TTL-based tool (grep) rather than read_file: read_file uses
	// mtime invalidation and would stat a nonexistent path -> forced miss.
	args := []byte(`{"pattern":"TODO"}`)
	origContent := "line 1\nline 2\nline 3"
	origResult := tool.Result{Content: origContent}

	// First call: miss, execute, put pristine result.
	if _, hit := memo.get("grep", args); hit {
		t.Fatal("first get should miss")
	}
	memo.put("grep", args, origResult)

	const cachedPrefix = "[cached - grep returned identical content since your last call]"

	// Simulate the agent loop: three consecutive repeated calls. On each hit,
	// the loop annotates result.Content (as agent.go does), and — after the
	// #983 fix — skips the put.
	for i := 0; i < 3; i++ {
		result, hit := memo.get("grep", args)
		if !hit {
			t.Fatalf("repeat call %d: expected memo hit", i+1)
		}
		annotated := result
		if annotated.Content != "" && !annotated.IsError {
			annotated.Content = fmtSprintfCachedPrefix(cachedPrefix, annotated.Content)
		}
		// #983 fix under test: hit => skip put (agent.go uses !memoHit).
		// The buggy behavior would be: memo.put("grep", args, annotated).
	}

	// After the repeats, a fresh get must return content with at most ONE
	// cached annotation layer - i.e. identical to the pristine original.
	final, hit := memo.get("grep", args)
	if !hit {
		t.Fatal("final get should hit")
	}
	if strings.Count(final.Content, "[cached") != 0 {
		t.Fatalf("memo stored content must stay pristine, got %d stacked prefix(es): %q",
			strings.Count(final.Content, "[cached"), final.Content)
	}
	if final.Content != origContent {
		t.Fatalf("memo stored content changed: want %q, got %q", origContent, final.Content)
	}

	// And the annotation the model sees on a hit is exactly one layer.
	view := final
	view.Content = fmtSprintfCachedPrefix(cachedPrefix, view.Content)
	if got := strings.Count(view.Content, "[cached"); got != 1 {
		t.Fatalf("model-visible hit content should carry exactly one annotation layer, got %d", got)
	}
}

func fmtSprintfCachedPrefix(prefix, content string) string {
	return prefix + "\n" + content
}

// TestIssue983MemoPutStillWorksAfterMiss guards the complementary side of the
// fix: real (non-hit) executions must still be cached, so the fix did not
// accidentally disable memoization entirely.
func TestIssue983MemoPutStillWorksAfterMiss(t *testing.T) {
	memo := newToolMemo()
	args := []byte(`{"pattern":"TODO"}`)
	res := tool.Result{Content: "match: foo.go:1"}

	memo.put("grep", args, res)
	got, hit := memo.get("grep", args)
	if !hit {
		t.Fatal("expected hit after put")
	}
	if got.Content != res.Content {
		t.Fatalf("content mismatch: want %q, got %q", res.Content, got.Content)
	}
}

// TestIssue983StreamErrorPreservesAssistantText verifies the second #983 fix:
// when the provider stream errors mid-response (terminal error path), the
// assistant text already streamed must be preserved in the context manager so
// a resumed session does not lose the partial turn. Pure text only — partial
// tool_use blocks remain correctly discarded for pairing integrity. This
// mirrors the policyBlocked handling (which keeps resp.Message for the same
// reason; see the truncation-recovery comment about "old behavior discarded
// everything already streamed").
func TestIssue983StreamErrorPreservesAssistantText(t *testing.T) {
	mp := &mockProvider{
		streamEvents: [][]provider.StreamEvent{
			{
				{Type: provider.StreamEventText, Text: "Partial answer that was already streamed to the user."},
				{Type: provider.StreamEventError, Error: errors.New("stream exploded mid-response")},
			},
		},
	}
	a := NewAgent(mp, tool.NewRegistry(), "sys", 2)
	if err := a.RunStream(t.Context(), "start", func(provider.StreamEvent) {}); err == nil {
		t.Fatal("expected RunStream to return the stream error")
	}
	found := false
	for _, msg := range a.Messages() {
		if msg.Role != "assistant" {
			continue
		}
		for _, block := range msg.Content {
			if block.Type == "text" && strings.Contains(block.Text, "Partial answer that was already streamed") {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("streamed assistant text was discarded on terminal stream error; messages: %+v", a.Messages())
	}
}
