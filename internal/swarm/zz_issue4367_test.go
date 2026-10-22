package swarm

// #4367: a panic between the successful claim and the (previously late)
// CurrentTaskID write made the #1688 rollback a no-op — the task stayed
// in_progress forever (claim only scans pending) and BlockedBy dependents
// deadlocked. The fix records CurrentTaskID immediately after the claim,
// before any panic-capable call (probe consumption, onEvent callback,
// prompt building). This test panics inside the first onEvent callback
// fired right after a claim and asserts the task is rolled back to pending.

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/task"
)

func TestIssue4367PanicInPostClaimOnEventRollsTaskBack(t *testing.T) {
	m := NewManager(
		config.SwarmConfig{MaxTeammatesPerTeam: 4, InboxSize: 16, TeammateTimeout: 5 * time.Second},
		nil,
		func(_ provider.Provider, _ interface{}, _ string, _ int) AgentRunner {
			return &mockAgentRunner{
				runStreamFn: func(_ context.Context, _ string, onEvent func(provider.StreamEvent)) error {
					if onEvent != nil {
						onEvent(provider.StreamEvent{Type: provider.StreamEventText, Text: "done"})
					}
					return nil
				},
			}
		},
		func(_ []string) interface{} { return nil },
	)
	defer m.Shutdown()

	// The first team_board_updated fires immediately after the claim succeeds
	// — BEFORE the (old) CurrentTaskID write. Panicking there is exactly the
	// #4367 window.
	var panicked int32
	m.SetOnUpdate(func(ev Event) {
		if ev.Type == "team_board_updated" && atomic.CompareAndSwapInt32(&panicked, 0, 1) {
			panic("issue-4367: onEvent panic inside the post-claim window")
		}
	})

	teamSnap := m.CreateTeam("issue-4367-team", "leader-1")
	if _, err := m.SpawnTeammate(teamSnap.ID, "worker", "32", nil); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	time.Sleep(50 * time.Millisecond) // let the idle loop start

	tmMgr, err := m.EnsureTaskManager(teamSnap.ID)
	if err != nil {
		t.Fatalf("ensure task manager: %v", err)
	}
	tk := tmMgr.Create("panic window task", "claimed then onEvent panics", "", nil)

	// The panic recovery should roll the claimed task BACK to pending.
	deadline := time.Now().Add(5 * time.Second)
	for {
		cur, ok := tmMgr.Get(tk.ID)
		if ok && cur.Status == task.StatusPending && atomic.LoadInt32(&panicked) == 1 {
			break // rolled back after the panic — the fix works
		}
		if time.Now().After(deadline) {
			cur, _ := tmMgr.Get(tk.ID)
			got := string(cur.Status)
			t.Fatalf("task not rolled back to pending after post-claim panic (panicked=%d, status=%q)",
				atomic.LoadInt32(&panicked), got)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
