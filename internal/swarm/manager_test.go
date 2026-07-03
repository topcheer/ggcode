package swarm

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/task"
)

// mockAgent is a minimal AgentRunner that records calls.
type mockAgent struct {
	mu      sync.Mutex
	calls   []string
	results map[string]string // prompt -> response
}

func newMockAgent() *mockAgent {
	return &mockAgent{
		results: make(map[string]string),
	}
}

func (a *mockAgent) RunStream(_ context.Context, prompt string, onEvent func(provider.StreamEvent)) error {
	a.mu.Lock()
	a.calls = append(a.calls, prompt)
	resp, ok := a.results[prompt]
	a.mu.Unlock()

	if !ok {
		resp = "mock result"
	}
	if onEvent != nil {
		onEvent(provider.StreamEvent{Type: provider.StreamEventText, Text: resp})
	}
	return nil
}

func (a *mockAgent) getCalls() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.calls...)
}

type usageAwareMockAgent struct {
	*mockAgent
	onUsage func(provider.TokenUsage)
}

func (a *usageAwareMockAgent) SetUsageHandler(fn func(provider.TokenUsage)) {
	a.onUsage = fn
}

func (a *usageAwareMockAgent) RunStream(ctx context.Context, prompt string, onEvent func(provider.StreamEvent)) error {
	if a.onUsage != nil {
		a.onUsage(provider.TokenUsage{InputTokens: 23, OutputTokens: 8})
	}
	return a.mockAgent.RunStream(ctx, prompt, onEvent)
}

func testManager(t *testing.T) (*Manager, *mockAgent) {
	t.Helper()
	ma := newMockAgent()
	m := NewManager(
		config.SwarmConfig{},
		nil, // provider not needed when we inject agentFactory
		func(_ provider.Provider, _ interface{}, _ string, _ int) AgentRunner {
			return ma
		},
		func(_ []string) interface{} { return nil },
	)
	return m, ma
}

func TestManager_CreateTeam(t *testing.T) {
	m, _ := testManager(t)

	snap := m.CreateTeam("my-team", "leader-1")
	if snap.ID != "team-1" {
		t.Errorf("expected team-1, got %s", snap.ID)
	}
	if snap.Name != "my-team" {
		t.Errorf("expected my-team, got %s", snap.Name)
	}
	if snap.LeaderID != "leader-1" {
		t.Errorf("expected leader-1, got %s", snap.LeaderID)
	}
	if len(snap.Teammates) != 0 {
		t.Errorf("expected 0 teammates, got %d", len(snap.Teammates))
	}
}

func TestManager_CreateMultipleTeams(t *testing.T) {
	m, _ := testManager(t)

	snap1 := m.CreateTeam("team-a", "leader-1")
	snap2 := m.CreateTeam("team-b", "leader-2")

	if snap1.ID == snap2.ID {
		t.Error("teams should have unique IDs")
	}

	teams := m.ListTeams()
	if len(teams) != 2 {
		t.Errorf("expected 2 teams, got %d", len(teams))
	}
}

func TestManager_DeleteTeam(t *testing.T) {
	m, _ := testManager(t)

	snap := m.CreateTeam("doomed", "leader-1")
	err := m.DeleteTeam(snap.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, ok := m.GetTeam(snap.ID)
	if ok {
		t.Error("team should be deleted")
	}
}

func TestManager_DeleteTeam_NotFound(t *testing.T) {
	m, _ := testManager(t)
	err := m.DeleteTeam("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent team")
	}
}

