package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/swarm"
	"github.com/topcheer/ggcode/internal/task"
	"github.com/topcheer/ggcode/internal/tool"
)

// skipE2E skips the test unless GGCODE_E2E=1 is set.
func skipE2E(t *testing.T) {
	t.Helper()
	if os.Getenv("GGCODE_E2E") != "1" {
		t.Skip("Set GGCODE_E2E=1 to run end-to-end tests (requires real LLM API key)")
	}
}

// newProviderFromEnv creates a real provider from environment variables.
// Expects:
//   - GGCODE_E2E_PROTOCOL: "openai", "anthropic", or "gemini" (default: "openai")
//   - GGCODE_E2E_API_KEY: API key
//   - GGCODE_E2E_BASE_URL: base URL (optional)
//   - GGCODE_E2E_MODEL: model name (default: protocol-specific)
func newProviderFromEnv(t *testing.T) provider.Provider {
	t.Helper()

	protocol := os.Getenv("GGCODE_E2E_PROTOCOL")
	if protocol == "" {
		protocol = "openai"
	}
	apiKey := os.Getenv("GGCODE_E2E_API_KEY")
	if apiKey == "" {
		t.Fatal("GGCODE_E2E_API_KEY is required")
	}
	baseURL := os.Getenv("GGCODE_E2E_BASE_URL")
	model := os.Getenv("GGCODE_E2E_MODEL")
	if model == "" {
		switch protocol {
		case "openai":
			model = "gpt-4o-mini"
		case "anthropic":
			model = "claude-haiku-4-5-20251001"
		case "gemini":
			model = "gemini-2.0-flash"
		}
	}

	prov, err := provider.NewProvider(&config.ResolvedEndpoint{
		VendorID:   protocol,
		VendorName: protocol,
		Protocol:   protocol,
		BaseURL:    baseURL,
		APIKey:     apiKey,
		Model:      model,
		MaxTokens:  1024,
	})
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}
	return prov
}

// TestE2E_TeamCreateSpawnAndMessage tests the full swarm lifecycle with a real LLM.
//
// Setup:
//
//	GGCODE_E2E=1 GGCODE_E2E_API_KEY=sk-xxx go test ./cmd/e2e_test/ -run TestE2E -v -timeout 5m
func TestE2E_TeamCreateSpawnAndMessage(t *testing.T) {
	skipE2E(t)
	prov := newProviderFromEnv(t)

	registry := tool.NewRegistry()

	factory := func(prov provider.Provider, tools interface{}, systemPrompt string, maxTurns int) swarm.AgentRunner {
		reg, _ := tools.(*tool.Registry)
		if reg == nil {
			reg = registry
		}
		a := agent.NewAgent(prov, reg, systemPrompt, maxTurns)
		a.SetPermissionPolicy(permission.NewConfigPolicyWithMode(nil, []string{"."}, permission.AutopilotMode))
		return a
	}
	builder := func(_ []string) interface{} {
		return registry
	}

	cfg := config.SwarmConfig{
		MaxTeammatesPerTeam: 3,
		TeammateTimeout:     2 * time.Minute,
		InboxSize:           16,
	}
	mgr := swarm.NewManager(cfg, prov, factory, builder)
	defer mgr.Shutdown()

	// Step 1: Create team
	snap := mgr.CreateTeam("e2e-team", "leader")
	if snap.ID == "" {
		t.Fatal("Expected non-empty team ID")
	}
	t.Logf("Created team: %s (%s)", snap.Name, snap.ID)

	// Step 2: Spawn teammate
	tmSnap, err := mgr.SpawnTeammate(snap.ID, "worker-1", "32", nil)
	if err != nil {
		t.Fatalf("SpawnTeammate failed: %v", err)
	}
	t.Logf("Spawned teammate: %s (%s)", tmSnap.Name, tmSnap.ID)

	time.Sleep(500 * time.Millisecond)

	// Step 3: Send a task and wait for the result via ReplyTo
	replyCh := make(chan swarm.TaskResult, 1)
	msg := swarm.MailMessage{
		From:    "leader",
		Content: "What is 2+2? Answer with just the number, nothing else.",
		Type:    "task",
		ReplyTo: replyCh,
	}

	if err := mgr.SendToTeammate(snap.ID, tmSnap.ID, msg); err != nil {
		t.Fatalf("SendToTeammate failed: %v", err)
	}
	t.Logf("Sent task to %s, waiting for result...", tmSnap.ID)

	select {
	case result := <-replyCh:
		t.Logf("Teammate result: %q", result.Output)
		if result.Error != nil {
			t.Errorf("Teammate returned error: %v", result.Error)
		}
		if result.Output == "" {
			t.Error("Expected non-empty output from teammate")
		}
		if !strings.Contains(result.Output, "4") {
			t.Errorf("Expected result to contain '4', got: %s", result.Output)
		}
	case <-time.After(3 * time.Minute):
		t.Fatal("Timed out waiting for teammate result")
	}

	// Step 4: Verify teammate is idle again
	teamSnap, ok := mgr.GetTeam(snap.ID)
	if !ok {
		t.Fatal("Team not found")
	}
	if len(teamSnap.Teammates) != 1 {
		t.Fatalf("Expected 1 teammate, got %d", len(teamSnap.Teammates))
	}
	if teamSnap.Teammates[0].Status != swarm.TeammateIdle {
		t.Errorf("Expected teammate status idle, got %s", teamSnap.Teammates[0].Status)
	}

	t.Log("E2E test passed!")
}

