//go:build goolm

package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

// #3778: mid-run guidance injected on the FINAL iteration is persisted in
// context but can never reach an LLM call this run. The post-loop block
// used to fall into the generic "max iterations (N) reached" sentinel -
// polluting the errors.Is terminal-state classification contract that
// sub-agents/ACP loops/cron rely on. It must return the dedicated
// ErrGuidanceAtBudget sentinel instead.
func TestIssue3778_GuidanceOnFinalIterationReturnsDedicatedSentinel(t *testing.T) {
	mp := &mockProvider{
		chatResp: &provider.ChatResponse{
			Message: provider.Message{
				Role:    "assistant",
				Content: []provider.ContentBlock{provider.TextBlock("working")},
			},
		},
	}
	registry := newTestRegistry(t)
	a := NewAgent(mp, registry, "", 1) // maxIter=1: the only iteration IS the final one
	guidance := false
	a.SetInterruptionHandler(func() []provider.ContentBlock {
		if guidance {
			return nil
		}
		guidance = true
		return []provider.ContentBlock{{Type: "text", Text: "please also run the linter"}}
	})

	var events []provider.StreamEvent
	err := a.RunStream(context.Background(), "do work", func(ev provider.StreamEvent) {
		events = append(events, ev)
	})

	if !errors.Is(err, ErrGuidanceAtBudget) {
		t.Fatalf("expected ErrGuidanceAtBudget, got %v", err)
	}
	for _, ev := range events {
		if ev.Type == provider.StreamEventText && strings.Contains(ev.Text, "max iterations") {
			t.Fatalf("generic max-iterations message must not fire for guidance-at-budget, got %q", ev.Text)
		}
	}
}

// Without guidance on the final iteration, the classic max-iterations
// sentinel path is unchanged.
func TestIssue3778_NoGuidanceKeepsClassicSentinel(t *testing.T) {
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
	registry := newTestRegistry(t)
	var countCount int
	if err := registry.Register(countingTool{name: "count", executed: &countCount}); err != nil {
		t.Fatalf("register countingTool: %v", err)
	}
	a := NewAgent(mp, registry, "", 1) // tool-loop exhaustion, no interruption handler

	err := a.RunStream(context.Background(), "do work", func(ev provider.StreamEvent) {})
	if err == nil {
		t.Fatal("expected max-iterations error from tool-loop exhaustion")
	}
	if errors.Is(err, ErrGuidanceAtBudget) {
		t.Fatalf("plain exhaustion must not map to guidance-at-budget, got %v", err)
	}
}