func TestManager_SpawnTeammate(t *testing.T) {
	m, _ := testManager(t)

	team := m.CreateTeam("test-team", "leader-1")
	tmSnap, err := m.SpawnTeammate(team.ID, "researcher", "32", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tmSnap.Name != "researcher" {
		t.Errorf("expected researcher, got %s", tmSnap.Name)
	}
	if tmSnap.Status != TeammateIdle {
		t.Errorf("expected idle, got %s", tmSnap.Status)
	}

	// Verify team snapshot updated
	updated, _ := m.GetTeam(team.ID)
	if len(updated.Teammates) != 1 {
		t.Errorf("expected 1 teammate, got %d", len(updated.Teammates))
	}
}

func TestManager_SpawnTeammate_ExceedsMax(t *testing.T) {
	m, _ := testManager(t)
	m.cfg.MaxTeammatesPerTeam = 2

	team := m.CreateTeam("small-team", "leader-1")
	m.SpawnTeammate(team.ID, "tm-1", "", nil)
	m.SpawnTeammate(team.ID, "tm-2", "", nil)

	_, err := m.SpawnTeammate(team.ID, "tm-3", "", nil)
	if err == nil {
		t.Error("expected error when exceeding max teammates")
	}
}

func TestManager_SpawnTeammate_TeamNotFound(t *testing.T) {
	m, _ := testManager(t)
	_, err := m.SpawnTeammate("nonexistent", "tm-1", "", nil)
	if err == nil {
		t.Error("expected error for nonexistent team")
	}
}

func TestManager_ShutdownTeammate(t *testing.T) {
	m, _ := testManager(t)

	team := m.CreateTeam("test-team", "leader-1")
	tmSnap, _ := m.SpawnTeammate(team.ID, "coder", "33", nil)

	// Give idle loop time to start
	time.Sleep(50 * time.Millisecond)

	err := m.ShutdownTeammate(team.ID, tmSnap.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify teammate is shutting down
	updated, _ := m.GetTeam(team.ID)
	found := false
	for _, tm := range updated.Teammates {
		if tm.ID == tmSnap.ID {
			found = true
			if tm.Status != TeammateShuttingDown {
				t.Errorf("expected shutting_down, got %s", tm.Status)
			}
		}
	}
	if !found {
		t.Error("teammate should still exist in team")
	}
}

func TestManager_ShutdownTeammate_NotFound(t *testing.T) {
	m, _ := testManager(t)
	team := m.CreateTeam("test", "leader")

	err := m.ShutdownTeammate(team.ID, "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent teammate")
	}
}

func TestManager_SendToTeammate(t *testing.T) {
	m, _ := testManager(t)

	team := m.CreateTeam("test-team", "leader-1")
	tmSnap, _ := m.SpawnTeammate(team.ID, "researcher", "", nil)

	msg := MailMessage{
		From:    "leader-1",
		Content: "Investigate the API",
		Type:    "task",
	}

	err := m.SendToTeammate(team.ID, tmSnap.ID, msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestManager_SendToTeammate_TeamNotFound(t *testing.T) {
	m, _ := testManager(t)
	err := m.SendToTeammate("nonexistent", "tm-1", MailMessage{})
	if err == nil {
		t.Error("expected error for nonexistent team")
	}
}

func TestManager_SendToTeammate_TeammateNotFound(t *testing.T) {
	m, _ := testManager(t)
	team := m.CreateTeam("test", "leader")
	err := m.SendToTeammate(team.ID, "nonexistent", MailMessage{})
	if err == nil {
		t.Error("expected error for nonexistent teammate")
	}
}

func TestManager_BroadcastToTeam(t *testing.T) {
	m, _ := testManager(t)

	team := m.CreateTeam("test-team", "leader-1")
	m.SpawnTeammate(team.ID, "tm-1", "", nil)
	m.SpawnTeammate(team.ID, "tm-2", "", nil)

	time.Sleep(50 * time.Millisecond) // let idle loops start

	msg := MailMessage{
		From:    "leader-1",
		Content: "All hands on deck",
		Type:    "message",
	}

	sent := m.BroadcastToTeam(team.ID, msg)
	if len(sent) != 2 {
		t.Errorf("expected 2 deliveries, got %d", len(sent))
	}
}

func TestManager_BroadcastToTeam_NotFound(t *testing.T) {
	m, _ := testManager(t)
	sent := m.BroadcastToTeam("nonexistent", MailMessage{})
	if sent != nil {
		t.Errorf("expected nil, got %v", sent)
	}
}

func TestManager_EnsureTaskManager(t *testing.T) {
	m, _ := testManager(t)
	team := m.CreateTeam("test", "leader")

	// First call creates
	tm1, err := m.EnsureTaskManager(team.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tm1 == nil {
		t.Fatal("expected task manager")
	}

	// Second call returns same
	tm2, _ := m.EnsureTaskManager(team.ID)
	if tm1 != tm2 {
		t.Error("expected same task manager instance")
	}
}

func TestManager_EnsureTaskManager_NotFound(t *testing.T) {
	m, _ := testManager(t)
	_, err := m.EnsureTaskManager("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent team")
	}
}

func TestManager_Shutdown(t *testing.T) {
	m, _ := testManager(t)

	team := m.CreateTeam("test", "leader")
	m.SpawnTeammate(team.ID, "tm-1", "", nil)
	m.SpawnTeammate(team.ID, "tm-2", "", nil)

	time.Sleep(50 * time.Millisecond)

	m.Shutdown()

	// Root context should be done
	select {
	case <-m.RootContext().Done():
		// expected
	default:
		t.Error("root context should be cancelled after shutdown")
	}
}

func TestManager_OnUpdate(t *testing.T) {
	var events []Event
	var mu sync.Mutex

	m, _ := testManager(t)
	m.SetOnUpdate(func(ev Event) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, ev)
	})

	team := m.CreateTeam("test", "leader")

	mu.Lock()
	found := false
	for _, ev := range events {
		if ev.Type == "team_created" && ev.TeamID == team.ID {
			found = true
		}
	}
	mu.Unlock()
	if !found {
		t.Error("expected team_created event")
	}
}

func TestManager_ListTeamBoardsIncludesTasksAndTeammates(t *testing.T) {
	m, _ := testManager(t)
	defer m.Shutdown()

	team := m.CreateTeam("board-team", "leader-1")
	if _, err := m.SpawnTeammate(team.ID, "coder", "32", nil); err != nil {
		t.Fatalf("spawn teammate: %v", err)
	}
	tmMgr, err := m.EnsureTaskManager(team.ID)
	if err != nil {
		t.Fatalf("ensure task manager: %v", err)
	}
	taskSnap := tmMgr.Create("Implement board", "Render shared work", "Implementing", map[string]string{"assignee": "tm-1", "priority": "high"})

	boards := m.ListTeamBoards()
	if len(boards) != 1 {
		t.Fatalf("expected 1 board, got %d: %+v", len(boards), boards)
	}
	board := boards[0]
	if board.ID != team.ID || board.Name != "board-team" || board.LeaderID != "leader-1" {
		t.Fatalf("unexpected board identity: %+v", board)
	}
	if len(board.Teammates) != 1 {
		t.Fatalf("expected 1 teammate, got %d: %+v", len(board.Teammates), board.Teammates)
	}
	if board.Teammates[0].Name != "coder" || board.Teammates[0].Color != "32" || board.Teammates[0].Status != string(TeammateIdle) {
		t.Fatalf("unexpected teammate snapshot: %+v", board.Teammates[0])
	}
	if len(board.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d: %+v", len(board.Tasks), board.Tasks)
	}
	gotTask := board.Tasks[0]
	if gotTask.ID != taskSnap.ID || gotTask.Subject != "Implement board" || gotTask.Description != "Render shared work" {
		t.Fatalf("unexpected task snapshot: %+v", gotTask)
	}
	if gotTask.ActiveForm != "Implementing" || gotTask.Status != string(task.StatusPending) || gotTask.Assignee != "tm-1" {
		t.Fatalf("unexpected task status/assignee: %+v", gotTask)
	}
	if gotTask.Metadata["priority"] != "high" {
		t.Fatalf("expected metadata to be copied, got %+v", gotTask.Metadata)
	}
}

func TestManager_DefaultConfig(t *testing.T) {
	m, _ := testManager(t)
	if m.cfg.MaxTeammatesPerTeam != 8 {
		t.Errorf("expected default max 8, got %d", m.cfg.MaxTeammatesPerTeam)
	}
	if m.cfg.TeammateTimeout != 0 {
		t.Errorf("expected default timeout 0 (no timeout), got %v", m.cfg.TeammateTimeout)
	}
	if m.cfg.InboxSize != 32 {
		t.Errorf("expected default inbox 32, got %d", m.cfg.InboxSize)
	}
}

func TestManager_ConcurrentSpawn(t *testing.T) {
	m, _ := testManager(t)
	team := m.CreateTeam("concurrent-test", "leader")

	var wg sync.WaitGroup
	errCh := make(chan error, 5)

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, err := m.SpawnTeammate(team.ID, string(rune('a'+idx)), "", nil)
			if err != nil {
				errCh <- err
			}
		}(i)
	}
	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("concurrent spawn error: %v", err)
	}

	snap, _ := m.GetTeam(team.ID)
	if len(snap.Teammates) != 5 {
		t.Errorf("expected 5 teammates, got %d", len(snap.Teammates))
	}
}

