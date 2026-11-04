package subagent

import (
	"context"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/provider"
)

// Issue #5013: sub-agent tool-call loops ran unbounded (173+ reads before
// manual cancel) because the runner hardcoded the AgentFactory maxTurns
// argument to 0 (= unbounded). RunnerConfig.MaxToolCalls must reach the
// factory so a looping sub-agent is killed with a partial result.

type issue5013Runner struct{}

func (r *issue5013Runner) RunStream(ctx context.Context, prompt string, onEvent func(provider.StreamEvent), origin ...string) error {
	onEvent(provider.StreamEvent{Type: provider.StreamEventText, Text: "done"})
	onEvent(provider.StreamEvent{Type: provider.StreamEventDone})
	return nil
}

func TestIssue5013MaxToolCallsReachesFactory(t *testing.T) {
	captured := []int{}
	mgr := NewManager(config.SubAgentConfig{MaxConcurrent: 1, Timeout: time.Second})
	id := mgr.Spawn("worker", "task", "task", nil, context.Background())

	Run(context.Background(), RunnerConfig{
		Task:       "task",
		Manager:    mgr,
		SubAgentID: id,
		AgentFactory: func(prov provider.Provider, tools interface{}, systemPrompt string, maxTurns int) AgentRunner {
			captured = append(captured, maxTurns)
			return &issue5013Runner{}
		},
		MaxToolCalls: 42,
	})

	if len(captured) != 1 {
		t.Fatalf("AgentFactory called %d times, want 1", len(captured))
	}
	if captured[0] != 42 {
		t.Fatalf("AgentFactory maxTurns = %d, want 42 (MaxToolCalls passthrough, #5013)", captured[0])
	}
}

func TestIssue5013DefaultStaysUnbounded(t *testing.T) {
	captured := []int{}
	mgr := NewManager(config.SubAgentConfig{MaxConcurrent: 1, Timeout: time.Second})
	id := mgr.Spawn("worker", "task", "task", nil, context.Background())

	Run(context.Background(), RunnerConfig{
		Task:       "task",
		Manager:    mgr,
		SubAgentID: id,
		AgentFactory: func(prov provider.Provider, tools interface{}, systemPrompt string, maxTurns int) AgentRunner {
			captured = append(captured, maxTurns)
			return &issue5013Runner{}
		},
		// MaxToolCalls unset: historical unbounded behavior preserved.
	})

	if len(captured) != 1 || captured[0] != 0 {
		t.Fatalf("AgentFactory maxTurns = %v, want [0] (unbounded default preserved)", captured)
	}
}
