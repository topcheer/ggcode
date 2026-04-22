package swarm

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/task"
)

// ---------------------------------------------------------------------------
// helpers reused across e2e tests
// ---------------------------------------------------------------------------

// newE2EManager builds a Manager wired with a mock agent factory.
func newE2EManager() *Manager {
	return NewManager(
		config.SwarmConfig{MaxTeammatesPerTeam: 10, InboxSize: 64, TeammateTimeout: 5 * time.Second},
		nil,
		func(_ provider.Provider, _ interface{}, _ string, _ int) AgentRunner {
			return &mockAgentRunner{
				runStreamFn: func(_ context.Context, prompt string, onEvent func(provider.StreamEvent)) error {
					if onEvent != nil {
						onEvent(provider.StreamEvent{Type: provider.StreamEventText, Text: "done: " + prompt})
					}
					return nil
				},
			}
		},
		func(_ []string) interface{} { return nil },
	)
}

// collectEvents waits for at least minCount events of the given type within
// the deadline and returns them.
func collectEvents(t *testing.T, m *Manager, eventType string, minCount int, timeout time.Duration) []Event {
	t.Helper()
	deadline := time.After(timeout)
	var collected []Event
	var mu sync.Mutex

	// We intercept events by wrapping the existing callback (if any).
	origFn := m.onUpdate
	var wrapped func(Event)
	wrapped = func(ev Event) {
		mu.Lock()
		if ev.Type == eventType {
			collected = append(collected, ev)
		}
		mu.Unlock()
		if origFn != nil {
			origFn(ev)
		}
	}
	m.SetOnUpdate(wrapped)

	for {
		mu.Lock()
		n := len(collected)
		mu.Unlock()
		if n >= minCount {
			return collected
		}
		select {
		case <-deadline:
			mu.Lock()
			t.Fatalf("timed out waiting for %d %q events, got %d", minCount, eventType, len(collected))
			mu.Unlock()
			return nil
		default:
			time.Sleep(20 * time.Millisecond)
		}
	}
}

// ---------------------------------------------------------------------------
// E2E: full lifecycle – create team → spawn → task → claim → complete
// ---------------------------------------------------------------------------

func TestE2E_FullTeamLifecycle(t *testing.T) {
	m := newE2EManager()
	defer m.Shutdown()

	// 1. Create team.
	teamSnap := m.CreateTeam("lifecycle-team", "leader-1")
	if teamSnap.ID == "" {
		t.Fatal("expected non-empty team ID")
	}

	// 2. Spawn multiple teammates.
	tm1, err := m.SpawnTeammate(teamSnap.ID, "researcher", "32", nil)
	if err != nil {
		t.Fatalf("spawn researcher: %v", err)
	}
	tm2, err := m.SpawnTeammate(teamSnap.ID, "coder", "33", nil)
	if err != nil {
		t.Fatalf("spawn coder: %v", err)
	}
	time.Sleep(50 * time.Millisecond) // let idle loops start

	// 3. Get task manager and create tasks on the board.
	tmMgr, err := m.EnsureTaskManager(teamSnap.ID)
	if err != nil {
		t.Fatalf("ensure task manager: %v", err)
	}

	task1 := tmMgr.Create("Research API", "Investigate REST endpoints", "Researching API", map[string]string{"priority": "high"})
	task2 := tmMgr.Create("Implement feature", "Write the code", "Implementing feature", map[string]string{"priority": "medium"})

	if task1.Status != task.StatusPending || task2.Status != task.StatusPending {
		t.Fatalf("expected both tasks pending, got %s and %s", task1.Status, task2.Status)
	}

	// 4. Claim (start) tasks via status update.
	inProg := task.StatusInProgress
	task1, err = tmMgr.Update(task1.ID, task.UpdateOptions{Status: &inProg, Owner: &tm1.ID})
	if err != nil {
		t.Fatalf("claim task1: %v", err)
	}
	task2, err = tmMgr.Update(task2.ID, task.UpdateOptions{Status: &inProg, Owner: &tm2.ID})
	if err != nil {
		t.Fatalf("claim task2: %v", err)
	}

	// 5. Complete tasks.
	completed := task.StatusCompleted
	task1, err = tmMgr.Update(task1.ID, task.UpdateOptions{Status: &completed})
	if err != nil {
		t.Fatalf("complete task1: %v", err)
	}
	task2, err = tmMgr.Update(task2.ID, task.UpdateOptions{Status: &completed})
	if err != nil {
		t.Fatalf("complete task2: %v", err)
	}

	// 6. Verify final state.
	got1, ok := tmMgr.Get(task1.ID)
	if !ok || got1.Status != task.StatusCompleted {
		t.Errorf("task1 not completed: %s", got1.Status)
	}
	got2, ok := tmMgr.Get(task2.ID)
	if !ok || got2.Status != task.StatusCompleted {
		t.Errorf("task2 not completed: %s", got2.Status)
	}

	// 7. Verify team snapshot includes the tasks.
	snap, _ := m.GetTeam(teamSnap.ID)
	if snap.TaskCount != 2 {
		t.Errorf("expected 2 tasks in team snapshot, got %d", snap.TaskCount)
	}
	if len(snap.Teammates) != 2 {
		t.Errorf("expected 2 teammates, got %d", len(snap.Teammates))
	}
}

