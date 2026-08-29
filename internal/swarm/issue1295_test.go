package swarm

// Regression test for GitHub issue #1295: transiently-failing tasks bounced
// back to pending with NO attempt cap - a poison task (deterministically
// hitting the same 5xx/EOF/timeout) was re-claimed every tick forever,
// burning a full LLM retry+fallback chain per attempt. The runner now
// counts retry_attempts in task metadata and parks the task as completed
// (permanent_error=max_retries_exceeded) after the cap.

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/task"
)

type failingAgent struct {
	mu    sync.Mutex
	calls int
}

func (a *failingAgent) RunStream(_ context.Context, _ string, _ func(provider.StreamEvent)) error {
	a.mu.Lock()
	a.calls++
	a.mu.Unlock()
	// A network-flavored transient error: classified FailureTransient, the
	// poison-task path (NOT quota/auth which were already permanent).
	return errors.New("Post \"https://relay/v1\": EOF")
}

func (a *failingAgent) getCalls() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.calls
}

func TestIssue1295_PoisonTaskParkedAfterRetryCap(t *testing.T) {
	agent := &failingAgent{}
	tm := &Teammate{
		ID:     "tm-1",
		Name:   "worker",
		Status: TeammateIdle,
		Inbox:  make(chan MailMessage, 16),
	}
	team := &Team{
		ID:        "team-1",
		Name:      "test",
		LeaderID:  "leader",
		Teammates: map[string]*Teammate{"tm-1": tm},
		Tasks:     task.NewManager(),
	}

	tk := team.Tasks.Create("poison", "deterministically fails", "", nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tm.ctx = ctx

	mgr := newTestManager()
	// Register the team so GetTaskManager(team.ID) resolves for board claims
	// (idle runners claim board tasks, not inbox messages).
	mgr.mu.Lock()
	mgr.teams[team.ID] = team
	mgr.mu.Unlock()
	go runTeammateLoop(ctx, tm, team, agent, mgr, nil, 30*time.Minute)

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		got, ok := team.Tasks.Get(tk.ID)
		if ok && got.Status == task.StatusCompleted {
			if got.Metadata["permanent_error"] != "max_retries_exceeded" {
				t.Fatalf("completed without retry cap: metadata=%v", got.Metadata)
			}
			if got.Metadata["retry_attempts"] != "3" {
				t.Fatalf("retry_attempts metadata = %q, want 3", got.Metadata["retry_attempts"])
			}
			if calls := agent.getCalls(); calls != maxTransientTaskRetries {
				t.Fatalf("agent ran %d times, want exactly %d (then parked)", calls, maxTransientTaskRetries)
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("poison task never parked; agent calls=%d", agent.getCalls())
}
