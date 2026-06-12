package swarm

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/task"
)

func TestTeam_Snapshot(t *testing.T) {
	team := &Team{
		ID:        "team-1",
		Name:      "test-team",
		LeaderID:  "leader",
		Teammates: make(map[string]*Teammate),
		Tasks:     task.NewManager(),
		CreatedAt: time.Now(),
	}

	// Add a teammate
	tm := &Teammate{
		ID:        "tm-1",
		Name:      "researcher",
		Color:     "32",
		Status:    TeammateIdle,
		Inbox:     make(chan MailMessage, 16),
		CreatedAt: time.Now(),
	}
	team.Teammates["tm-1"] = tm

	snap := team.snapshot()
	if snap.ID != "team-1" {
		t.Errorf("expected ID team-1, got %s", snap.ID)
	}
	if snap.Name != "test-team" {
		t.Errorf("expected Name test-team, got %s", snap.Name)
	}
	if snap.LeaderID != "leader" {
		t.Errorf("expected LeaderID leader, got %s", snap.LeaderID)
	}
	if len(snap.Teammates) != 1 {
		t.Fatalf("expected 1 teammate, got %d", len(snap.Teammates))
	}
	if snap.Teammates[0].Name != "researcher" {
		t.Errorf("expected teammate name researcher, got %s", snap.Teammates[0].Name)
	}
	if snap.Teammates[0].Status != TeammateIdle {
		t.Errorf("expected teammate status idle, got %s", snap.Teammates[0].Status)
	}
}

func TestTeammate_StatusTransition(t *testing.T) {
	tm := &Teammate{
		ID:     "tm-1",
		Name:   "coder",
		Status: TeammateIdle,
		Inbox:  make(chan MailMessage, 16),
	}

	// idle -> working
	tm.setStatus(TeammateWorking)
	if tm.getStatus() != TeammateWorking {
		t.Errorf("expected working, got %s", tm.getStatus())
	}

	// working -> idle
	tm.setStatus(TeammateIdle)
	if tm.getStatus() != TeammateIdle {
		t.Errorf("expected idle, got %s", tm.getStatus())
	}

	// idle -> shutting_down
	tm.setStatus(TeammateShuttingDown)
	if tm.getStatus() != TeammateShuttingDown {
		t.Errorf("expected shutting_down, got %s", tm.getStatus())
	}
}

func TestTeammate_SetCurrentTask(t *testing.T) {
	tm := &Teammate{
		ID:     "tm-1",
		Name:   "researcher",
		Status: TeammateIdle,
		Inbox:  make(chan MailMessage, 16),
	}

	tm.setCurrentTask("investigate API")
	if tm.CurrentTask != "investigate API" {
		t.Errorf("expected 'investigate API', got %s", tm.CurrentTask)
	}

	snap := tm.snapshot()
	if snap.CurrentTask != "investigate API" {
		t.Errorf("snapshot: expected 'investigate API', got %s", snap.CurrentTask)
	}
}

func TestTeam_GetTeammate(t *testing.T) {
	team := &Team{
		ID:        "team-1",
		Name:      "test",
		LeaderID:  "leader",
		Teammates: make(map[string]*Teammate),
		CreatedAt: time.Now(),
	}

	// Not found
	_, ok := team.getTeammate("tm-999")
	if ok {
		t.Error("expected teammate not found")
	}

	// Add and find
	tm := &Teammate{ID: "tm-1", Name: "coder", Inbox: make(chan MailMessage, 16)}
	team.Teammates["tm-1"] = tm

	found, ok := team.getTeammate("tm-1")
	if !ok {
		t.Fatal("expected teammate found")
	}
	if found.Name != "coder" {
		t.Errorf("expected coder, got %s", found.Name)
	}
}

