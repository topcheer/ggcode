package agent

import (
	"context"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

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

	if echoCount != 1 {
		t.Fatalf("expected echo tool to execute exactly once despite pending steering, got %d", echoCount)
	}

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
