package agent

import (
	"context"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// TestRunStreamSteeringNeverStrandsToolUsePairing guards the protocol
// invariant investigated in #3289: user steering (mid-run interruption) must
// NEVER be injected into the context between an assistant tool_use message
// and its tool_result blocks, or the next ChatStream payload violates the
// tool_use/tool_result pairing protocol and the API rejects it (Anthropic
// 400). The investigation concluded the current wiring is safe: the
// post-stream injection site sits inside the len(toolCalls)==0 branch, so a
// tool-call turn always executes and pairs its calls first. This test pins
// that behavior so a future refactor cannot silently move the injection
// point into the tool-execution window.
func TestRunStreamSteeringNeverStrandsToolUsePairing(t *testing.T) {
	mp := &mockProvider{
		chatResponses: []*provider.ChatResponse{
			{
				Message: provider.Message{
					Role: "assistant",
					Content: []provider.ContentBlock{
						provider.ToolUseBlock("call_1", "echo", []byte(`{}`)),
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
	var echoCount int
	if err := registry.Register(countingTool{name: "echo", executed: &echoCount}); err != nil {
		t.Fatalf("register echo tool: %v", err)
	}

	a := NewAgent(mp, registry, "", 3)
	// Steering arrives MID-STREAM: the first check (loop-top, before the
	// model responds) misses it; the second check sees it. The interruption
	// queue drains once.
	interruptCalls := 0
	a.SetInterruptionHandler(func() []provider.ContentBlock {
		interruptCalls++
		if interruptCalls == 2 {
			return []provider.ContentBlock{{Type: "text", Text: "change of plans"}}
		}
		return nil
	})

	if err := a.RunStream(context.Background(), "start", func(event provider.StreamEvent) {}); err != nil {
		t.Fatalf("RunStream failed: %v", err)
	}
	// Invariant 1: steering must not preempt a pending tool call - the
	// tool-call turn executes and pairs its calls before steering lands.
	if echoCount != 1 {
		t.Fatalf("expected echo tool to execute exactly once despite pending steering, got %d", echoCount)
	}
	// Invariant 2: the follow-up ChatStream payload must pair every tool_use
	// with a tool_result.
	msgs := mp.capturedMsgs[len(mp.capturedMsgs)-1]
	paired := false
	for _, m := range msgs {
		if m.Role != "user" {
			continue
		}
		for _, b := range m.Content {
			if b.Type == "tool_result" && b.ToolID == "call_1" {
				paired = true
			}
		}
	}
	if !paired {
		t.Fatalf("tool_use call_1 has no matching tool_result in follow-up ChatStream payload: %+v", msgs)
	}
}
