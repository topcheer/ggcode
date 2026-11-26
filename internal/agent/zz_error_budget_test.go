package agent

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// The SLO error budget is a statistic, not a gate: a run where nearly every
// tool call fails must never receive "[error-budget]" steering (fast-burn
// warning or "BUDGET EXHAUSTED ... ask the user") and must never be stopped
// by it. The model decides when to stop retrying.
func TestErrorBudgetNeverSteersOrStops(t *testing.T) {
	e := newErrorBudget()
	for i := 0; i < 60; i++ {
		if msg := e.evaluate(false); msg != "" {
			t.Fatalf("failed call %d produced steering text: %q", i, msg)
		}
	}
	if rate := e.burnRate(); rate < 0.99 {
		t.Fatalf("burn rate must still reflect the failures (statistic kept), got %.2f", rate)
	}
	e.reset()
	if e.burnRate() != 0 {
		t.Fatalf("reset must clear the window, got %.2f", e.burnRate())
	}
}

// alwaysFailTool fails every call; the provider keeps issuing one call per turn.
type alwaysFailTool struct{ calls int32 }

func (f *alwaysFailTool) Name() string { return "always_fail" }
func (f *alwaysFailTool) Description() string {
	return "Always fails. Used to prove the error budget no longer steers."
}
func (f *alwaysFailTool) Parameters() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (f *alwaysFailTool) Execute(_ context.Context, _ json.RawMessage) (tool.Result, error) {
	atomic.AddInt32(&f.calls, 1)
	return tool.Result{IsError: true, Content: "simulated failure"}, nil
}

// errorStormProvider emits one always_fail tool call per turn for the first
// `calls` turns, then a plain text turn (the model's own end of run).
type errorStormProvider struct {
	mockProvider
	calls int32
	turns int32
}

func (p *errorStormProvider) ChatStream(ctx context.Context, messages []provider.Message, tools []provider.ToolDefinition) (<-chan provider.StreamEvent, error) {
	n := int(atomic.AddInt32(&p.turns, 1))
	var resp provider.ChatResponse
	if int32(n) <= atomic.LoadInt32(&p.calls) {
		resp = provider.ChatResponse{Message: provider.Message{Role: "assistant", Content: []provider.ContentBlock{
			provider.ToolUseBlock("c1", "always_fail", json.RawMessage(`{}`)),
		}}}
	} else {
		resp = provider.ChatResponse{Message: provider.Message{Role: "assistant", Content: []provider.ContentBlock{
			provider.TextBlock("I am done trying."),
		}}}
	}
	resp.Usage = provider.TokenUsage{InputTokens: 5, OutputTokens: 1}
	events := streamEventsFromResponse(&resp)
	ch := make(chan provider.StreamEvent, len(events))
	for _, e := range events {
		ch <- e
	}
	close(ch)
	return ch, nil
}

// TestErrorStormRunIsNeverSteeredByBudget drives a real run with 25
// consecutive failing tool calls: the old breaker fired after ~10 and told
// the model to stop and ask the user; now nothing is injected and the run
// ends on the model's own final text turn.
func TestErrorStormRunIsNeverSteeredByBudget(t *testing.T) {
	ft := &alwaysFailTool{}
	registry := tool.NewRegistry()
	if err := registry.Register(ft); err != nil {
		t.Fatalf("register: %v", err)
	}
	pp := &errorStormProvider{calls: 25}
	a := NewAgent(pp, registry, "", 0)
	var sys []string
	if err := a.RunStream(context.Background(), "keep trying", func(ev provider.StreamEvent) {
		if ev.Type == provider.StreamEventSystem {
			sys = append(sys, ev.Text)
		}
	}); err != nil {
		t.Fatalf("run error = %v", err)
	}
	if got := atomic.LoadInt32(&ft.calls); got != 25 {
		t.Fatalf("tool calls = %d, want 25 (the run must not be cut short)", got)
	}
	for i, m := range a.contextManager.Messages() {
		for _, b := range m.Content {
			if b.Type == "text" && strings.Contains(b.Text, "error-budget") {
				t.Fatalf("message %d carries error-budget steering: %q", i, b.Text)
			}
		}
	}
	joined := strings.Join(sys, "\n")
	if strings.Contains(joined, "error-budget") || strings.Contains(joined, "BUDGET EXHAUSTED") {
		t.Fatalf("system events carry budget steering:\n%s", joined)
	}
	if a.errorBudget == nil || a.errorBudget.burnRate() < 0.99 {
		t.Fatalf("burn rate statistic must still be tracked, got %v", a.errorBudget)
	}
}
