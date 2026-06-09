package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/swarm"
)

func swarmTestManager(t *testing.T) *swarm.Manager {
	t.Helper()
	return swarm.NewManager(
		config.SwarmConfig{},
		nil,
		nil, // agentFactory not needed for tool tests
		nil, // toolBuilder not needed for tool tests
	)
}

// ——— TeamCreate ———

func TestTeamCreateTool(t *testing.T) {
	mgr := swarmTestManager(t)
	tool := TeamCreateTool{Manager: mgr}

	input, _ := json.Marshal(map[string]string{
		"name":      "build-squad",
		"leader_id": "agent-1",
	})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	if result.Content == "" {
		t.Error("expected content")
	}
}

func TestTeamCreateTool_EmptyName(t *testing.T) {
	mgr := swarmTestManager(t)
	tool := TeamCreateTool{Manager: mgr}

	input, _ := json.Marshal(map[string]string{"name": "   "})
	result, _ := tool.Execute(context.Background(), input)
	if !result.IsError {
		t.Error("expected error for empty name")
	}
}

func TestTeamCreateTool_InvalidJSON(t *testing.T) {
	mgr := swarmTestManager(t)
	tool := TeamCreateTool{Manager: mgr}

	result, _ := tool.Execute(context.Background(), json.RawMessage(`{invalid}`))
	if !result.IsError {
		t.Error("expected error for invalid JSON")
	}
}

// ——— TeamDelete ———

func TestTeamDeleteTool(t *testing.T) {
	mgr := swarmTestManager(t)
	team := mgr.CreateTeam("test", "leader")

	tool := TeamDeleteTool{Manager: mgr}
	input, _ := json.Marshal(map[string]string{"team_id": team.ID})

	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
}

func TestTeamDeleteTool_NotFound(t *testing.T) {
	mgr := swarmTestManager(t)
	tool := TeamDeleteTool{Manager: mgr}

	input, _ := json.Marshal(map[string]string{"team_id": "nonexistent"})
	result, _ := tool.Execute(context.Background(), input)
	if !result.IsError {
		t.Error("expected error for nonexistent team")
	}
}

// ——— TeammateSpawn ———

func TestTeammateSpawnTool(t *testing.T) {
	mgr := swarmTestManager(t)
	team := mgr.CreateTeam("test", "leader")

	tool := TeammateSpawnTool{Manager: mgr}
	input, _ := json.Marshal(map[string]interface{}{
		"team_id": team.ID,
		"name":    "researcher",
		"color":   "32",
	})

	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
}

func TestTeammateSpawnTool_MissingName(t *testing.T) {
	mgr := swarmTestManager(t)
	team := mgr.CreateTeam("test", "leader")

	tool := TeammateSpawnTool{Manager: mgr}
	input, _ := json.Marshal(map[string]interface{}{
		"team_id": team.ID,
	})

	result, _ := tool.Execute(context.Background(), input)
	if !result.IsError {
		t.Error("expected error for missing name")
	}
}

func TestTeammateSpawnTool_TeamNotFound(t *testing.T) {
	mgr := swarmTestManager(t)
	tool := TeammateSpawnTool{Manager: mgr}

	input, _ := json.Marshal(map[string]interface{}{
		"team_id": "nonexistent",
		"name":    "researcher",
	})

	result, _ := tool.Execute(context.Background(), input)
	if !result.IsError {
		t.Error("expected error for nonexistent team")
	}
}

// ——— TeammateList ———

func TestTeammateListTool(t *testing.T) {
	mgr := swarmTestManager(t)
	team := mgr.CreateTeam("test", "leader")
	mgr.SpawnTeammate(team.ID, "researcher", "32", nil)

	tool := TeammateListTool{Manager: mgr}
	input, _ := json.Marshal(map[string]string{"team_id": team.ID})

	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	if result.Content == "" {
		t.Error("expected content")
	}
}

func TestTeammateListTool_Empty(t *testing.T) {
	mgr := swarmTestManager(t)
	team := mgr.CreateTeam("test", "leader")

	tool := TeammateListTool{Manager: mgr}
	input, _ := json.Marshal(map[string]string{"team_id": team.ID})

	result, _ := tool.Execute(context.Background(), input)
	if result.Content != "No teammates.\n" {
		t.Errorf("expected 'No teammates.', got %q", result.Content)
	}
}

// ——— TeammateShutdown ———

func TestTeammateShutdownTool(t *testing.T) {
	mgr := swarmTestManager(t)
	team := mgr.CreateTeam("test", "leader")
	tm, _ := mgr.SpawnTeammate(team.ID, "researcher", "", nil)

	tool := TeammateShutdownTool{Manager: mgr}
	input, _ := json.Marshal(map[string]string{
		"team_id":     team.ID,
		"teammate_id": tm.ID,
	})

	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
}

func TestTeammateShutdownTool_NotFound(t *testing.T) {
	mgr := swarmTestManager(t)
	team := mgr.CreateTeam("test", "leader")

	tool := TeammateShutdownTool{Manager: mgr}
	input, _ := json.Marshal(map[string]string{
		"team_id":     team.ID,
		"teammate_id": "nonexistent",
	})

	result, _ := tool.Execute(context.Background(), input)
	if !result.IsError {
		t.Error("expected error for nonexistent teammate")
	}
}

func TestTeamToolDescriptionsClarifyTeammateLifecycleAndResults(t *testing.T) {
	deleteDesc := TeamDeleteTool{}.Description()
	for _, want := range []string{"destructive", "team board is removed", "explicitly asks"} {
		if !strings.Contains(deleteDesc, want) {
			t.Fatalf("team_delete description should mention %q, got %q", want, deleteDesc)
		}
	}

	spawnDesc := TeammateSpawnTool{}.Description()
	for _, want := range []string{"persistent idle loop", "parallel work", "distinct role", "swarm_task_create", "send_message only for untracked"} {
		if !strings.Contains(spawnDesc, want) {
			t.Fatalf("teammate_spawn description should mention %q, got %q", want, spawnDesc)
		}
	}

	shutdownDesc := TeammateShutdownTool{}.Description()
	for _, want := range []string{"will not accept new inbox or board work", "does not merely cancel one task", "retiring that teammate"} {
		if !strings.Contains(shutdownDesc, want) {
			t.Fatalf("teammate_shutdown description should mention %q, got %q", want, shutdownDesc)
		}
	}

	resultsDesc := TeammateResultsTool{}.Description()
	for _, want := range []string{"latest completed output only", "not a queue", "not cleared after reading", "swarm_task_list", "teammate_list"} {
		if !strings.Contains(resultsDesc, want) {
			t.Fatalf("teammate_results description should mention %q, got %q", want, resultsDesc)
		}
	}
	params := string(TeammateResultsTool{}.Parameters())
	if !strings.Contains(params, "not a history queue") || !strings.Contains(params, "not cleared after reading") {
		t.Fatalf("teammate_results teammate_id schema should clarify latest-result semantics: %s", params)
	}
}
