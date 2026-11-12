package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// haltAgentTool succeeds with HaltAgent set, simulating the restart tool's
// success path (#5520).
type haltAgentTool struct {
	name     string
	executed *int
}

func (t haltAgentTool) Name() string                { return t.name }
func (t haltAgentTool) Description() string         { return "halt agent tool" }
func (t haltAgentTool) Parameters() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (t haltAgentTool) Execute(ctx context.Context, input json.RawMessage) (tool.Result, error) {
	*t.executed++
	return tool.Result{Content: "handover armed", HaltAgent: true}, nil
}

// TestRunStreamHaltAgentStopsRemainingToolCalls (#5520): a successful tool
// result carrying HaltAgent terminates the run within the same turn - the
// remaining tool call in the batch is never executed, no further LLM stream
// call happens, and the run unwinds with a clean nil error (not a cancel).
func TestRunStreamHaltAgentStopsRemainingToolCalls(t *testing.T) {
	mp := &mockProvider{
		chatResp: &provider.ChatResponse{
			Message: provider.Message{
				Role: "assistant",
				Content: []provider.ContentBlock{
					provider.ToolUseBlock("call_1", "halt", []byte(`{}`)),
					provider.ToolUseBlock("call_2", "count", []byte(`{}`)),
				},
			},
			Usage: provider.TokenUsage{InputTokens: 5, OutputTokens: 2},
		},
	}

	registry := tool.NewRegistry()
	var haltCount, countCount int
	if err := registry.Register(haltAgentTool{name: "halt", executed: &haltCount}); err != nil {
		t.Fatalf("register halt tool: %v", err)
	}
	if err := registry.Register(countingTool{name: "count", executed: &countCount}); err != nil {
		t.Fatalf("register counting tool: %v", err)
	}

	a := NewAgent(mp, registry, "", 2)

	var sysNotes []string
	err := a.RunStream(context.Background(), "hi", func(event provider.StreamEvent) {
		if event.Type == provider.StreamEventSystem {
			sysNotes = append(sysNotes, event.Text)
		}
	})
	if err != nil {
		t.Fatalf("halt-agent run must unwind cleanly, got %v", err)
	}

	if haltCount != 1 {
		t.Fatalf("expected halt tool to execute exactly once, got %d", haltCount)
	}
	if countCount != 0 {
		t.Fatalf("expected ZERO subsequent tool calls after a HaltAgent result, got %d", countCount)
	}
	if mp.streamCalls != 1 {
		t.Fatalf("expected no further LLM stream call after halt, got %d", mp.streamCalls)
	}

	stopped := false
	for _, n := range sysNotes {
		if strings.Contains(n, "Agent loop stopped") {
			stopped = true
		}
	}
	if !stopped {
		t.Errorf("expected a system event announcing the loop stop, got %v", sysNotes)
	}
}
