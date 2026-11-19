package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/swarm"
)

// lastActiveAgent emits one text event so the teammate records a result.
type lastActiveAgent struct{}

func (lastActiveAgent) RunStream(_ context.Context, _ string, onEvent func(provider.StreamEvent), _ ...string) error {
	if onEvent != nil {
		onEvent(provider.StreamEvent{Type: provider.StreamEventText, Text: "unit done"})
	}
	return nil
}

func TestTeammateListShowsLastActive(t *testing.T) {
	mgr := swarmTestManager(t)
	team := mgr.CreateTeam("active-team", "leader-1")
	if _, err := mgr.SpawnTeammate(team.ID, "researcher", "32", nil); err != nil {
		t.Fatalf("SpawnTeammate: %v", err)
	}

	tool := TeammateListTool{Manager: mgr}
	input, _ := json.Marshal(map[string]string{"team_id": team.ID})
	result, err := tool.Execute(context.Background(), input)
	if err != nil || result.IsError {
		t.Fatalf("teammate_list failed: err=%v isError=%v content=%s", err, result.IsError, result.Content)
	}
	if !strings.Contains(result.Content, "last active ") {
		t.Fatalf("teammate_list output must carry the last-active timestamp, got: %s", result.Content)
	}
}

func TestTeammateResultsShowsLastActive(t *testing.T) {
	mgr := swarm.NewManager(config.SwarmConfig{}, nil, func(provider.Provider, interface{}, string, int) swarm.AgentRunner {
		return lastActiveAgent{}
	}, nil)
	team := mgr.CreateTeam("active-team", "leader-1")
	snap, err := mgr.SpawnTeammate(team.ID, "worker", "32", nil)
	if err != nil {
		t.Fatalf("SpawnTeammate: %v", err)
	}

	reply := make(chan swarm.TaskResult, 1)
	if err := mgr.SendToTeammate(team.ID, snap.ID, swarm.MailMessage{Content: "run", Type: "task", ReplyTo: reply}); err != nil {
		t.Fatalf("SendToTeammate: %v", err)
	}
	select {
	case res := <-reply:
		if res.Error != nil {
			t.Fatalf("teammate task failed: %v", res.Error)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no TaskResult from teammate")
	}

	tool := TeammateResultsTool{Manager: mgr}
	input, _ := json.Marshal(map[string]string{"team_id": team.ID})
	result, err := tool.Execute(context.Background(), input)
	if err != nil || result.IsError {
		t.Fatalf("teammate_results failed: err=%v isError=%v content=%s", err, result.IsError, result.Content)
	}
	if !strings.Contains(result.Content, "unit done") {
		t.Fatalf("teammate_results output must carry the result, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "last active ") {
		t.Fatalf("teammate_results output must carry the last-active timestamp, got: %s", result.Content)
	}
}
