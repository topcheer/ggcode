package swarm

import (
	"context"
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
