package agent

// Issue #611: session-timeout break must not fall into the shared post-loop
// block that reports "max iterations (N) reached" (when maxIter>0) or returns
// nil (when maxIter<=0, autopilot/cron semantics). The timeout exit must return
// the ErrSessionTimeout sentinel so callers can distinguish the terminal state.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// issue611Timeout is tiny enough that the first loop-top check always sees the
// deadline exceeded (deterministic: start() runs well before the first check).
const issue611Timeout = 1 * time.Nanosecond

// TestIssue611TimeoutWithMaxIter asserts that when a session timeout fires
// while maxIter > 0, RunStream returns ErrSessionTimeout instead of the
// misleading "max iterations (N) reached" error (probe: maxIter=100 +
// timeout=1ns actually iterated once but reported 100 iterations reached).
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

	// No contradictory "maximum iterations" UI message may be emitted either.
	for _, ev := range events {
		if ev.Type == provider.StreamEventText && strings.Contains(ev.Text, "maximum iterations") {
			t.Fatalf("unexpected max-iterations message alongside timeout stop: %q", ev.Text)
		}
		if ev.Type == provider.StreamEventError && !errors.Is(ev.Error, ErrSessionTimeout) {
			t.Fatalf("unexpected error event: %v", ev.Error)
		}
	}
}

// TestIssue611TimeoutUnlimitedIterations asserts that with maxIter<=0
// (autopilot/cron semantics), a timeout-killed run returns a non-nil error
// instead of masquerading as normal completion (previously: return nil).
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

	a := NewAgent(mp, registry, "", 0) // 0 = unlimited iterations
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

// TestIssue611NoTimeoutStillReportsMaxIter guards the fix against regressions
// in the opposite direction: without a timeout, exhausting maxIter must still
// report "max iterations (N) reached" (not ErrSessionTimeout).
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
	// No session timeout configured (disabled, interactive default).

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

// newTestRegistry builds a minimal tool registry for the timeout tests. No
// tools are needed: the 1ns timeout breaks the loop before any LLM call.
func newTestRegistry(t *testing.T) *tool.Registry {
	t.Helper()
	return tool.NewRegistry()
}