// ---------------------------------------------------------------------------
// E2E: teammate idle → working status transition via real task message
// ---------------------------------------------------------------------------

func TestE2E_TeammateIdleToWorkingTransition(t *testing.T) {
	m := newE2EManager()
	defer m.Shutdown()

	team := m.CreateTeam("status-team", "leader-1")

	tmSnap, err := m.SpawnTeammate(team.ID, "worker", "32", nil)
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	// Teammate should start idle.
	snap, _ := m.GetTeam(team.ID)
	found := false
	for _, tm := range snap.Teammates {
		if tm.ID == tmSnap.ID && tm.Status == TeammateIdle {
			found = true
		}
	}
	if !found {
		t.Fatal("teammate should be idle initially")
	}

	// Capture events for working transition.
	var workingEvent atomic.Value
	m.SetOnUpdate(func(ev Event) {
		if ev.Type == "teammate_working" && ev.TeammateID == tmSnap.ID {
			workingEvent.Store(ev)
		}
	})

	// Send a real task message — the idle loop should pick it up and go working.
	err = m.SendToTeammate(team.ID, tmSnap.ID, MailMessage{
		From:    "leader-1",
		Content: "Do important work",
		Type:    "task",
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	// Wait for working event.
	deadline := time.After(3 * time.Second)
	for workingEvent.Load() == nil {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for teammate_working event")
		default:
			time.Sleep(20 * time.Millisecond)
		}
	}

	// Wait for teammate to return to idle after completing the task.
	deadline = time.After(3 * time.Second)
	for {
		snap, _ = m.GetTeam(team.ID)
		for _, tm := range snap.Teammates {
			if tm.ID == tmSnap.ID && tm.Status == TeammateIdle {
				return // success
			}
		}
		select {
		case <-deadline:
			t.Fatal("teammate did not return to idle after task")
		default:
			time.Sleep(30 * time.Millisecond)
		}
	}
}

// ---------------------------------------------------------------------------
// E2E: broadcast message to all teammates
// ---------------------------------------------------------------------------

func TestE2E_BroadcastToAllTeammates(t *testing.T) {
	m := newE2EManager()
	defer m.Shutdown()

	team := m.CreateTeam("broadcast-team", "leader-1")

	var tmIDs []string
	for i := 0; i < 4; i++ {
		snap, err := m.SpawnTeammate(team.ID, "tm-"+string(rune('A'+i)), "", nil)
		if err != nil {
			t.Fatalf("spawn %d: %v", i, err)
		}
		tmIDs = append(tmIDs, snap.ID)
	}
	time.Sleep(80 * time.Millisecond)

	// Track how many teammates receive the broadcast.
	var receivedCount atomic.Int32
	// Wire event listener — each teammate will emit "teammate_working" then "teammate_idle".
	m.SetOnUpdate(func(ev Event) {
		if ev.Type == "teammate_idle" && ev.Result != "" {
			receivedCount.Add(1)
		}
	})

	msg := MailMessage{
		From:    "leader-1",
		Content: "All hands meeting",
		Type:    "message",
	}
	sent := m.BroadcastToTeam(team.ID, msg)
	if len(sent) != 4 {
		t.Fatalf("expected 4 deliveries, got %d", len(sent))
	}

	// Wait for all teammates to process the message.
	deadline := time.After(5 * time.Second)
	for receivedCount.Load() < 4 {
		select {
		case <-deadline:
			t.Fatalf("timed out: only %d/4 teammates processed broadcast", receivedCount.Load())
		default:
			time.Sleep(30 * time.Millisecond)
		}
	}
}

// ---------------------------------------------------------------------------
// E2E: teammate shutdown and cleanup
// ---------------------------------------------------------------------------

func TestE2E_TeammateShutdownAndCleanup(t *testing.T) {
	m := newE2EManager()
	defer m.Shutdown()

	team := m.CreateTeam("shutdown-team", "leader-1")

	tm1, _ := m.SpawnTeammate(team.ID, "worker-1", "", nil)
	tm2, _ := m.SpawnTeammate(team.ID, "worker-2", "", nil)
	time.Sleep(50 * time.Millisecond)

	// Shut down one teammate.
	err := m.ShutdownTeammate(team.ID, tm1.ID)
	if err != nil {
		t.Fatalf("shutdown tm1: %v", err)
	}

	// Verify tm1 is shutting_down.
	snap, _ := m.GetTeam(team.ID)
	for _, tm := range snap.Teammates {
		if tm.ID == tm1.ID && tm.Status != TeammateShuttingDown {
			t.Errorf("expected tm1 shutting_down, got %s", tm.Status)
		}
	}

	// tm2 should still be idle.
	for _, tm := range snap.Teammates {
		if tm.ID == tm2.ID && tm.Status != TeammateIdle {
			t.Errorf("expected tm2 idle, got %s", tm.Status)
		}
	}

	// tm2 should still accept messages.
	err = m.SendToTeammate(team.ID, tm2.ID, MailMessage{
		From:    "leader",
		Content: "Still alive?",
		Type:    "task",
	})
	if err != nil {
		t.Fatalf("tm2 should still accept messages: %v", err)
	}
}

// ---------------------------------------------------------------------------
// E2E: concurrent task claim – two teammates race for same task
// ---------------------------------------------------------------------------

func TestE2E_ConcurrentTaskClaim(t *testing.T) {
	m := newE2EManager()
	defer m.Shutdown()

	team := m.CreateTeam("race-team", "leader-1")

	tm1, _ := m.SpawnTeammate(team.ID, "claimant-1", "", nil)
	tm2, _ := m.SpawnTeammate(team.ID, "claimant-2", "", nil)
	time.Sleep(50 * time.Millisecond)

	tmMgr, err := m.EnsureTaskManager(team.ID)
	if err != nil {
		t.Fatalf("task manager: %v", err)
	}

	// Create a single task for two claimants to race over.
	tk := tmMgr.Create("Race task", "Only one should claim this", "", nil)

	// Use ExpectedStatus to implement first-writer-wins semantics.
	var (
		wg         sync.WaitGroup
		winner     atomic.Value
		loserCount atomic.Int32
	)

	inProg := task.StatusInProgress
	pending := task.StatusPending

	claim := func(ownerID string) {
		defer wg.Done()
		updated, err := tmMgr.Update(tk.ID, task.UpdateOptions{
			ExpectedStatus: &pending,
			Status:         &inProg,
			Owner:          &ownerID,
		})
		if err != nil {
			loserCount.Add(1)
			return
		}
		winner.Store(updated.Owner)
	}

	wg.Add(2)
	go claim(tm1.ID)
	go claim(tm2.ID)
	wg.Wait()

	w := winner.Load()
	if w == nil {
		t.Fatal("expected one claimant to win the race")
	}
	if loserCount.Load() != 1 {
		t.Errorf("expected exactly 1 loser, got %d", loserCount.Load())
	}

	// Verify only one owner is set.
	got, _ := tmMgr.Get(tk.ID)
	if got.Status != task.StatusInProgress {
		t.Errorf("expected in_progress, got %s", got.Status)
	}
	if got.Owner == "" {
		t.Error("expected non-empty owner")
	}
}

// ---------------------------------------------------------------------------
// E2E: team delete cleans up all teammates
// ---------------------------------------------------------------------------

func TestE2E_TeamDeleteCleansUp(t *testing.T) {
	m := newE2EManager()

	team := m.CreateTeam("doomed-team", "leader-1")
	m.SpawnTeammate(team.ID, "tm-1", "", nil)
	m.SpawnTeammate(team.ID, "tm-2", "", nil)
	m.SpawnTeammate(team.ID, "tm-3", "", nil)
	time.Sleep(50 * time.Millisecond)

	err := m.DeleteTeam(team.ID)
	if err != nil {
		t.Fatalf("delete team: %v", err)
	}

	// Team should no longer exist.
	_, ok := m.GetTeam(team.ID)
	if ok {
		t.Error("team should be gone after deletion")
	}

	// Root context should still be alive (only the team is deleted, not the manager).
	select {
	case <-m.RootContext().Done():
		t.Error("root context should NOT be cancelled by team deletion")
	default:
	}

	m.Shutdown()
}

// ---------------------------------------------------------------------------
// E2E: event emission tracking through full lifecycle
// ---------------------------------------------------------------------------

func TestE2E_EventTracking(t *testing.T) {
	m := newE2EManager()
	defer m.Shutdown()

	var events []Event
	var mu sync.Mutex
	m.SetOnUpdate(func(ev Event) {
		mu.Lock()
		events = append(events, ev)
		mu.Unlock()
	})

	team := m.CreateTeam("event-team", "leader-1")
	tmSnap, _ := m.SpawnTeammate(team.ID, "worker", "", nil)
	time.Sleep(50 * time.Millisecond)

	// Send a task.
	m.SendToTeammate(team.ID, tmSnap.ID, MailMessage{Content: "work", Type: "task"})
	time.Sleep(300 * time.Millisecond)

	// Shutdown teammate.
	m.ShutdownTeammate(team.ID, tmSnap.ID)

	// Delete team.
	m.DeleteTeam(team.ID)

	// Allow events to propagate.
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	expectedTypes := map[string]bool{
		"team_created":      false,
		"teammate_spawned":  false,
		"teammate_working":  false,
		"teammate_idle":     false,
		"teammate_shutdown": false,
		"team_deleted":      false,
	}

	for _, ev := range events {
		if _, ok := expectedTypes[ev.Type]; ok {
			expectedTypes[ev.Type] = true
		}
	}

	for typ, seen := range expectedTypes {
		if !seen {
			t.Errorf("expected event type %q not seen", typ)
		}
	}
}

// ---------------------------------------------------------------------------
// E2E: message reply channel (request-response pattern)
// ---------------------------------------------------------------------------

func TestE2E_MessageReplyChannel(t *testing.T) {
	m := newE2EManager()
	defer m.Shutdown()

	team := m.CreateTeam("reply-team", "leader-1")
	tmSnap, _ := m.SpawnTeammate(team.ID, "worker", "", nil)
	time.Sleep(50 * time.Millisecond)

	replyCh := make(chan TaskResult, 1)
	err := m.SendToTeammate(team.ID, tmSnap.ID, MailMessage{
		From:    "leader",
		Content: "What is 2+2?",
		Type:    "task",
		ReplyTo: replyCh,
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	select {
	case result := <-replyCh:
		if result.Error != nil {
			t.Errorf("unexpected error: %v", result.Error)
		}
		if result.Output == "" {
			t.Error("expected non-empty output")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for reply")
	}
}

// ---------------------------------------------------------------------------
// E2E: multiple teams operate independently
// ---------------------------------------------------------------------------

func TestE2E_MultipleTeamsIndependent(t *testing.T) {
	m := newE2EManager()
	defer m.Shutdown()

	team1 := m.CreateTeam("team-alpha", "leader-a")
	team2 := m.CreateTeam("team-beta", "leader-b")

	tmA, _ := m.SpawnTeammate(team1.ID, "alpha-worker", "", nil)
	tmB, _ := m.SpawnTeammate(team2.ID, "beta-worker", "", nil)
	time.Sleep(50 * time.Millisecond)

	// Cross-team message should fail.
	err := m.SendToTeammate(team2.ID, tmA.ID, MailMessage{Content: "hello", Type: "task"})
	if err == nil {
		t.Error("expected error when sending to teammate in different team")
	}

	// Valid intra-team messages should work.
	err = m.SendToTeammate(team1.ID, tmA.ID, MailMessage{Content: "work", Type: "task"})
	if err != nil {
		t.Fatalf("send to alpha worker: %v", err)
	}
	err = m.SendToTeammate(team2.ID, tmB.ID, MailMessage{Content: "work", Type: "task"})
	if err != nil {
		t.Fatalf("send to beta worker: %v", err)
	}

	// Deleting team1 should not affect team2.
	m.DeleteTeam(team1.ID)
	_, ok := m.GetTeam(team2.ID)
	if !ok {
		t.Error("team2 should still exist after team1 deletion")
	}
}

// ---------------------------------------------------------------------------
// E2E: inbox full behaviour
// ---------------------------------------------------------------------------

func TestE2E_InboxFull(t *testing.T) {
	cfg := config.SwarmConfig{
		MaxTeammatesPerTeam: 2,
		InboxSize:           2, // tiny inbox
		TeammateTimeout:     5 * time.Second,
	}
	// Use a blocking agent so the teammate stays busy and can't drain inbox.
	m := NewManager(cfg, nil,
		func(_ provider.Provider, _ interface{}, _ string, _ int) AgentRunner {
			return &mockAgentRunner{
				runStreamFn: func(ctx context.Context, _ string, onEvent func(provider.StreamEvent)) error {
					// Block until context cancelled.
					<-ctx.Done()
					return nil
				},
			}
		},
		func(_ []string) interface{} { return nil },
	)
	defer m.Shutdown()

	team := m.CreateTeam("inbox-team", "leader-1")
	tmSnap, _ := m.SpawnTeammate(team.ID, "slow-worker", "", nil)
	time.Sleep(50 * time.Millisecond)

	// First message starts processing (teammate goes working).
	err := m.SendToTeammate(team.ID, tmSnap.ID, MailMessage{Content: "task-1", Type: "task"})
	if err != nil {
		t.Fatalf("first send: %v", err)
	}
	time.Sleep(50 * time.Millisecond) // let it pick up

	// Fill the remaining inbox.
	err = m.SendToTeammate(team.ID, tmSnap.ID, MailMessage{Content: "task-2", Type: "task"})
	if err != nil {
		t.Fatalf("second send: %v", err)
	}
	err = m.SendToTeammate(team.ID, tmSnap.ID, MailMessage{Content: "task-3", Type: "task"})
	if err != nil {
		t.Fatalf("third send: %v", err)
	}

	// The fourth should overflow.
	err = m.SendToTeammate(team.ID, tmSnap.ID, MailMessage{Content: "task-4", Type: "task"})
	if err == nil {
		t.Error("expected inbox full error")
	}
}
