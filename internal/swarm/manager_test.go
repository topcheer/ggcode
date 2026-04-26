package swarm

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/provider"
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

func TestManager_DefaultConfig(t *testing.T) {
	m, _ := testManager(t)
	if m.cfg.MaxTeammatesPerTeam != 8 {
		t.Errorf("expected default max 8, got %d", m.cfg.MaxTeammatesPerTeam)
	}
	if m.cfg.TeammateTimeout != 30*time.Minute {
		t.Errorf("expected default timeout 30m, got %v", m.cfg.TeammateTimeout)
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
	var resultEvent Event
	m.SetOnUpdate(func(ev Event) {
		if ev.Type == "teammate_idle" {
			resultEvent = ev
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
	for resultEvent.Result == "" {
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
