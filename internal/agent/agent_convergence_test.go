package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// countConvergenceMessages scans context messages for convergence pressure text.
func countConvergenceMessages(msgs []provider.Message, needle string) int {
	count := 0
	for _, msg := range msgs {
		for _, c := range msg.Content {
			if c.Type == "text" && strings.Contains(c.Text, needle) {
				count++
			}
		}
	}
	return count
}

// makeConvergenceAgent creates an agent that always returns a tool_use,
// so it loops until maxIter is reached. The agent then returns an error.
func makeConvergenceAgent(maxIter int) *Agent {
	mp := &mockProvider{
		chatResp: &provider.ChatResponse{
			Message: provider.Message{
				Role: "assistant",
				Content: []provider.ContentBlock{
					{Type: "text", Text: "working"},
					{Type: "tool_use", ToolID: "t1", ToolName: "mock", Input: json.RawMessage(`{}`)},
				},
			},
			Usage: provider.TokenUsage{InputTokens: 100, OutputTokens: 10},
		},
	}
	registry := tool.NewRegistry()
	_ = registry.Register(mockTool{name: "mock", result: tool.Result{Content: "ok"}})
	return NewAgent(mp, registry, "Be helpful", maxIter)
}

// TestConvergencePressureAt85Percent verifies that the convergence pressure
// message is injected at 85% of maxIter, guiding the agent to finalize.
func TestConvergencePressureAt85Percent(t *testing.T) {
	a := makeConvergenceAgent(20)
	_ = a.RunStream(context.Background(), "do something", func(provider.StreamEvent) {})
	msgs := a.contextManager.Messages()
	if c := countConvergenceMessages(msgs, "Shift to convergence"); c != 1 {
		t.Errorf("convergence pressure (85%%) should fire exactly once, got %d", c)
	}
}

// TestConvergencePressureAt95Percent verifies the final wrap-up message fires at 95%.
func TestConvergencePressureAt95Percent(t *testing.T) {
	a := makeConvergenceAgent(20)
	_ = a.RunStream(context.Background(), "do something", func(provider.StreamEvent) {})
	msgs := a.contextManager.Messages()
	if c := countConvergenceMessages(msgs, "You MUST produce your final response"); c != 1 {
		t.Errorf("convergence pressure (95%%) should fire exactly once, got %d", c)
	}
}

// TestConvergencePressureNotTriggeredForShortRuns verifies that runs with
// maxIter < 20 do not get convergence pressure (too short to be useful).
func TestConvergencePressureNotTriggeredForShortRuns(t *testing.T) {
	a := makeConvergenceAgent(10)
	_ = a.RunStream(context.Background(), "do something", func(provider.StreamEvent) {})
	msgs := a.contextManager.Messages()
	if c := countConvergenceMessages(msgs, "Shift to convergence"); c != 0 {
		t.Errorf("convergence pressure should not fire for maxIter < 20, got %d", c)
	}
}

// TestConvergencePressureFiresOnceForLongRun verifies that convergence messages
// are injected at most once per run, even across many iterations.
func TestConvergencePressureFiresOnceForLongRun(t *testing.T) {
	a := makeConvergenceAgent(100)
	_ = a.RunStream(context.Background(), "do something", func(provider.StreamEvent) {})
	msgs := a.contextManager.Messages()
	if c := countConvergenceMessages(msgs, "Shift to convergence"); c != 1 {
		t.Errorf("convergence pressure (85%%) should fire exactly once for long runs, got %d", c)
	}
	if c := countConvergenceMessages(msgs, "You MUST produce your final response"); c != 1 {
		t.Errorf("convergence pressure (95%%) should fire exactly once for long runs, got %d", c)
	}
}

// TestConvergencePressureContent verifies the convergence messages contain
// actionable guidance (iteration count, remaining budget, what to do).
func TestConvergencePressureContent(t *testing.T) {
	a := makeConvergenceAgent(20)
	_ = a.RunStream(context.Background(), "do something", func(provider.StreamEvent) {})
	msgs := a.contextManager.Messages()

	// The 85% message should mention iteration budget and convergence
	for _, msg := range msgs {
		for _, c := range msg.Content {
			if c.Type == "text" && strings.Contains(c.Text, "Shift to convergence") {
				if !strings.Contains(c.Text, "17/20") {
					t.Errorf("85%% message should mention iteration count, got: %s", c.Text)
				}
				if !strings.Contains(c.Text, "verification") && !strings.Contains(c.Text, "verify") {
					t.Errorf("85%% message should mention verification, got: %s", c.Text)
				}
			}
		}
	}
}