// TestE2E_MultipleTeammates tests spawning multiple teammates and sending tasks to each.
func TestE2E_MultipleTeammates(t *testing.T) {
	skipE2E(t)
	prov := newProviderFromEnv(t)

	registry := tool.NewRegistry()
	factory := func(prov provider.Provider, tools interface{}, systemPrompt string, maxTurns int) swarm.AgentRunner {
		reg, _ := tools.(*tool.Registry)
		if reg == nil {
			reg = registry
		}
		return agent.NewAgent(prov, reg, systemPrompt, maxTurns)
	}
	builder := func(_ []string) interface{} { return registry }

	cfg := config.SwarmConfig{
		MaxTeammatesPerTeam: 3,
		TeammateTimeout:     2 * time.Minute,
		InboxSize:           16,
	}
	mgr := swarm.NewManager(cfg, prov, factory, builder)
	defer mgr.Shutdown()

	snap := mgr.CreateTeam("multi-team", "leader")

	tm1, err := mgr.SpawnTeammate(snap.ID, "worker-a", "32", nil)
	if err != nil {
		t.Fatalf("Spawn worker-a failed: %v", err)
	}
	tm2, err := mgr.SpawnTeammate(snap.ID, "worker-b", "33", nil)
	if err != nil {
		t.Fatalf("Spawn worker-b failed: %v", err)
	}

	time.Sleep(500 * time.Millisecond)

	reply1 := make(chan swarm.TaskResult, 1)
	reply2 := make(chan swarm.TaskResult, 1)

	mgr.SendToTeammate(snap.ID, tm1.ID, swarm.MailMessage{
		Content: "What is the capital of France? Answer with just the city name.",
		Type:    "task",
		ReplyTo: reply1,
	})
	mgr.SendToTeammate(snap.ID, tm2.ID, swarm.MailMessage{
		Content: "What is the capital of Japan? Answer with just the city name.",
		Type:    "task",
		ReplyTo: reply2,
	})

	for i, ch := range []chan swarm.TaskResult{reply1, reply2} {
		select {
		case result := <-ch:
			t.Logf("Teammate %d result: %q", i+1, result.Output)
			if result.Error != nil {
				t.Errorf("Teammate %d error: %v", i+1, result.Error)
			}
			if result.Output == "" {
				t.Errorf("Teammate %d: expected non-empty output", i+1)
			}
		case <-time.After(3 * time.Minute):
			t.Fatalf("Teammate %d timed out", i+1)
		}
	}

	t.Log("Multiple teammates test passed!")
}

