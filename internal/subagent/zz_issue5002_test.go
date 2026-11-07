package subagent

import (
	"context"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/provider"
)

// #5002 gap 2: sub-agent runs must enable an early pre-compact trigger on the
// constructed agent so single long-task sessions compact proactively (~70% of
// the metadata-derived window) instead of relying on the reactive overflow
// path. subagent.Run is the choke point for every spawn face (spawn_agent,
// skill, trial_fork, named agents).

type earlyCompactProbeRunner struct {
	setRatio  func(float64)
	runStream func(ctx context.Context, prompt string, onEvent func(provider.StreamEvent), origin ...string) error
}

func (r *earlyCompactProbeRunner) SetEarlyCompactRatio(ratio float64) {
	if r.setRatio != nil {
		r.setRatio(ratio)
	}
}

func (r *earlyCompactProbeRunner) RunStream(ctx context.Context, prompt string, onEvent func(provider.StreamEvent), origin ...string) error {
	if r.runStream != nil {
		return r.runStream(ctx, prompt, onEvent, origin...)
	}
	onEvent(provider.StreamEvent{Type: provider.StreamEventText, Text: "done"})
	return nil
}

func TestIssue5002SubAgentRunEnablesEarlyCompact(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{MaxConcurrent: 1, Timeout: 5 * time.Second})
	id := mgr.Spawn("probe", "task", "task", nil, context.Background())

	var gotRatio float64
	var ratioSet bool
	runner := &earlyCompactProbeRunner{
		setRatio: func(r float64) {
			gotRatio = r
			ratioSet = true
		},
	}
	Run(context.Background(), RunnerConfig{
		Task:       "task",
		Manager:    mgr,
		SubAgentID: id,
		AgentFactory: func(prov provider.Provider, tools interface{}, systemPrompt string, maxTurns int) AgentRunner {
			return runner
		},
	})

	if !ratioSet {
		t.Fatal("subagent.Run must enable the early compact trigger on the constructed agent (SetEarlyCompactRatio never called)")
	}
	if gotRatio != subagentEarlyCompactRatio {
		t.Fatalf("early compact ratio: got %v want %v", gotRatio, subagentEarlyCompactRatio)
	}
	if subagentEarlyCompactRatio != 0.70 {
		t.Fatalf("subagentEarlyCompactRatio constant must be 0.70 per #5002, got %v", subagentEarlyCompactRatio)
	}
}

// Agents whose type does not expose the optional interface (or whose context
// manager is nil) must still run: the early trigger is best-effort.
func TestIssue5002SubAgentRunToleratesUnawareAgent(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{MaxConcurrent: 1, Timeout: 5 * time.Second})
	id := mgr.Spawn("probe", "task", "task", nil, context.Background())

	ran := false
	bare := &earlyCompactProbeRunner{
		runStream: func(ctx context.Context, prompt string, onEvent func(provider.StreamEvent), origin ...string) error {
			ran = true
			return nil
		},
	}
	// Wrap so the outer type implements nothing extra.
	opaque := struct{ AgentRunner }{bare}
	Run(context.Background(), RunnerConfig{
		Task:       "task",
		Manager:    mgr,
		SubAgentID: id,
		AgentFactory: func(prov provider.Provider, tools interface{}, systemPrompt string, maxTurns int) AgentRunner {
			return opaque
		},
	})
	if !ran {
		t.Fatal("unaware agent must still run to completion")
	}
}
