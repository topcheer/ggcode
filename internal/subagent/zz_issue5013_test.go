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
// factory so a looping sub-agent is killed with a partial result, AND - the
// call-semantics completion - a nonzero budget must also be wired through
// the agent's SetToolCallBudget so the exec layer counts actual tool
// invocations (parallel batching lets one LLM turn issue many calls; sa-18
// ran 248 calls under an explicit 12-call task budget without ever hitting
// the turn cap).

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

// issue5013BudgetProbe captures the SetToolCallBudget wiring the runner is
// expected to perform for nonzero budgets.
type issue5013BudgetProbe struct {
	issue5013Runner
	budgets []int
}

func (p *issue5013BudgetProbe) SetToolCallBudget(budget int) {
	p.budgets = append(p.budgets, budget)
}

func TestIssue5013MaxToolCallsWiresPerCallBudget(t *testing.T) {
	// Retarget of the former TestIssue5013DefaultStaysUnbounded contract:
	// at the tool layer normalizeMaxToolCredits maps omitted/<=0 to the
	// conservative default before RunnerConfig is built, so a live spawn
	// never passes 0 anymore. The direct-Run contract that still holds is
	// the wiring one: N>0 must reach BOTH gates (turns + per-call budget).
	probe := &issue5013BudgetProbe{}
	mgr := NewManager(config.SubAgentConfig{MaxConcurrent: 1, Timeout: time.Second})
	id := mgr.Spawn("worker", "task", "task", nil, context.Background())

	Run(context.Background(), RunnerConfig{
		Task:       "task",
		Manager:    mgr,
		SubAgentID: id,
		AgentFactory: func(prov provider.Provider, tools interface{}, systemPrompt string, maxTurns int) AgentRunner {
			if maxTurns != 12 {
				t.Fatalf("AgentFactory maxTurns = %d, want 12", maxTurns)
			}
			return probe
		},
		MaxToolCalls: 12,
	})

	if len(probe.budgets) != 1 || probe.budgets[0] != 12 {
		t.Fatalf("SetToolCallBudget calls = %v, want [12] (exec-layer per-call gate, #5013 call semantics)", probe.budgets)
	}
}

func TestIssue5013ZeroMaxToolCallsLeavesBudgetUnset(t *testing.T) {
	// Legacy RunnerConfig.MaxToolCalls=0 keeps the turn-derived default:
	// the per-call gate must NOT be armed (a 0-call budget would kill the
	// run on its first tool call - check() would see totalCalls >= budget
	// with budget 0... budget<=0 means unlimited inside toolCallBudget,
	// but SetBudget(0) would still override the derived default with 0,
	// which effectiveBudget() maps to defaultBudget; arming it would be
	// dead state at best, so the contract is "not called").
	probe := &issue5013BudgetProbe{}
	mgr := NewManager(config.SubAgentConfig{MaxConcurrent: 1, Timeout: time.Second})
	id := mgr.Spawn("worker", "task", "task", nil, context.Background())

	Run(context.Background(), RunnerConfig{
		Task:       "task",
		Manager:    mgr,
		SubAgentID: id,
		AgentFactory: func(prov provider.Provider, tools interface{}, systemPrompt string, maxTurns int) AgentRunner {
			return probe
		},
	})

	if len(probe.budgets) != 0 {
		t.Fatalf("SetToolCallBudget calls = %v, want none for MaxToolCalls=0 (turn-derived default preserved)", probe.budgets)
	}
}

func TestIssue5013ToolWithoutBudgetSetterStillRuns(t *testing.T) {
	// The interface assertion must be optional: factories returning agents
	// without SetToolCallBudget (test doubles, future runner types) still
	// run to completion under the turn gate alone.
	mgr := NewManager(config.SubAgentConfig{MaxConcurrent: 1, Timeout: time.Second})
	id := mgr.Spawn("worker", "task", "task", nil, context.Background())

	Run(context.Background(), RunnerConfig{
		Task:       "task",
		Manager:    mgr,
		SubAgentID: id,
		AgentFactory: func(prov provider.Provider, tools interface{}, systemPrompt string, maxTurns int) AgentRunner {
			return &issue5013Runner{}
		},
		MaxToolCalls: 12,
	})

	sa, ok := mgr.Get(id)
	if !ok {
		t.Fatal("sub-agent record missing after Run")
	}
	if sa.Error != nil {
		t.Fatalf("run error = %v, want clean completion", sa.Error)
	}
}
