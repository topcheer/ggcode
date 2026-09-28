package agent

// r212 pin tests (track B behavior-preserving split of RunStreamWithContent).
// These lock the observable behavior of the prologue seam chain (detector
// resets, workspace pre-run checks, experience-recall seam site) and the
// epilogue summary seams (max-iterations path) BEFORE the extraction, so the
// refactor can be validated against a green golden baseline.

import (
	"context"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// TestPinRunStreamHappyPath locks the observable event relay and nil error of
// a single-iteration text-only run: exactly one text event with the assistant
// payload must be forwarded to onEvent, exercising the full prologue in order.
func TestPinRunStreamHappyPath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	mp := &mockProvider{
		chatResp: &provider.ChatResponse{
			Message: provider.Message{
				Role:    "assistant",
				Content: []provider.ContentBlock{provider.TextBlock("pin-reply")},
			},
		},
	}
	a := NewAgent(mp, tool.NewRegistry(), "", 1)

	var texts []string
	err := a.RunStreamWithContent(context.Background(),
		[]provider.ContentBlock{{Type: "text", Text: "pin prompt"}},
		func(ev provider.StreamEvent) {
			if ev.Type == provider.StreamEventText {
				texts = append(texts, ev.Text)
			}
		})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if len(texts) != 1 || texts[0] != "pin-reply" {
		t.Fatalf("unexpected text events: %q", texts)
	}
}

// TestPinRunStreamMaxIterExhaustion locks the max-iterations epilogue seam:
// the exact sentinel error string "max iterations (N) reached" and exactly one
// "maximum iterations" summary text event must be emitted.
func TestPinRunStreamMaxIterExhaustion(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	mp := &mockProvider{
		chatResp: &provider.ChatResponse{
			Message: provider.Message{
				Role: "assistant",
				Content: []provider.ContentBlock{
					provider.ToolUseBlock("call_1", "count", []byte(`{}`)),
				},
			},
		},
	}
	registry := tool.NewRegistry()
	var countCount int
	if err := registry.Register(countingTool{name: "count", executed: &countCount}); err != nil {
		t.Fatalf("register countingTool: %v", err)
	}
	a := NewAgent(mp, registry, "", 2)

	var maxIterTexts []string
	err := a.RunStreamWithContent(context.Background(),
		[]provider.ContentBlock{{Type: "text", Text: "pin prompt"}},
		func(ev provider.StreamEvent) {
			if ev.Type == provider.StreamEventText && strings.Contains(ev.Text, "maximum iterations") {
				maxIterTexts = append(maxIterTexts, ev.Text)
			}
		})
	if err == nil || err.Error() != "max iterations (2) reached" {
		t.Fatalf("expected exact max-iterations error, got %v", err)
	}
	if len(maxIterTexts) != 1 {
		t.Fatalf("expected exactly one max-iterations summary text event, got %d", len(maxIterTexts))
	}
}

// TestPinConcatTextBlocks locks the pure text-block concatenation used for
// stats-prompt extraction and user-message hooks (order-sensitive joining).
func TestPinConcatTextBlocks(t *testing.T) {
	got := concatTextBlocks([]provider.ContentBlock{
		{Type: "text", Text: "a"},
		{Type: "image", Text: "ignored"},
		{Type: "text", Text: "b"},
	})
	if got != "ab" {
		t.Fatalf("expected %q, got %q", "ab", got)
	}
	if concatTextBlocks(nil) != "" {
		t.Fatal("expected empty string for nil content")
	}
}
