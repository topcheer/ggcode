package agent

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

func TestIssue983MemoHitDoesNotRestackCachedPrefix(t *testing.T) {
	memo := newToolMemo()

	args := []byte(`{"pattern":"TODO"}`)
	origContent := "line 1\nline 2\nline 3"
	origResult := tool.Result{Content: origContent}

	if _, hit := memo.get("grep", args); hit {
		t.Fatal("first get should miss")
	}
	memo.put("grep", args, origResult)

	const cachedPrefix = "[cached - grep returned identical content since your last call]"

	for i := 0; i < 3; i++ {
		result, hit := memo.get("grep", args)
		if !hit {
			t.Fatalf("repeat call %d: expected memo hit", i+1)
		}
		annotated := result
		if annotated.Content != "" && !annotated.IsError {
			annotated.Content = fmtSprintfCachedPrefix(cachedPrefix, annotated.Content)
		}

	}

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

	view := final
	view.Content = fmtSprintfCachedPrefix(cachedPrefix, view.Content)
	if got := strings.Count(view.Content, "[cached"); got != 1 {
		t.Fatalf("model-visible hit content should carry exactly one annotation layer, got %d", got)
	}
}

func fmtSprintfCachedPrefix(prefix, content string) string {
	return prefix + "\n" + content
}

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

func TestIssue983AgentLoopKeepsMemoPristine(t *testing.T) {
	registry := tool.NewRegistry()
	if err := registry.Register(mockTool{name: "grep", result: tool.Result{Content: "match: unique-marker-983"}}); err != nil {
		t.Fatalf("register mock tool: %v", err)
	}
	args := json.RawMessage(`{"pattern":"unique-marker-983"}`)
	mp := &mockProvider{
		streamEvents: [][]provider.StreamEvent{
			{{Type: provider.StreamEventToolCallDone, Tool: provider.ToolCallDelta{ID: "tc1", Name: "grep", Arguments: args}}},
			{{Type: provider.StreamEventToolCallDone, Tool: provider.ToolCallDelta{ID: "tc2", Name: "grep", Arguments: args}}},
			{{Type: provider.StreamEventText, Text: "done"}},
		},
	}
	a := NewAgent(mp, registry, "sys", 10)
	if err := a.RunStream(t.Context(), "start", func(provider.StreamEvent) {}); err != nil {
		t.Fatalf("RunStream failed: %v", err)
	}

	final, hit := a.toolMemo.get("grep", []byte(args))
	if !hit {
		t.Fatal("expected memo entry for the double grep call")
	}
	if strings.Count(final.Content, "[cached") != 0 {
		t.Fatalf("agent loop wrote the annotated copy back into toolMemo (L3117 !memoHit regressed): %q", final.Content)
	}
	if !strings.Contains(final.Content, "unique-marker-983") {
		t.Fatalf("memo content lost the original result: %q", final.Content)
	}
}