func TestTeam_ListTeammates(t *testing.T) {
	team := &Team{
		ID:        "team-1",
		Name:      "test",
		LeaderID:  "leader",
		Teammates: make(map[string]*Teammate),
		CreatedAt: time.Now(),
	}

	// Empty
	list := team.listTeammates()
	if len(list) != 0 {
		t.Errorf("expected empty list, got %d", len(list))
	}

	// Add 3 teammates
	for i := 0; i < 3; i++ {
		id := string(rune('a' + i))
		team.Teammates[id] = &Teammate{ID: id, Inbox: make(chan MailMessage, 16)}
	}

	list = team.listTeammates()
	if len(list) != 3 {
		t.Errorf("expected 3 teammates, got %d", len(list))
	}
}

func TestMailMessage_Delivery(t *testing.T) {
	inbox := make(chan MailMessage, 16)

	msg := MailMessage{
		From:    "leader",
		Content: "Do the thing",
		Summary: "task assignment",
		Type:    "task",
	}

	select {
	case inbox <- msg:
	default:
		t.Fatal("inbox should accept message")
	}

	select {
	case received := <-inbox:
		if received.From != "leader" {
			t.Errorf("expected from leader, got %s", received.From)
		}
		if received.Content != "Do the thing" {
			t.Errorf("expected content 'Do the thing', got %s", received.Content)
		}
		if received.Type != "task" {
			t.Errorf("expected type task, got %s", received.Type)
		}
	default:
		t.Fatal("inbox should have message")
	}
}

func TestTeam_Snapshot_NilTasks(t *testing.T) {
	team := &Team{
		ID:        "team-1",
		Name:      "test",
		LeaderID:  "leader",
		Teammates: make(map[string]*Teammate),
		Tasks:     nil, // no task manager yet
		CreatedAt: time.Now(),
	}

	snap := team.snapshot()
	if snap.TaskCount != 0 {
		t.Errorf("expected 0 tasks, got %d", snap.TaskCount)
	}
}

func TestTeammate_Cancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	tm := &Teammate{
		ID:     "tm-1",
		Name:   "coder",
		Status: TeammateIdle,
		Inbox:  make(chan MailMessage, 16),
		ctx:    ctx,
		cancel: cancel,
	}

	// Cancel should work without panic
	cancel()

	select {
	case <-tm.ctx.Done():
		// expected
	default:
		t.Error("context should be done after cancel")
	}
}

func TestTeammate_statusInfo(t *testing.T) {
	tm := &Teammate{
		ID:     "tm-42",
		Name:   "coder",
		Status: TeammateWorking,
		Inbox:  make(chan MailMessage, 16),
	}

	info := tm.statusInfo()
	if info.ID != "tm-42" {
		t.Errorf("expected ID tm-42, got %s", info.ID)
	}
	if info.Name != "coder" {
		t.Errorf("expected Name coder, got %s", info.Name)
	}
	if info.Status != TeammateWorking {
		t.Errorf("expected Status working, got %s", info.Status)
	}
}

func TestTeammate_statusInfo_ReflectsStatusChange(t *testing.T) {
	tm := &Teammate{
		ID:     "tm-1",
		Name:   "researcher",
		Status: TeammateIdle,
		Inbox:  make(chan MailMessage, 16),
	}

	info := tm.statusInfo()
	if info.Status != TeammateIdle {
		t.Fatalf("expected idle, got %s", info.Status)
	}

	tm.setStatus(TeammateWorking)
	info = tm.statusInfo()
	if info.Status != TeammateWorking {
		t.Errorf("expected working after setStatus, got %s", info.Status)
	}
}

