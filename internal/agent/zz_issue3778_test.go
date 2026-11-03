//go:build goolm

package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

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
	a := NewAgent(mp, registry, "", 1)
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
	a := NewAgent(mp, registry, "", 1)

	err := a.RunStream(context.Background(), "do work", func(ev provider.StreamEvent) {})
	if err == nil {
		t.Fatal("expected max-iterations error from tool-loop exhaustion")
	}
	if errors.Is(err, ErrGuidanceAtBudget) {
		t.Fatalf("plain exhaustion must not map to guidance-at-budget, got %v", err)
	}
}