func TestManager_SpawnTeammate_NilFactory(t *testing.T) {
	cfg := config.SwarmConfig{MaxTeammatesPerTeam: 3}
	m := NewManager(cfg, nil, nil, nil)
	defer m.Shutdown()

	team := m.CreateTeam("test", "leader")
	_, err := m.SpawnTeammate(team.ID, "worker", "", nil)
	if err != nil {
		t.Fatalf("SpawnTeammate with nil factory should not error: %v", err)
	}

	snap, _ := m.GetTeam(team.ID)
	if len(snap.Teammates) != 1 {
		t.Fatalf("expected 1 teammate, got %d", len(snap.Teammates))
	}
	// The teammate should be created but with nil agent — idle loop should still run
	if snap.Teammates[0].Status != TeammateIdle {
		t.Fatalf("expected idle status, got %s", snap.Teammates[0].Status)
	}
}

func TestManager_SpawnTeammate_NilProvider(t *testing.T) {
	cfg := config.SwarmConfig{MaxTeammatesPerTeam: 3}
	// Nil provider + factory that creates agent with nil provider
	factory := func(prov provider.Provider, tools interface{}, systemPrompt string, maxTurns int) AgentRunner {
		return nil // no agent without provider
	}
	m := NewManager(cfg, nil, factory, nil)
	defer m.Shutdown()

	team := m.CreateTeam("test", "leader")
	// Should not panic even with nil provider
	snap, err := m.SpawnTeammate(team.ID, "worker", "", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if snap.Status != TeammateIdle {
		t.Fatalf("expected idle, got %s", snap.Status)
	}
}

func TestManager_NextTeamIDRaceFree(t *testing.T) {
	cfg := config.SwarmConfig{MaxTeammatesPerTeam: 100}
	m := NewManager(cfg, nil, nil, nil)
	defer m.Shutdown()

	team := m.CreateTeam("test", "leader")

	var wg sync.WaitGroup
	errCh := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := m.SpawnTeammate(team.ID, "worker", "", nil)
			if err != nil {
				errCh <- err
			}
		}()
	}
	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("spawn error: %v", err)
	}

	snap, _ := m.GetTeam(team.ID)
	// All 20 teammates should have unique IDs
	ids := map[string]bool{}
	for _, tm := range snap.Teammates {
		if ids[tm.ID] {
			t.Errorf("duplicate teammate ID: %s", tm.ID)
		}
		ids[tm.ID] = true
	}
}

