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

// #4232 case 1: the provider stream layer cancels a per-request child
// context on idle-timeout while the main run ctx stays alive. The terminal
// stream-error path must NOT report the aborted run as success: the old code
// emitted an Error event carrying a nil error and returned ctx.Err()==nil.
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

// #4232 case 2: gateways that report all-zero usage (older vLLM / LiteLLM)
// previously bypassed the empty-response guard entirely because of the
// InputTokens > 0 conjunct - a single empty reply exited as silent success.
// With the conjunct removed, a genuinely empty reply must still trigger the
// recovery nudge (second stream call).
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

// #4232 case 2 (abort leg): three consecutive genuinely-empty responses on
// an all-zero-usage gateway must abort with a distinguishable error, not
// exit silently as success after the first one (old behavior).
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