func TestTeam_teammateStatuses(t *testing.T) {
	team := &Team{
		ID:        "team-1",
		Name:      "test",
		LeaderID:  "leader",
		Teammates: make(map[string]*Teammate),
		CreatedAt: time.Now(),
	}

	// Empty team
	statuses := team.teammateStatuses()
	if len(statuses) != 0 {
		t.Fatalf("expected 0 statuses, got %d", len(statuses))
	}

	// Add teammates out of order
	team.Teammates["tm-3"] = &Teammate{ID: "tm-3", Name: "c", Status: TeammateIdle, Inbox: make(chan MailMessage, 16)}
	team.Teammates["tm-1"] = &Teammate{ID: "tm-1", Name: "a", Status: TeammateWorking, Inbox: make(chan MailMessage, 16)}
	team.Teammates["tm-2"] = &Teammate{ID: "tm-2", Name: "b", Status: TeammateShuttingDown, Inbox: make(chan MailMessage, 16)}

	statuses = team.teammateStatuses()
	if len(statuses) != 3 {
		t.Fatalf("expected 3 statuses, got %d", len(statuses))
	}

	// Verify sorted by ID
	if statuses[0].ID != "tm-1" {
		t.Errorf("expected first tm-1, got %s", statuses[0].ID)
	}
	if statuses[1].ID != "tm-2" {
		t.Errorf("expected second tm-2, got %s", statuses[1].ID)
	}
	if statuses[2].ID != "tm-3" {
		t.Errorf("expected third tm-3, got %s", statuses[2].ID)
	}

	// Verify statuses
	if statuses[0].Status != TeammateWorking {
		t.Errorf("tm-1: expected working, got %s", statuses[0].Status)
	}
	if statuses[1].Status != TeammateShuttingDown {
		t.Errorf("tm-2: expected shutting_down, got %s", statuses[1].Status)
	}
	if statuses[2].Status != TeammateIdle {
		t.Errorf("tm-3: expected idle, got %s", statuses[2].Status)
	}
}

func TestTeammateStatusInfo_ConcurrentWithAppendEvent(t *testing.T) {
	// This test verifies that statusInfo() does not deadlock when called
	// concurrently with appendEvent(). Both acquire Teammate.mu — if statusInfo()
	// held the lock too long (like snapshot() does while copying 200 events),
	// the appendEvent goroutine would be blocked, causing this test to timeout.
	tm := &Teammate{
		ID:     "tm-1",
		Name:   "coder",
		Status: TeammateWorking,
		Inbox:  make(chan MailMessage, 16),
	}

	var wg sync.WaitGroup
	done := make(chan struct{})

	// Writer: append events rapidly (simulates streaming tokens)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			tm.appendEvent(TeammateEvent{Type: TeammateEventText, Text: "chunk"})
		}
	}()

	// Reader: call statusInfo rapidly (simulates TUI strip refresh)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			info := tm.statusInfo()
			if info.ID != "tm-1" {
				t.Errorf("unexpected ID: %s", info.ID)
			}
		}
	}()

	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// success — no deadlock
	case <-time.After(10 * time.Second):
		t.Fatal("deadlock detected: statusInfo and appendEvent did not complete within 10s")
	}
}

func TestTeammate_EventsSince(t *testing.T) {
	tm := &Teammate{
		ID:     "tm-1",
		Name:   "coder",
		Status: TeammateWorking,
		Inbox:  make(chan MailMessage, 16),
	}

	// No events yet
	events, total := tm.EventsSince(0)
	if total != 0 {
		t.Fatalf("expected 0 total, got %d", total)
	}
	if len(events) != 0 {
		t.Fatalf("expected 0 events, got %d", len(events))
	}

	// Add some events
	for i := 0; i < 5; i++ {
		tm.appendEvent(TeammateEvent{Type: TeammateEventText, Text: fmt.Sprintf("chunk-%d", i)})
	}

	// Get all events
	events, total = tm.EventsSince(0)
	if total != 5 {
		t.Fatalf("expected 5 total, got %d", total)
	}
	if len(events) != 5 {
		t.Fatalf("expected 5 events, got %d", len(events))
	}

	// Get incremental events (from index 3)
	events, total = tm.EventsSince(3)
	if total != 5 {
		t.Fatalf("expected 5 total, got %d", total)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 incremental events, got %d", len(events))
	}
	if events[0].Text != "chunk-3" {
		t.Errorf("expected chunk-3, got %s", events[0].Text)
	}
	if events[1].Text != "chunk-4" {
		t.Errorf("expected chunk-4, got %s", events[1].Text)
	}

	// fromIdx >= total returns empty
	events, total = tm.EventsSince(10)
	if total != 5 {
		t.Fatalf("expected 5 total, got %d", total)
	}
	if len(events) != 0 {
		t.Fatalf("expected 0 events for fromIdx >= total, got %d", len(events))
	}
}
