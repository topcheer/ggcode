package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

func TestRunStreamWatchdogCancelReportedAsFailure(t *testing.T) {
	watchdogErr := fmt.Errorf("stream idle-timeout: %w", context.Canceled)
	mp := &mockProvider{streamErr: watchdogErr}
	a := NewAgent(mp, tool.NewRegistry(), "", 5)
	defer a.Close()

	var gotErrEvent bool
	err := a.RunStream(context.Background(), "do something", func(event provider.StreamEvent) {
		if event.Type == provider.StreamEventError {
			gotErrEvent = true
			if event.Error == nil {
				t.Errorf("StreamEventError carried nil error; aborted run must surface a real error")
			}
		}
	})
	if err == nil {
		t.Fatalf("RunStream() = nil error; watchdog-cancelled stream must be reported as failure")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("RunStream() error = %v; want context.Canceled in chain", err)
	}
	if !gotErrEvent {
		t.Fatalf("no StreamEventError emitted for aborted stream")
	}
}

func TestRunStreamAllZeroUsageEmptyStillRetried(t *testing.T) {
	mp := &mockProvider{
		streamEvents: [][]provider.StreamEvent{
			{{Type: provider.StreamEventDone, Usage: &provider.TokenUsage{InputTokens: 0, OutputTokens: 0}}},
			{{Type: provider.StreamEventText, Text: "recovered"},
				{Type: provider.StreamEventDone, Usage: &provider.TokenUsage{InputTokens: 0, OutputTokens: 4}}},
		},
	}
	a := NewAgent(mp, tool.NewRegistry(), "", 5)
	defer a.Close()

	var texts []string
	err := a.RunStream(context.Background(), "say hi", func(event provider.StreamEvent) {
		if event.Type == provider.StreamEventText {
			texts = append(texts, event.Text)
		}
	})
	if err != nil {
		t.Fatalf("RunStream() error = %v", err)
	}
	if got := mp.streamCallCount(); got != 2 {
		t.Fatalf("expected 2 stream calls (empty retry then recovery), got %d", got)
	}
	if !strings.Contains(strings.Join(texts, "\n"), "recovered") {
		t.Fatalf("recovery text missing from output: %v", texts)
	}
}

func TestRunStreamAllZeroUsageThreeEmptiesAbort(t *testing.T) {
	empty := []provider.StreamEvent{{Type: provider.StreamEventDone, Usage: &provider.TokenUsage{InputTokens: 0, OutputTokens: 0}}}
	mp := &mockProvider{
		streamEvents: [][]provider.StreamEvent{empty, empty, empty},
	}
	a := NewAgent(mp, tool.NewRegistry(), "", 5)
	defer a.Close()

	err := a.RunStream(context.Background(), "say hi", func(event provider.StreamEvent) {})
	if err == nil {
		t.Fatalf("RunStream() = nil; 3 consecutive empty responses on all-zero-usage gateway must abort, not silently succeed")
	}
	if !strings.Contains(err.Error(), "consecutive empty") {
		t.Fatalf("RunStream() error = %v; want consecutive-empty abort message", err)
	}
}