// mockAgentRunner is a simple AgentRunner for testing SpawnTeammate with a real factory.
type mockAgentRunner struct {
	runStreamFn func(ctx context.Context, prompt string, onEvent func(provider.StreamEvent)) error
}

func (m *mockAgentRunner) RunStream(ctx context.Context, prompt string, onEvent func(provider.StreamEvent)) error {
	if m.runStreamFn != nil {
		return m.runStreamFn(ctx, prompt, onEvent)
	}
	return nil
}

func TestManager_SpawnTeammate_WithFactory(t *testing.T) {
	cfg := config.SwarmConfig{MaxTeammatesPerTeam: 3, InboxSize: 16}
	factory := func(prov provider.Provider, tools interface{}, systemPrompt string, maxTurns int) AgentRunner {
		return &mockAgentRunner{
			runStreamFn: func(ctx context.Context, prompt string, onEvent func(provider.StreamEvent)) error {
				onEvent(provider.StreamEvent{Type: provider.StreamEventText, Text: "result: " + prompt})
				return nil
			},
		}
	}
	m := NewManager(cfg, nil, factory, nil)
	defer m.Shutdown()

	team := m.CreateTeam("test", "leader")
	snap, err := m.SpawnTeammate(team.ID, "worker", "", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Send a task to the teammate
	var mu sync.Mutex
	var resultEvent Event
	m.SetOnUpdate(func(ev Event) {
		if ev.Type == "teammate_idle" {
			mu.Lock()
			resultEvent = ev
			mu.Unlock()
		}
	})

	err = m.SendToTeammate(team.ID, snap.ID, MailMessage{
		From:    "leader",
		Content: "hello world",
		Type:    "task",
	})
	if err != nil {
		t.Fatalf("SendToTeammate error: %v", err)
	}

	// Wait for result
	deadline := time.After(3 * time.Second)
	for {
		mu.Lock()
		done := resultEvent.Result != ""
		mu.Unlock()
		if done {
			break
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for teammate to process task")
		default:
			time.Sleep(50 * time.Millisecond)
		}
	}

	if !contains(resultEvent.Result, "hello world") {
		t.Fatalf("expected result to contain task content, got: %s", resultEvent.Result)
	}
}

func TestManager_SpawnTeammate_ForwardsUsageHandler(t *testing.T) {
	cfg := config.SwarmConfig{MaxTeammatesPerTeam: 3, InboxSize: 16}
	agent := &usageAwareMockAgent{mockAgent: newMockAgent()}
	m := NewManager(cfg, nil, func(prov provider.Provider, tools interface{}, systemPrompt string, maxTurns int) AgentRunner {
		return agent
	}, nil)
	defer m.Shutdown()

	var got provider.TokenUsage
	m.SetUsageHandler(func(usage provider.TokenUsage) {
		got = usage
	})

	team := m.CreateTeam("test", "leader")
	snap, err := m.SpawnTeammate(team.ID, "worker", "", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if agent.onUsage == nil {
		t.Fatal("expected teammate usage handler to be installed")
	}

	done := make(chan struct{})
	m.SetOnUpdate(func(ev Event) {
		if ev.Type == "teammate_idle" {
			close(done)
		}
	})

	err = m.SendToTeammate(team.ID, snap.ID, MailMessage{
		From:    "leader",
		Content: "hello world",
		Type:    "task",
	})
	if err != nil {
		t.Fatalf("SendToTeammate error: %v", err)
	}

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for teammate to process task")
	}

	if got != (provider.TokenUsage{InputTokens: 23, OutputTokens: 8}) {
		t.Fatalf("expected forwarded usage, got %+v", got)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || containsSubstr(s, sub))
}

func containsSubstr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestManager_CancelAll(t *testing.T) {
	// Create a manager where we can directly access internal teams
	ma := newMockAgent()
	m := NewManager(
		config.SwarmConfig{},
		nil,
		func(_ provider.Provider, _ interface{}, _ string, _ int) AgentRunner {
			return ma
		},
		func(_ []string) interface{} { return nil },
	)

	teamSnap := m.CreateTeam("cancel-team", "leader-1")
	m.SpawnTeammate(teamSnap.ID, "w1", "", nil)
	m.SpawnTeammate(teamSnap.ID, "w2", "", nil)
	m.SpawnTeammate(teamSnap.ID, "w3", "", nil)

	// Directly set w1 and w2 to Working status via internal team access
	m.mu.Lock()
	team := m.teams[teamSnap.ID]
	m.mu.Unlock()
	for _, tm := range team.listTeammates() {
		if tm.Name == "w1" || tm.Name == "w2" {
			tm.setStatus(TeammateWorking)
		}
	}

	// CancelAll should stop every live teammate, including idle ones that could
	// otherwise pick up queued work after the UI already shows cancellation.
	m.CancelAll()

	snap := m.ListTeams()
	for _, tm := range snap[0].Teammates {
		if tm.Status != TeammateShuttingDown {
			t.Errorf("expected %s shutting_down, got %s", tm.Name, tm.Status)
		}
	}
}

func TestManager_CancelAll_Empty(t *testing.T) {
	m, _ := testManager(t)
	// No teams — should be no-op
	m.CancelAll()
}

func TestBuildTeammateSystemPrompt_WithWorkingDir(t *testing.T) {
	prompt := buildTeammateSystemPrompt("researcher", "review-team", "/home/user/project")
	if !containsSubstr(prompt, `researcher`) {
		t.Error("expected teammate name in prompt")
	}
	if !containsSubstr(prompt, "review-team") {
		t.Error("expected team name in prompt")
	}
	if !containsSubstr(prompt, "/home/user/project") {
		t.Error("expected working directory in prompt")
	}
	if !containsSubstr(prompt, "Working directory:") {
		t.Error("expected 'Working directory:' label in prompt")
	}
	if !containsSubstr(prompt, "shared task board as the source of truth") {
		t.Error("expected prompt to mention shared task board coordination")
	}
	if !containsSubstr(prompt, "do not re-claim it from the board first") {
		t.Error("expected prompt to distinguish direct assignment from board claiming")
	}
	if !containsSubstr(prompt, "avoid repetitive back-and-forth or message loops") {
		t.Error("expected prompt to discourage message loops")
	}
}

func TestManager_SystemPromptBuilder(t *testing.T) {
	m, _ := testManager(t)
	m.SetWorkingDir("/test/dir")

	called := false
	m.SetSystemPromptBuilder(func(name, teamName, workingDir string) string {
		called = true
		if name != "tester" {
			t.Errorf("expected name %q, got %q", "tester", name)
		}
		if teamName != "my-team" {
			t.Errorf("expected teamName %q, got %q", "my-team", teamName)
		}
		if workingDir != "/test/dir" {
			t.Errorf("expected workingDir %q, got %q", "/test/dir", workingDir)
		}
		return "custom-prompt"
	})

	// Spawn a teammate — it should use the builder, not the fallback
	m.CreateTeam("my-team", "")
	_, err := m.SpawnTeammate("team-1", "tester", "", nil)
	if err != nil {
		t.Fatalf("SpawnTeammate failed: %v", err)
	}

	if !called {
		t.Error("systemPromptBuilder was not called during SpawnTeammate")
	}
	m.CancelAll()
}

func TestManager_SystemPromptBuilder_FallbackToMinimal(t *testing.T) {
	m, _ := testManager(t)
	m.SetWorkingDir("/test/dir")
	// Do NOT set systemPromptBuilder — should fall back to buildTeammateSystemPrompt

	m.CreateTeam("fallback-team", "")
	_, err := m.SpawnTeammate("team-1", "fallback-teammate", "", nil)
	if err != nil {
		t.Fatalf("SpawnTeammate failed: %v", err)
	}
	// If no panic and teammate spawned, fallback works
	m.CancelAll()
}

func TestBuildTeammateSystemPrompt_NoWorkingDir(t *testing.T) {
	prompt := buildTeammateSystemPrompt("researcher", "review-team", "")
	if containsSubstr(prompt, "Working directory:") {
		t.Error("should not contain 'Working directory:' when empty")
	}
}

func TestManager_SetWorkingDir(t *testing.T) {
	m, _ := testManager(t)
	m.SetWorkingDir("/test/dir")
	if m.workingDir != "/test/dir" {
		t.Errorf("expected /test/dir, got %s", m.workingDir)
	}
}

func TestManager_ListTeamStatuses_Empty(t *testing.T) {
	m, _ := testManager(t)
	statuses := m.ListTeamStatuses()
	if len(statuses) != 0 {
		t.Fatalf("expected 0 teams, got %d", len(statuses))
	}
}

func TestManager_ListTeamStatuses_WithTeammates(t *testing.T) {
	m, tools := testManager(t)
	team := m.CreateTeam("status-team", "leader-id")

	// Spawn teammates
	_, err := m.SpawnTeammate(team.ID, "coder", "32", nil)
	if err != nil {
		t.Fatal(err)
	}

	_, err = m.SpawnTeammate(team.ID, "reviewer", "31", nil)
	if err != nil {
		t.Fatal(err)
	}

	// ListTeamStatuses should return lightweight info without copying events
	statuses := m.ListTeamStatuses()
	if len(statuses) != 1 {
		t.Fatalf("expected 1 team, got %d", len(statuses))
	}

	ts := statuses[0]
	if ts.ID != team.ID {
		t.Errorf("expected team ID %s, got %s", team.ID, ts.ID)
	}
	if ts.Name != "status-team" {
		t.Errorf("expected team name status-team, got %s", ts.Name)
	}
	if ts.LeaderID != "leader-id" {
		t.Errorf("expected leader-id, got %s", ts.LeaderID)
	}
	if len(ts.Teammates) != 2 {
		t.Fatalf("expected 2 teammates, got %d", len(ts.Teammates))
	}

	// Teammates should be sorted by ID
	if ts.Teammates[0].ID == "" || ts.Teammates[1].ID == "" {
		t.Fatal("teammate IDs should not be empty")
	}

	// Verify the lightweight info has no events field (compile-time check via type)
	// The TeammateStatusInfo type has only ID, Name, Status — no Events.
	// This test ensures the API returns correct values.
	for _, tm := range ts.Teammates {
		if tm.Status != TeammateIdle {
			t.Errorf("expected teammate %s to be idle, got %s", tm.ID, tm.Status)
		}
	}

	// Suppress unused tools warning
	_ = tools
}

func TestManager_ListTeamStatuses_DoesNotCopyEvents(t *testing.T) {
	// Verify that ListTeamStatuses returns data even after appending many events.
	// The key guarantee: it does NOT copy events, so it's safe to call at high frequency.
	m, _ := testManager(t)
	team := m.CreateTeam("events-team", "leader")

	tm, err := m.SpawnTeammate(team.ID, "coder", "32", nil)
	if err != nil {
		t.Fatal(err)
	}

	// Append events directly to the teammate (bypass the runner)
	// Access the teammate via the manager's internal team
	tmID := tm.ID
	m.mu.Lock()
	for _, t := range m.teams {
		if raw, ok := t.Teammates[tmID]; ok {
			for i := 0; i < 100; i++ {
				raw.appendEvent(TeammateEvent{Type: TeammateEventText, Text: "text chunk"})
			}
		}
	}
	m.mu.Unlock()

	// ListTeamStatuses should still work and be lightweight
	statuses := m.ListTeamStatuses()
	if len(statuses) != 1 {
		t.Fatalf("expected 1 team, got %d", len(statuses))
	}
	if len(statuses[0].Teammates) != 1 {
		t.Fatalf("expected 1 teammate, got %d", len(statuses[0].Teammates))
	}

	// The status info should not expose events (type has no Events field)
	if statuses[0].Teammates[0].Status != TeammateIdle {
		t.Errorf("expected idle, got %s", statuses[0].Teammates[0].Status)
	}
}

func TestManager_TeammateEventsSince(t *testing.T) {
	m, _ := testManager(t)
	team := m.CreateTeam("events-team", "leader")

	tm, err := m.SpawnTeammate(team.ID, "coder", "32", nil)
	if err != nil {
		t.Fatal(err)
	}

	// Add events
	tmID := tm.ID
	m.mu.Lock()
	for _, t := range m.teams {
		if raw, ok := t.Teammates[tmID]; ok {
			for i := 0; i < 10; i++ {
				raw.appendEvent(TeammateEvent{Type: TeammateEventText, Text: fmt.Sprintf("text-%d", i)})
			}
		}
	}
	m.mu.Unlock()

	// Get all events
	events, total, ok := m.TeammateEventsSince(tmID, 0)
	if !ok {
		t.Fatal("expected teammate to be found")
	}
	if total != 10 {
		t.Fatalf("expected 10 total, got %d", total)
	}
	if len(events) != 10 {
		t.Fatalf("expected 10 events, got %d", len(events))
	}

	// Get incremental events from index 7
	events, total, ok = m.TeammateEventsSince(tmID, 7)
	if !ok {
		t.Fatal("expected teammate to be found")
	}
	if total != 10 {
		t.Fatalf("expected 10 total, got %d", total)
	}
	if len(events) != 3 {
		t.Fatalf("expected 3 incremental events, got %d", len(events))
	}

	// Non-existent teammate
	_, _, ok = m.TeammateEventsSince("nonexistent", 0)
	if ok {
		t.Error("expected false for nonexistent teammate")
	}
}

func TestManager_TeammateEventsSince_NoEvents(t *testing.T) {
	m, _ := testManager(t)
	team := m.CreateTeam("empty-team", "leader")

	tm, err := m.SpawnTeammate(team.ID, "coder", "32", nil)
	if err != nil {
		t.Fatal(err)
	}

	events, total, ok := m.TeammateEventsSince(tm.ID, 0)
	if !ok {
		t.Fatal("expected teammate to be found")
	}
	if total != 0 {
		t.Fatalf("expected 0 total, got %d", total)
	}
	if len(events) != 0 {
		t.Fatalf("expected 0 events, got %d", len(events))
	}
}
