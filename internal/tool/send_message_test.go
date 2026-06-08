package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/subagent"
)

func TestSendMessageToolDescriptionsClarifyWorkerSemantics(t *testing.T) {
	tool := SendMessageTool{}
	desc := tool.Description()
	for _, want := range []string{
		"swarm teammates",
		"task-like work",
		"prefer swarm_task_create",
		"sub-agent runs",
		"mailbox",
		"spawn a new sub-agent",
		"best-effort broadcast",
	} {
		if !strings.Contains(desc, want) {
			t.Fatalf("send_message description should mention %q, got %q", want, desc)
		}
	}

	params := string(tool.Parameters())
	for _, want := range []string{
		"task-like inbox work",
		"only receive mailbox messages",
		"For tracked teammate work, use swarm_task_create",
		"provide the team ID",
	} {
		if !strings.Contains(params, want) {
			t.Fatalf("send_message schema should mention %q, got %s", want, params)
		}
	}
}

func TestSendMessageToSubAgentWarnsBestEffort(t *testing.T) {
	mgr := subagent.NewManager(config.SubAgentConfig{})
	id := mgr.Spawn("test", "hold for mailbox test", "hold for mailbox test", nil, context.Background())
	mgr.SetCancel(id, func() {})

	tool := SendMessageTool{Manager: mgr}
	input, _ := json.Marshal(map[string]string{
		"to":      id,
		"message": "follow-up",
	})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", result.Content)
	}
	for _, want := range []string{"Mailbox message", "best-effort", "one-shot", "spawn_agent"} {
		if !strings.Contains(result.Content, want) {
			t.Fatalf("sub-agent send result should mention %q, got %q", want, result.Content)
		}
	}
}

func TestSendMessageToTeammateClarifiesUntrackedWork(t *testing.T) {
	mgr := swarmTestManager(t)
	team := mgr.CreateTeam("send-message-test", "leader")
	tm, err := mgr.SpawnTeammate(team.ID, "worker", "", nil)
	if err != nil {
		t.Fatalf("SpawnTeammate failed: %v", err)
	}
	defer mgr.DeleteTeam(team.ID)

	tool := SendMessageTool{SwarmMgr: mgr}
	input, _ := json.Marshal(map[string]string{
		"to":      tm.ID,
		"team_id": team.ID,
		"message": "return ok",
	})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", result.Content)
	}
	for _, want := range []string{"Task-like message", "not tracked", "teammate_results", "swarm_task_create"} {
		if !strings.Contains(result.Content, want) {
			t.Fatalf("teammate send result should mention %q, got %q", want, result.Content)
		}
	}
}