// TestE2E_SwarmTaskBoard tests the shared task board workflow.
func TestE2E_SwarmTaskBoard(t *testing.T) {
	skipE2E(t)
	prov := newProviderFromEnv(t)

	registry := tool.NewRegistry()
	factory := func(prov provider.Provider, tools interface{}, systemPrompt string, maxTurns int) swarm.AgentRunner {
		reg, _ := tools.(*tool.Registry)
		if reg == nil {
			reg = registry
		}
		return agent.NewAgent(prov, reg, systemPrompt, maxTurns)
	}
	builder := func(_ []string) interface{} { return registry }

	cfg := config.SwarmConfig{
		MaxTeammatesPerTeam: 3,
		TeammateTimeout:     2 * time.Minute,
		InboxSize:           16,
	}
	mgr := swarm.NewManager(cfg, prov, factory, builder)
	defer mgr.Shutdown()

	snap := mgr.CreateTeam("task-team", "leader")

	taskMgr, err := mgr.EnsureTaskManager(snap.ID)
	if err != nil {
		t.Fatalf("EnsureTaskManager failed: %v", err)
	}

	task1 := taskMgr.Create("Research topic A", "Find information about topic A", "", map[string]string{"assignee": "worker-1"})
	task2 := taskMgr.Create("Research topic B", "Find information about topic B", "", nil)

	t.Logf("Created tasks: %s, %s", task1.ID, task2.ID)

	tasks := taskMgr.List()
	if len(tasks) != 2 {
		t.Fatalf("Expected 2 tasks, got %d", len(tasks))
	}

	out, _ := json.Marshal(snap)
	t.Logf("Team snapshot: %s", string(out))

	if !strings.Contains(string(out), "task-team") {
		t.Error("Expected team name in snapshot")
	}

	inProgress := task.TaskStatus(task.StatusInProgress)
	owner := "worker-1"
	_, err = taskMgr.Update(task1.ID, task.UpdateOptions{
		Status: &inProgress,
		Owner:  &owner,
	})
	if err != nil {
		t.Errorf("Failed to claim task: %v", err)
	}

	completed := task.TaskStatus(task.StatusCompleted)
	_, err = taskMgr.Update(task2.ID, task.UpdateOptions{
		Status: &completed,
	})
	if err != nil {
		t.Errorf("Failed to complete task: %v", err)
	}

	t.Log("Task board test passed!")
}

