package swarm

import (
	"testing"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
)

// policyAgentRunner implements AgentRunner plus the SetPermissionPolicy hook
// that #5464 threads through SpawnTeammate.
type policyAgentRunner struct {
	mockAgentRunner
	policy permission.PermissionPolicy
}

func (p *policyAgentRunner) SetPermissionPolicy(policy permission.PermissionPolicy) {
	p.policy = policy
}

// Issue #5464 pin: SpawnTeammate must thread the Manager-level permission
// policy into spawned teammate agents (same gating as the primary agent),
// and must leave agents untouched when no policy was configured.
// The teammate agent is a loop-local value, so the factory captures the
// runners it creates and the test asserts on those.
func TestIssue5464TeammatePolicyThreading(t *testing.T) {
	var muMuMu = make(chan struct{}, 1)
	muMuMu <- struct{}{}
	var runners []*policyAgentRunner
	factory := func(_ provider.Provider, _ interface{}, _ string, _ int) AgentRunner {
		r := &policyAgentRunner{}
		<-muMuMu
		runners = append(runners, r)
		muMuMu <- struct{}{}
		return r
	}

	t.Run("policy threaded into teammate agent", func(t *testing.T) {
		runners = nil
		m := NewManager(config.SwarmConfig{}, nil, factory, nil)
		m.SetWorkingDir(t.TempDir())
		m.CreateTeam("t1", "")
		want := permission.NewConfigPolicy(map[string]permission.Decision{}, nil)
		m.SetTeammatePermissionPolicy(want)
		if _, err := m.SpawnTeammate("team-1", "dev", "", nil); err != nil {
			t.Fatalf("SpawnTeammate: %v", err)
		}
		if len(runners) != 1 {
			t.Fatalf("expected 1 runner created, got %d", len(runners))
		}
		if runners[0].policy == nil {
			t.Fatal("SetPermissionPolicy was not called on teammate agent")
		}
	})

	t.Run("no policy configured keeps factory default", func(t *testing.T) {
		runners = nil
		m := NewManager(config.SwarmConfig{}, nil, factory, nil)
		m.SetWorkingDir(t.TempDir())
		m.CreateTeam("t2", "")
		if _, err := m.SpawnTeammate("team-1", "dev", "", nil); err != nil {
			t.Fatalf("SpawnTeammate: %v", err)
		}
		if len(runners) != 1 {
			t.Fatalf("expected 1 runner created, got %d", len(runners))
		}
		if runners[0].policy != nil {
			t.Fatal("policy was set without SetTeammatePermissionPolicy being called")
		}
	})
}
