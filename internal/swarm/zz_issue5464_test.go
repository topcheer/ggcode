package swarm

import (
	"context"
	"sync"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
)

// policyAwareRunner records whether SetPermissionPolicy was called (#5464).
type policyAwareRunner struct {
	mu       sync.Mutex
	pol      permission.PermissionPolicy
	gotPolis bool
}

func (r *policyAwareRunner) RunStream(ctx context.Context, prompt string, onEvent func(provider.StreamEvent), origin ...string) error {
	return nil
}

func (r *policyAwareRunner) SetPermissionPolicy(p permission.PermissionPolicy) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pol = p
	r.gotPolis = true
}

type bareRunner struct{}

func (r *bareRunner) RunStream(ctx context.Context, prompt string, onEvent func(provider.StreamEvent), origin ...string) error {
	return nil
}

type stubPolicy struct{ permission.PermissionPolicy }

func TestSpawnTeammateWiresPermissionPolicy(t *testing.T) {
	runner := &policyAwareRunner{}
	m := NewManager(config.SwarmConfig{MaxTeammatesPerTeam: 4}, nil, func(prov provider.Provider, tools interface{}, sp string, mt int) AgentRunner {
		return runner
	}, nil)
	m.SetPermissionPolicyGetter(func() permission.PermissionPolicy {
		return stubPolicy{}
	})

	team := m.CreateTeam("t1", "leader")

	snap, err := m.SpawnTeammate(team.ID, "w1", "", nil)
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if snap.ID == "" {
		t.Fatal("empty teammate id")
	}
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if !runner.gotPolis {
		t.Fatal("policy getter wired but SetPermissionPolicy not called on teammate agent")
	}
	if runner.pol == nil {
		t.Fatal("nil policy applied")
	}
}

func TestSpawnTeammateNoGetterLeavesRunnerUntouched(t *testing.T) {
	runner := &policyAwareRunner{}
	m := NewManager(config.SwarmConfig{MaxTeammatesPerTeam: 4}, nil, func(prov provider.Provider, tools interface{}, sp string, mt int) AgentRunner {
		return runner
	}, nil)

	team := m.CreateTeam("t1", "leader")

	if _, err := m.SpawnTeammate(team.ID, "w1", "", nil); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if runner.gotPolis {
		t.Fatal("no policy getter wired but SetPermissionPolicy was called")
	}
}

func TestSpawnTeammateBareRunnerNoPanic(t *testing.T) {
	m := NewManager(config.SwarmConfig{MaxTeammatesPerTeam: 4}, nil, func(prov provider.Provider, tools interface{}, sp string, mt int) AgentRunner {
		return &bareRunner{}
	}, nil)
	m.SetPermissionPolicyGetter(func() permission.PermissionPolicy {
		return stubPolicy{}
	})
	team := m.CreateTeam("t1", "leader")

	if _, err := m.SpawnTeammate(team.ID, "w1", "", nil); err != nil {
		t.Fatalf("spawn: %v", err)
	}
}