// TestE2E_SendMessageToolIntegration tests the send_message tool with swarm routing.
func TestE2E_SendMessageToolIntegration(t *testing.T) {
	skipE2E(t)
	prov := newProviderFromEnv(t)

	registry := tool.NewRegistry()
	factory := func(prov provider.Provider, tools interface{}, systemPrompt string, maxTurns int) swarm.AgentRunner {
		reg, _ := tools.(*tool.Registry)
		if reg == nil {
			reg = registry
		}
		return agent.NewAgent(prov, reg, systemPrompt, maxTurns)
	}
	builder := func(_ []string) interface{} { return registry }

	cfg := config.SwarmConfig{
		MaxTeammatesPerTeam: 3,
		TeammateTimeout:     2 * time.Minute,
		InboxSize:           16,
	}
	mgr := swarm.NewManager(cfg, prov, factory, builder)
	defer mgr.Shutdown()

	registry.Register(tool.TeamCreateTool{Manager: mgr})
	registry.Register(tool.TeammateSpawnTool{Manager: mgr})
	registry.Register(tool.TeammateListTool{Manager: mgr})
	registry.Register(tool.SendMessageTool{SwarmMgr: mgr})

	// Step 1: Create team via tool
	t1Tool, ok := registry.Get("team_create")
	if !ok {
		t.Fatal("team_create tool not found")
	}
	result, err := t1Tool.Execute(context.Background(), json.RawMessage(`{"name": "integration-team"}`))
	if err != nil {
		t.Fatalf("team_create tool failed: %v", err)
	}
	t.Logf("team_create result: %s", result.Content)

	var teamSnap map[string]interface{}
	json.Unmarshal([]byte(strings.TrimSpace(result.Content)), &teamSnap)
	teamID, _ := teamSnap["ID"].(string)
	if teamID == "" {
		t.Fatal("Could not parse team ID from team_create result")
	}

	// Step 2: Spawn teammate via tool
	t2Tool, ok := registry.Get("teammate_spawn")
	if !ok {
		t.Fatal("teammate_spawn tool not found")
	}
	result, err = t2Tool.Execute(context.Background(), json.RawMessage(fmt.Sprintf(
		`{"team_id": "%s", "name": "test-worker"}`, teamID,
	)))
	if err != nil {
		t.Fatalf("teammate_spawn tool failed: %v", err)
	}
	t.Logf("teammate_spawn result: %s", result.Content)

	var tmSnap map[string]interface{}
	json.Unmarshal([]byte(strings.TrimSpace(result.Content)), &tmSnap)
	tmID, _ := tmSnap["ID"].(string)
	if tmID == "" {
		t.Fatal("Could not parse teammate ID from teammate_spawn result")
	}

	time.Sleep(500 * time.Millisecond)

	// Step 3: Send message via tool (this now blocks until result is ready)
	t3Tool, ok := registry.Get("send_message")
	if !ok {
		t.Fatal("send_message tool not found")
	}
	sendInput, _ := json.Marshal(map[string]string{
		"to":      tmID,
		"message": "What is 3*7? Answer with just the number.",
		"team_id": teamID,
	})
	result, err = t3Tool.Execute(context.Background(), sendInput)
	if err != nil {
		t.Fatalf("send_message tool failed: %v", err)
	}
	t.Logf("send_message result: %s", result.Content)

	if result.IsError {
		t.Errorf("send_message returned error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "21") {
		t.Errorf("Expected result to contain '21', got: %s", result.Content)
	}

	t.Log("Send message tool integration test passed!")
}

// TestE2E_BroadcastWithoutTeamID tests that send_message to="*" reaches swarm teammates
// even when team_id is NOT provided (the bug-fix scenario).
func TestE2E_BroadcastWithoutTeamID(t *testing.T) {
	skipE2E(t)
	prov := newProviderFromEnv(t)

	registry := tool.NewRegistry()
	factory := func(prov provider.Provider, tools interface{}, systemPrompt string, maxTurns int) swarm.AgentRunner {
		reg, _ := tools.(*tool.Registry)
		if reg == nil {
			reg = registry
		}
		return agent.NewAgent(prov, reg, systemPrompt, maxTurns)
	}
	builder := func(_ []string) interface{} { return registry }

	cfg := config.SwarmConfig{
		MaxTeammatesPerTeam: 3,
		TeammateTimeout:     2 * time.Minute,
		InboxSize:           16,
	}
	mgr := swarm.NewManager(cfg, prov, factory, builder)
	defer mgr.Shutdown()

	registry.Register(tool.TeamCreateTool{Manager: mgr})
	registry.Register(tool.TeammateSpawnTool{Manager: mgr})
	registry.Register(tool.SendMessageTool{SwarmMgr: mgr})

	// Create team and spawn 2 teammates
	t1Tool, _ := registry.Get("team_create")
	result, _ := t1Tool.Execute(context.Background(), json.RawMessage(`{"name": "broadcast-team"}`))
	var teamSnap map[string]interface{}
	json.Unmarshal([]byte(strings.TrimSpace(result.Content)), &teamSnap)
	teamID, _ := teamSnap["ID"].(string)

	t2Tool, _ := registry.Get("teammate_spawn")
	t2Tool.Execute(context.Background(), json.RawMessage(fmt.Sprintf(
		`{"team_id": "%s", "name": "tm-a"}`, teamID,
	)))
	t2Tool.Execute(context.Background(), json.RawMessage(fmt.Sprintf(
		`{"team_id": "%s", "name": "tm-b"}`, teamID,
	)))

	time.Sleep(500 * time.Millisecond)

	// Broadcast without team_id — this should reach swarm teammates
	t3Tool, _ := registry.Get("send_message")
	sendInput, _ := json.Marshal(map[string]string{
		"to":      "*",
		"message": "Hello team! Reply with just 'ok'.",
	})
	result, err := t3Tool.Execute(context.Background(), sendInput)
	if err != nil {
		t.Fatalf("broadcast failed: %v", err)
	}
	t.Logf("Broadcast result: %s", result.Content)

	if strings.Contains(result.Content, "No running agents") {
		t.Error("Broadcast should have reached swarm teammates but got 'No running agents'")
	}
	if !strings.Contains(result.Content, "Broadcast sent to") {
		t.Errorf("Expected broadcast confirmation, got: %s", result.Content)
	}

	t.Log("Broadcast without team_id test passed!")
}

// TestE2E_TargetedMessageAutoRoute tests that send_message with to="tm-X" (no team_id)
// automatically finds the teammate across all teams.
func TestE2E_TargetedMessageAutoRoute(t *testing.T) {
	skipE2E(t)
	prov := newProviderFromEnv(t)

	registry := tool.NewRegistry()
	factory := func(prov provider.Provider, tools interface{}, systemPrompt string, maxTurns int) swarm.AgentRunner {
		reg, _ := tools.(*tool.Registry)
		if reg == nil {
			reg = registry
		}
		a := agent.NewAgent(prov, reg, systemPrompt, maxTurns)
		a.SetPermissionPolicy(permission.NewConfigPolicyWithMode(nil, []string{"."}, permission.AutopilotMode))
		return a
	}
	builder := func(_ []string) interface{} { return registry }

	cfg := config.SwarmConfig{
		MaxTeammatesPerTeam: 3,
		TeammateTimeout:     2 * time.Minute,
		InboxSize:           16,
	}
	mgr := swarm.NewManager(cfg, prov, factory, builder)
	defer mgr.Shutdown()

	registry.Register(tool.TeamCreateTool{Manager: mgr})
	registry.Register(tool.TeammateSpawnTool{Manager: mgr})
	registry.Register(tool.SendMessageTool{SwarmMgr: mgr})

	// Create team and spawn teammate
	t1Tool, _ := registry.Get("team_create")
	result, _ := t1Tool.Execute(context.Background(), json.RawMessage(`{"name": "auto-route-team"}`))
	var teamSnap map[string]interface{}
	json.Unmarshal([]byte(strings.TrimSpace(result.Content)), &teamSnap)
	teamID, _ := teamSnap["ID"].(string)

	t2Tool, _ := registry.Get("teammate_spawn")
	result, _ = t2Tool.Execute(context.Background(), json.RawMessage(fmt.Sprintf(
		`{"team_id": "%s", "name": "auto-worker"}`, teamID,
	)))
	var tmSnap map[string]interface{}
	json.Unmarshal([]byte(strings.TrimSpace(result.Content)), &tmSnap)
	tmID, _ := tmSnap["ID"].(string)

	time.Sleep(500 * time.Millisecond)

	// Send targeted message WITHOUT team_id — auto-route should find the teammate
	t3Tool, _ := registry.Get("send_message")
	sendInput, _ := json.Marshal(map[string]string{
		"to":      tmID,
		"message": "What is 5+3? Answer with just the number.",
	})
	result, err := t3Tool.Execute(context.Background(), sendInput)
	if err != nil {
		t.Fatalf("auto-route send failed: %v", err)
	}
	t.Logf("Auto-route result: %s", result.Content)

	if result.IsError {
		t.Fatalf("Expected success but got error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "8") {
		t.Errorf("Expected result to contain '8', got: %s", result.Content)
	}

	t.Log("Targeted message auto-route test passed!")
}

// TestE2E_TeammateNoInfiniteLoop verifies a teammate does not spin when receiving
// a vague message — it should complete within a reasonable time, not loop forever.
func TestE2E_TeammateNoInfiniteLoop(t *testing.T) {
	skipE2E(t)
	prov := newProviderFromEnv(t)

	registry := tool.NewRegistry()
	factory := func(prov provider.Provider, tools interface{}, systemPrompt string, maxTurns int) swarm.AgentRunner {
		reg, _ := tools.(*tool.Registry)
		if reg == nil {
			reg = registry
		}
		a := agent.NewAgent(prov, reg, systemPrompt, 3) // limit max turns to prevent runaway
		a.SetPermissionPolicy(permission.NewConfigPolicyWithMode(nil, []string{"."}, permission.AutopilotMode))
		return a
	}
	builder := func(_ []string) interface{} { return registry }

	cfg := config.SwarmConfig{
		MaxTeammatesPerTeam: 3,
		TeammateTimeout:     90 * time.Second, // hard timeout
		InboxSize:           16,
	}
	mgr := swarm.NewManager(cfg, prov, factory, builder)
	defer mgr.Shutdown()

	snap := mgr.CreateTeam("loop-test", "leader")
	tmSnap, err := mgr.SpawnTeammate(snap.ID, "worker", "32", nil)
	if err != nil {
		t.Fatalf("SpawnTeammate failed: %v", err)
	}

	time.Sleep(300 * time.Millisecond)

	// Send a deliberately vague message that might tempt an LLM to loop
	replyCh := make(chan swarm.TaskResult, 1)
	mgr.SendToTeammate(snap.ID, tmSnap.ID, swarm.MailMessage{
		From:    "leader",
		Content: "Think about what you would do if you were asked to think about thinking. Stop after one sentence.",
		Type:    "task",
		ReplyTo: replyCh,
	})

	select {
	case result := <-replyCh:
		t.Logf("Teammate completed (no infinite loop). Output length: %d", len(result.Output))
		if result.Error != nil {
			t.Logf("Teammate error (acceptable): %v", result.Error)
		}
	case <-time.After(2 * time.Minute):
		t.Fatal("Teammate did not complete within 2 minutes — possible infinite loop")
	}

	// Verify teammate returned to idle
	teamSnap, _ := mgr.GetTeam(snap.ID)
	for _, tm := range teamSnap.Teammates {
		if tm.ID == tmSnap.ID && tm.Status != swarm.TeammateIdle {
			t.Errorf("Expected teammate to be idle after task, got: %s", tm.Status)
		}
	}

	t.Log("No infinite loop test passed!")
}

// TestE2E_TeamBroadcastAndTargetedMix tests both broadcast and targeted messaging
// in the same session, verifying message delivery counts are correct.
func TestE2E_TeamBroadcastAndTargetedMix(t *testing.T) {
	skipE2E(t)
	prov := newProviderFromEnv(t)

	registry := tool.NewRegistry()
	factory := func(prov provider.Provider, tools interface{}, systemPrompt string, maxTurns int) swarm.AgentRunner {
		reg, _ := tools.(*tool.Registry)
		if reg == nil {
			reg = registry
		}
		a := agent.NewAgent(prov, reg, systemPrompt, maxTurns)
		a.SetPermissionPolicy(permission.NewConfigPolicyWithMode(nil, []string{"."}, permission.AutopilotMode))
		return a
	}
	builder := func(_ []string) interface{} { return registry }

	cfg := config.SwarmConfig{
		MaxTeammatesPerTeam: 3,
		TeammateTimeout:     2 * time.Minute,
		InboxSize:           16,
	}
	mgr := swarm.NewManager(cfg, prov, factory, builder)
	defer mgr.Shutdown()

	snap := mgr.CreateTeam("mix-team", "leader")

	// Spawn 2 teammates
	tm1, _ := mgr.SpawnTeammate(snap.ID, "alpha", "32", nil)
	tm2, _ := mgr.SpawnTeammate(snap.ID, "beta", "33", nil)

	time.Sleep(500 * time.Millisecond)

	// Broadcast should reach both
	sent := mgr.BroadcastToTeam(snap.ID, swarm.MailMessage{
		From:    "leader",
		Content: "Broadcast: respond with just your name.",
		Type:    "message",
	})
	if len(sent) != 2 {
		t.Fatalf("Expected broadcast to 2 teammates, got %d", len(sent))
	}
	t.Logf("Broadcast reached: %v", sent)

	// Now send targeted tasks to each
	reply1 := make(chan swarm.TaskResult, 1)
	reply2 := make(chan swarm.TaskResult, 1)

	mgr.SendToTeammate(snap.ID, tm1.ID, swarm.MailMessage{
		Content: "What is 1+1? Answer with just the number.",
		Type:    "task",
		ReplyTo: reply1,
	})
	mgr.SendToTeammate(snap.ID, tm2.ID, swarm.MailMessage{
		Content: "What is 2+2? Answer with just the number.",
		Type:    "task",
		ReplyTo: reply2,
	})

	// Both should complete
	for i, ch := range []chan swarm.TaskResult{reply1, reply2} {
		select {
		case result := <-ch:
			t.Logf("Teammate %d result: %q", i+1, result.Output)
			if result.Output == "" {
				t.Errorf("Teammate %d returned empty output", i+1)
			}
		case <-time.After(3 * time.Minute):
			t.Fatalf("Teammate %d timed out", i+1)
		}
	}

	// Verify both back to idle
	teamSnap, _ := mgr.GetTeam(snap.ID)
	idleCount := 0
	for _, tm := range teamSnap.Teammates {
		if tm.Status == swarm.TeammateIdle {
			idleCount++
		}
	}
	if idleCount != 2 {
		t.Errorf("Expected 2 idle teammates, got %d", idleCount)
	}

	t.Log("Broadcast + targeted mix test passed!")
}

// TestE2E_ShutdownDuringTask verifies that shutting down a teammate mid-task
// does not cause deadlock or panic.
func TestE2E_ShutdownDuringTask(t *testing.T) {
	skipE2E(t)
	prov := newProviderFromEnv(t)

	registry := tool.NewRegistry()
	factory := func(prov provider.Provider, tools interface{}, systemPrompt string, maxTurns int) swarm.AgentRunner {
		reg, _ := tools.(*tool.Registry)
		if reg == nil {
			reg = registry
		}
		a := agent.NewAgent(prov, reg, systemPrompt, maxTurns)
		a.SetPermissionPolicy(permission.NewConfigPolicyWithMode(nil, []string{"."}, permission.AutopilotMode))
		return a
	}
	builder := func(_ []string) interface{} { return registry }

	cfg := config.SwarmConfig{
		MaxTeammatesPerTeam: 3,
		TeammateTimeout:     2 * time.Minute,
		InboxSize:           16,
	}
	mgr := swarm.NewManager(cfg, prov, factory, builder)
	defer mgr.Shutdown()

	snap := mgr.CreateTeam("shutdown-test", "leader")
	tmSnap, _ := mgr.SpawnTeammate(snap.ID, "doomed-worker", "31", nil)

	time.Sleep(300 * time.Millisecond)

	// Send a long-ish task, then immediately shut down the teammate
	mgr.SendToTeammate(snap.ID, tmSnap.ID, swarm.MailMessage{
		Content: "Write a very long essay about the history of computing.",
		Type:    "task",
	})

	// Shut down while task is likely running
	time.Sleep(200 * time.Millisecond)
	err := mgr.ShutdownTeammate(snap.ID, tmSnap.ID)
	if err != nil {
		t.Fatalf("ShutdownTeammate failed: %v", err)
	}

	// Give time for goroutine to clean up
	time.Sleep(500 * time.Millisecond)

	// Manager should still be functional — the teammate should be either
	// shutting_down (cancelled mid-task) or idle (finished before cancel reached).
	// Either outcome is acceptable; we mainly verify no panic or deadlock.
	teamSnap, ok := mgr.GetTeam(snap.ID)
	if !ok {
		t.Fatal("Team should still exist after teammate shutdown")
	}
	for _, tm := range teamSnap.Teammates {
		if tm.ID == tmSnap.ID {
			t.Logf("Teammate final status: %s", tm.Status)
			if tm.Status != swarm.TeammateShuttingDown && tm.Status != swarm.TeammateIdle {
				t.Errorf("Expected shutting_down or idle, got %s", tm.Status)
			}
		}
	}

	t.Log("Shutdown during task test passed!")
}
