package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

const issue611Timeout = 1 * time.Nanosecond

func TestIssue611TimeoutWithMaxIter(t *testing.T) {
	mp := &mockProvider{
		chatResp: &provider.ChatResponse{
			Message: provider.Message{
				Role:    "assistant",
				Content: []provider.ContentBlock{provider.TextBlock("working")},
			},
		},
	}
	registry := newTestRegistry(t)

	a := NewAgent(mp, registry, "", 100)
	a.SetSessionTimeout(issue611Timeout)

	var events []provider.StreamEvent
	err := a.RunStream(context.Background(), "do work", func(ev provider.StreamEvent) {
		events = append(events, ev)
	})

	if err == nil {
		t.Fatal("expected non-nil error from timeout-killed run")
	}
	if !errors.Is(err, ErrSessionTimeout) {
		t.Fatalf("expected ErrSessionTimeout, got %v", err)
	}
	if strings.Contains(err.Error(), "max iterations") {
		t.Fatalf("timeout exit must not report max iterations, got %q", err.Error())
	}

	for _, ev := range events {
		if ev.Type == provider.StreamEventText && strings.Contains(ev.Text, "maximum iterations") {
			t.Fatalf("unexpected max-iterations message alongside timeout stop: %q", ev.Text)
		}
		if ev.Type == provider.StreamEventError && !errors.Is(ev.Error, ErrSessionTimeout) {
			t.Fatalf("unexpected error event: %v", ev.Error)
		}
	}
}

func TestIssue611TimeoutUnlimitedIterations(t *testing.T) {
	mp := &mockProvider{
		chatResp: &provider.ChatResponse{
			Message: provider.Message{
				Role:    "assistant",
				Content: []provider.ContentBlock{provider.TextBlock("working")},
			},
		},
	}
	registry := newTestRegistry(t)

	a := NewAgent(mp, registry, "", 0)
	a.SetSessionTimeout(issue611Timeout)

	done := make(chan error, 1)
	go func() {
		done <- a.RunStream(context.Background(), "do work", func(provider.StreamEvent) {})
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected non-nil error from timeout-killed unlimited run")
		}
		if !errors.Is(err, ErrSessionTimeout) {
			t.Fatalf("expected ErrSessionTimeout, got %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("run did not terminate after session timeout")
	}
}

func TestIssue611NoTimeoutStillReportsMaxIter(t *testing.T) {
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

	a := NewAgent(mp, registry, "", 2)

	err := a.RunStream(context.Background(), "do work", func(provider.StreamEvent) {})
	if err == nil {
		t.Fatal("expected max-iterations error")
	}
	if errors.Is(err, ErrSessionTimeout) {
		t.Fatalf("max-iterations exhaustion must not report ErrSessionTimeout, got %v", err)
	}
	if !strings.Contains(err.Error(), "max iterations") {
		t.Fatalf("expected max iterations error, got %v", err)
	}
}

func newTestRegistry(t *testing.T) *tool.Registry {
	t.Helper()
	return tool.NewRegistry()
}
