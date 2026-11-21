package swarm

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/task"
)

// Direct-delivered tasks (swarm_task_create with assignee) must be marked
// completed on the board by the runner when the work finishes. Before the
// fix the direct path only replied to the leader; the board stayed pending
// forever and every restart re-claimed and re-ran the finished task
// ("teammate shows working right after restore, redoing finished work").

func TestDirectTaskCompletionMarksBoard(t *testing.T) {
	mgr := newTestManager()
	snap := mgr.CreateTeam("direct-board", "")
	team := &Team{ID: snap.ID, Name: "direct-board", LeaderID: "leader"}
	board, err := mgr.EnsureTaskManager(team.ID)
	if err != nil {
		t.Fatalf("ensure board: %v", err)
	}
	tk := board.Create("collect", "desc", "", map[string]string{"assignee": "tm-7"})
	if tk.Status != task.StatusPending {
		t.Fatalf("expected fresh task pending, got %s", tk.Status)
	}

	agent := &countingAgent{}
	tm := &Teammate{ID: "tm-7", Name: "ops", Status: TeammateIdle, Inbox: make(chan MailMessage, 4)}
	msg := MailMessage{From: "leader", Type: "task", TaskID: tk.ID, Content: "collect the things"}

	handleMessage(context.Background(), tm, team, agent, mgr, nil, time.Minute, msg)

	after, ok := board.Get(tk.ID)
	if !ok {
		t.Fatal("task vanished from board")
	}
	if after.Status != task.StatusCompleted {
		t.Fatalf("direct-delivered task not marked completed after runner finished: status=%s (the board-write was skipped - restart would redo it)", after.Status)
	}
	if tm.getStatus() != TeammateIdle {
		t.Fatalf("teammate should return to idle, got %s", tm.getStatus())
	}
}

// Stale-results regression: the direct-delivery completion path must archive
// the task result into the board metadata (persistTaskResult), matching the
// claim path. Before the fix only the board STATUS was flipped; the newest
// board-archived result never changed, so GetTeammateTaskResult kept serving
// whichever earlier task happened to have an archived result - every later
// direct-delivered task reported that old output forever.
func TestDirectTaskCompletionPersistsResult(t *testing.T) {
	mgr := newTestManager()
	snap := mgr.CreateTeam("direct-persist", "")
	team := &Team{ID: snap.ID, Name: "direct-persist", LeaderID: "leader"}
	board, err := mgr.EnsureTaskManager(team.ID)
	if err != nil {
		t.Fatalf("ensure board: %v", err)
	}

	agent := &countingAgent{}
	tm := &Teammate{ID: "tm-7", Name: "ops", Status: TeammateIdle, Inbox: make(chan MailMessage, 4)}

	first := board.Create("first-pass", "desc", "", map[string]string{"assignee": "tm-7"})
	handleMessage(context.Background(), tm, team, agent, mgr, nil, time.Minute,
		MailMessage{From: "leader", Type: "task", TaskID: first.ID, Content: "do first-pass"})

	second := board.Create("second-pass", "desc", "", map[string]string{"assignee": "tm-7"})
	handleMessage(context.Background(), tm, team, agent, mgr, nil, time.Minute,
		MailMessage{From: "leader", Type: "task", TaskID: second.ID, Content: "do second-pass"})

	taskID, result, ok := mgr.GetTeammateTaskResult(team.ID, "tm-7")
	if !ok {
		t.Fatal("no board-archived result for tm-7 (direct-delivery path skipped persistTaskResult)")
	}
	if taskID != second.ID {
		t.Fatalf("expected newest archived task %s, got %s", second.ID, taskID)
	}
	if !strings.Contains(result, "second-pass") {
		t.Fatalf("archived result should carry the second task's output, got: %.200s", result)
	}
}
