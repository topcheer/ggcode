package agent

// r213 pin tests (track B behavior-preserving split of RunStreamWithContent,
// round 2: main-loop body seams). These lock the observable behavior of the
// four body seams BEFORE extraction, so the refactor validates against a
// green golden baseline:
//   - handlePostResponsePhase: empty-response nudge + continue (capturedMsgs)
//   - classifyStreamResponseError: terminal cancellation classification
//   - finishToolTurnPhase: tool_result delivery into the next request
// The r212 pin tests (prologue/epilogue) continue to cover the advisories
// seam indirectly: every iteration runs runPerIterationAdvisories, and
// TestPinRunStreamMaxIterExhaustion drives two full iterations.

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// TestPinR213EmptyResponseNudgeSequence locks the empty-response recovery
// path: a first stream with input tokens but zero output tokens and no tool
// calls must inject the exact nudge text into the context and continue; the
// follow-up request (capturedMsgs[1]) must carry that nudge, and the run
// must complete with the second response's text relayed.
func TestPinR213EmptyResponseNudgeSequence(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	mp := &mockProvider{
		chatResponses: []*provider.ChatResponse{
			{Usage: provider.TokenUsage{InputTokens: 10}}, // empty response
			{Message: provider.Message{
				Role:    "assistant",
				Content: []provider.ContentBlock{provider.TextBlock("recovered")},
			}},
		},
	}
	a := NewAgent(mp, tool.NewRegistry(), "", 2)

	var texts []string
	err := a.RunStreamWithContent(context.Background(),
		[]provider.ContentBlock{{Type: "text", Text: "pin prompt"}},
		func(ev provider.StreamEvent) {
			if ev.Type == provider.StreamEventText {
				texts = append(texts, ev.Text)
			}
		})
	if err != nil {
		t.Fatalf("expected nil error after empty-response recovery, got %v", err)
	}
	joined := ""
	for _, s := range texts {
		joined += s
	}
	if !strings.Contains(joined, "recovered") {
		t.Fatalf("expected recovered text relayed, got %q", texts)
	}
	mp.mu.Lock()
	defer mp.mu.Unlock()
	if len(mp.capturedMsgs) != 2 {
		t.Fatalf("expected 2 captured requests, got %d", len(mp.capturedMsgs))
	}
	nudge := "The previous response was empty. Please try again."
	found := false
	for _, msg := range mp.capturedMsgs[1] {
		for _, b := range msg.Content {
			if b.Type == "text" && b.Text == nudge {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("empty-response nudge missing from follow-up request: %+v", mp.capturedMsgs[1])
	}
}

// TestPinR213CanceledStreamError locks the terminal cancellation branch of
// the stream-error classifier with a NON-canceled parent context: a wrapped
// context.Canceled from the stream must NOT be retried; the branch returns
// ctx.Err() which is nil here (quirky but existing behavior, pinned as-is),
// and exactly one error event relaying that nil error is emitted.
func TestPinR213CanceledStreamError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	mp := &mockProvider{
		streamErr: fmt.Errorf("stream torn down: %w", context.Canceled),
	}
	a := NewAgent(mp, tool.NewRegistry(), "", 1)

	var errEvents int
	var lastErr error
	err := a.RunStreamWithContent(context.Background(),
		[]provider.ContentBlock{{Type: "text", Text: "pin prompt"}},
		func(ev provider.StreamEvent) {
			if ev.Type == provider.StreamEventError {
				errEvents++
				lastErr = ev.Error
			}
		})
	if err != nil {
		t.Fatalf("expected nil error (ctx.Err() quirk with live parent ctx), got %v", err)
	}
	if errEvents != 1 {
		t.Fatalf("expected exactly 1 error event, got %d", errEvents)
	}
	if lastErr != nil {
		t.Fatalf("expected error event to carry nil ctx.Err(), got %v", lastErr)
	}
}

// TestPinR213ToolResultDelivery locks the finish-tool-turn seam: after a tool
// call turn, the next request must contain the executed tool's result as a
// user-role tool_result delivery (tool_use/tool_result pairing).
func TestPinR213ToolResultDelivery(t *testing.T) {
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

	var errEvents int
	_ = a.RunStreamWithContent(context.Background(),
		[]provider.ContentBlock{{Type: "text", Text: "pin prompt"}},
		func(ev provider.StreamEvent) {
			if ev.Type == provider.StreamEventError {
				errEvents++
			}
		})
	// sticky chatResp → one tool call per iteration; maxIter=2 → 2 executions
	if countCount != 2 {
		t.Fatalf("expected tool executed once per iteration (2), got %d", countCount)
	}
	mp.mu.Lock()
	defer mp.mu.Unlock()
	if len(mp.capturedMsgs) < 2 {
		t.Fatalf("expected follow-up request after tool turn, got %d", len(mp.capturedMsgs))
	}
	found := false
	for _, msg := range mp.capturedMsgs[1] {
		if msg.Role != "user" {
			continue
		}
		for _, b := range msg.Content {
			if b.Type == "tool_result" {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("tool_result delivery missing from follow-up request")
	}
	_ = errEvents
}
