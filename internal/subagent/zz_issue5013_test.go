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

// issue5013BudgetRunner records SetToolCallBudget calls so tests can assert
// the exec-layer per-invocation budget wiring (#5013: the parameter is named
// max_tool_calls, so actual tool invocations must be bounded, not just LLM
// turns - the sa-18 runaway ran 248 calls without reaching the 60-turn cap).
type issue5013BudgetRunner struct {
	issue5013Runner
	budgets []int
}

func (r *issue5013BudgetRunner) SetToolCallBudget(budget int) {
	r.budgets = append(r.budgets, budget)
}

func TestIssue5013CallBudgetWiredFromMaxToolCalls(t *testing.T) {
	runner := &issue5013BudgetRunner{}
	mgr := NewManager(config.SubAgentConfig{MaxConcurrent: 1, Timeout: time.Second})
	id := mgr.Spawn("worker", "task", "task", nil, context.Background())

	Run(context.Background(), RunnerConfig{
		Task:       "task",
		Manager:    mgr,
		SubAgentID: id,
		AgentFactory: func(prov provider.Provider, tools interface{}, systemPrompt string, maxTurns int) AgentRunner {
			return runner
		},
		MaxToolCalls: 12,
	})

	if len(runner.budgets) != 1 || runner.budgets[0] != 12 {
		t.Fatalf("SetToolCallBudget calls = %v, want [12] (exec-layer budget must mirror MaxToolCalls, #5013)", runner.budgets)
	}
}

func TestIssue5013CallBudgetNotSetWhenUnbounded(t *testing.T) {
	runner := &issue5013BudgetRunner{}
	mgr := NewManager(config.SubAgentConfig{MaxConcurrent: 1, Timeout: time.Second})
	id := mgr.Spawn("worker", "task", "task", nil, context.Background())

	Run(context.Background(), RunnerConfig{
		Task:       "task",
		Manager:    mgr,
		SubAgentID: id,
		AgentFactory: func(prov provider.Provider, tools interface{}, systemPrompt string, maxTurns int) AgentRunner {
			return runner
		},
		// MaxToolCalls unset: no exec-layer budget override, deriveDefaultBudget applies.
	})

	if len(runner.budgets) != 0 {
		t.Fatalf("SetToolCallBudget calls = %v, want none (0 MaxToolCalls must not pin an exec-layer budget)", runner.budgets)
	}
}
