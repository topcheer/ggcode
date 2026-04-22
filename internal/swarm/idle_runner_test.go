package swarm

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/task"
)

// countingAgent tracks how many times RunStream is called and what prompts it receives.
type countingAgent struct {
	mu      sync.Mutex
	calls   int
	prompts []string
}

func (a *countingAgent) RunStream(_ context.Context, prompt string, onEvent func(provider.StreamEvent)) error {
	a.mu.Lock()
	a.calls++
	a.prompts = append(a.prompts, prompt)
	a.mu.Unlock()

	if onEvent != nil {
		onEvent(provider.StreamEvent{Type: provider.StreamEventText, Text: "result for: " + prompt})
	}
	return nil
}

func (a *countingAgent) getCalls() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.calls
}

func (a *countingAgent) getPrompts() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]string, len(a.prompts))
	copy(out, a.prompts)
	return out
}

// newTestManager creates a minimal Manager suitable for idle runner unit tests.
func newTestManager() *Manager {
	return NewManager(config.SwarmConfig{
		MaxTeammatesPerTeam: 10,
		InboxSize:           16,
		TeammateTimeout:     5 * time.Second,
		PollInterval:        50 * time.Millisecond, // fast polling for tests
	}, nil, nil, nil)
}

func TestIdleRunner_ReceivesTask(t *testing.T) {
	agent := &countingAgent{}
	tm := &Teammate{
		ID:     "tm-1",
		Name:   "researcher",
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

	var events []Event
	var mu sync.Mutex
	onEvent := func(ev Event) {
		mu.Lock()
		events = append(events, ev)
		mu.Unlock()
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tm.ctx = ctx

	mgr := newTestManager()
	go runTeammateLoop(ctx, tm, team, agent, mgr, onEvent, 30*time.Minute)

	// Send a task
	tm.Inbox <- MailMessage{
		From:    "leader",
		Content: "Research the API",
		Type:    "task",
	}

	// Wait for agent to be called
	time.Sleep(200 * time.Millisecond)

	if agent.getCalls() != 1 {
		t.Errorf("expected 1 agent call, got %d", agent.getCalls())
	}

	// Verify events
	mu.Lock()
	defer mu.Unlock()
	hasWorking := false
	hasIdle := false
	for _, ev := range events {
		if ev.Type == "teammate_working" {
			hasWorking = true
		}
		if ev.Type == "teammate_idle" {
			hasIdle = true
		}
	}
	if !hasWorking {
		t.Error("expected teammate_working event")
	}
	if !hasIdle {
		t.Error("expected teammate_idle event after task completion")
	}
}

func TestIdleRunner_MultipleTasks(t *testing.T) {
	agent := &countingAgent{}
	tm := &Teammate{
		ID:     "tm-1",
		Name:   "coder",
		Status: TeammateIdle,
		Inbox:  make(chan MailMessage, 16),
	}
	team := &Team{
		ID:        "team-1",
		Name:      "test",
		LeaderID:  "leader",
		Teammates: map[string]*Teammate{"tm-1": tm},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tm.ctx = ctx

	mgr := newTestManager()
	go runTeammateLoop(ctx, tm, team, agent, mgr, nil, 30*time.Minute)

	// Send 3 tasks sequentially
	for i := 0; i < 3; i++ {
		tm.Inbox <- MailMessage{
			Content: "task-" + string(rune('A'+i)),
			Type:    "task",
		}
		time.Sleep(100 * time.Millisecond)
	}

	if agent.getCalls() != 3 {
		t.Errorf("expected 3 agent calls, got %d", agent.getCalls())
	}
}

func TestIdleRunner_ContextCancel(t *testing.T) {
	agent := &countingAgent{}
	tm := &Teammate{
		ID:     "tm-1",
		Name:   "coder",
		Status: TeammateIdle,
		Inbox:  make(chan MailMessage, 16),
	}
	team := &Team{
		ID:        "team-1",
		Name:      "test",
		LeaderID:  "leader",
		Teammates: map[string]*Teammate{"tm-1": tm},
	}

	ctx, cancel := context.WithCancel(context.Background())
	tm.ctx = ctx

	mgr := newTestManager()
	done := make(chan struct{})
	go func() {
		runTeammateLoop(ctx, tm, team, agent, mgr, nil, 30*time.Minute)
		close(done)
	}()

	cancel()

	select {
	case <-done:
		// expected: loop exited
	case <-time.After(1 * time.Second):
		t.Error("idle loop should exit on context cancel")
	}
}

func TestIdleRunner_ShutdownMessage(t *testing.T) {
	agent := &countingAgent{}
	tm := &Teammate{
		ID:     "tm-1",
		Name:   "coder",
		Status: TeammateIdle,
		Inbox:  make(chan MailMessage, 16),
	}
	team := &Team{
		ID:        "team-1",
		Name:      "test",
		LeaderID:  "leader",
		Teammates: map[string]*Teammate{"tm-1": tm},
	}

	ctx := context.Background()
	tm.ctx = ctx

	mgr := newTestManager()
	done := make(chan struct{})
	go func() {
		runTeammateLoop(ctx, tm, team, agent, mgr, nil, 30*time.Minute)
		close(done)
	}()

	tm.Inbox <- MailMessage{Type: "shutdown"}

	select {
	case <-done:
		// expected
	case <-time.After(1 * time.Second):
		t.Error("idle loop should exit on shutdown message")
	}

	if agent.getCalls() != 0 {
		t.Error("shutdown message should not trigger agent call")
	}
}

func TestIdleRunner_NilAgent(t *testing.T) {
	tm := &Teammate{
		ID:     "tm-1",
		Name:   "coder",
		Status: TeammateIdle,
		Inbox:  make(chan MailMessage, 16),
	}
	team := &Team{
		ID:        "team-1",
		Name:      "test",
		LeaderID:  "leader",
		Teammates: map[string]*Teammate{"tm-1": tm},
	}

	ctx := context.Background()
	tm.ctx = ctx

	events := make([]Event, 0, 2)
	onEvent := func(ev Event) { events = append(events, ev) }

	mgr := newTestManager()
	done := make(chan struct{})
	go func() {
		runTeammateLoop(ctx, tm, team, nil, mgr, onEvent, 30*time.Minute)
		close(done)
	}()

	// Task message with nil agent should emit error event but keep loop alive
	tm.Inbox <- MailMessage{Content: "do something", Type: "task"}

	time.Sleep(100 * time.Millisecond)

	// Loop should still be running (not exited)
	select {
	case <-done:
		t.Error("idle loop should NOT exit when agent is nil - it should continue")
	default:
		// expected
	}

	// Should have emitted an error event
	if len(events) == 0 || events[0].Type != "teammate_error" {
		t.Fatalf("expected teammate_error event, got %v", events)
	}

	// Now shut it down properly
	tm.Inbox <- MailMessage{Type: "shutdown"}

	select {
	case <-done:
		// expected
	case <-time.After(1 * time.Second):
		t.Error("idle loop should exit on shutdown message")
	}
}

func TestIdleRunner_InboxClose(t *testing.T) {
	tm := &Teammate{
		ID:     "tm-1",
		Name:   "coder",
		Status: TeammateIdle,
		Inbox:  make(chan MailMessage, 16),
	}
	team := &Team{
		ID:        "team-1",
		Name:      "test",
		LeaderID:  "leader",
		Teammates: map[string]*Teammate{"tm-1": tm},
	}

	ctx := context.Background()
	tm.ctx = ctx

	mgr := newTestManager()
	done := make(chan struct{})
	go func() {
		runTeammateLoop(ctx, tm, team, &countingAgent{}, mgr, nil, 30*time.Minute)
		close(done)
	}()

	close(tm.Inbox)

	select {
	case <-done:
		// expected
	case <-time.After(1 * time.Second):
		t.Error("idle loop should exit when inbox is closed")
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		input  string
		maxLen int
		want   string
	}{
		{"short", 10, "short"},
		{"exactly8", 8, "exactly8"},
		{"too long string here", 10, "too lon..."},
		{"ab", 2, "ab"},
		{"abc", 2, "ab"},
		{"中文测试字符串", 4, "中..."},
	}

	for _, tt := range tests {
		got := truncate(tt.input, tt.maxLen)
		if got != tt.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.want)
		}
	}
}

// --- Task board polling tests ---

func TestIdleRunner_PollsTaskBoardAndClaims(t *testing.T) {
	agent := &countingAgent{}
	tm := &Teammate{
		ID:     "tm-1",
		Name:   "worker",
		Status: TeammateIdle,
		Inbox:  make(chan MailMessage, 16),
	}

	taskMgr := task.NewManager()
	team := &Team{
		ID:        "team-1",
		Name:      "test",
		LeaderID:  "leader",
		Teammates: map[string]*Teammate{"tm-1": tm},
		Tasks:     taskMgr,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tm.ctx = ctx

	mgr := newTestManager()
	// Register the team in the manager so GetTaskManager works.
	mgr.mu.Lock()
	mgr.teams["team-1"] = team
	mgr.mu.Unlock()

	go runTeammateLoop(ctx, tm, team, agent, mgr, nil, 30*time.Minute)

	// Create a pending task on the board (not sending any inbox message).
	taskMgr.Create("Write tests", "Add unit tests for the module", "", nil)

	// Wait for the poller to pick it up.
	time.Sleep(300 * time.Millisecond)

	calls := agent.getCalls()
	if calls != 1 {
		t.Errorf("expected agent to be called 1 time via task board polling, got %d", calls)
	}

	// Verify the task was claimed and completed.
	tasks := taskMgr.List()
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].Status != task.StatusCompleted {
		t.Errorf("task status = %s, want completed", tasks[0].Status)
	}
	if tasks[0].Owner != "tm-1" {
		t.Errorf("task owner = %q, want tm-1", tasks[0].Owner)
	}

	// Verify the prompt contains the task subject.
	prompts := agent.getPrompts()
	if len(prompts) == 0 {
		t.Fatal("expected at least one prompt")
	}
	if !contains(prompts[0], "Write tests") {
		t.Errorf("prompt should contain task subject, got: %s", prompts[0])
	}
}

func TestIdleRunner_MultiplePendingTasks(t *testing.T) {
	agent := &countingAgent{}
	tm := &Teammate{
		ID:     "tm-1",
		Name:   "worker",
		Status: TeammateIdle,
		Inbox:  make(chan MailMessage, 16),
	}

	taskMgr := task.NewManager()
	team := &Team{
		ID:        "team-1",
		Name:      "test",
		LeaderID:  "leader",
		Teammates: map[string]*Teammate{"tm-1": tm},
		Tasks:     taskMgr,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tm.ctx = ctx

	mgr := newTestManager()
	mgr.mu.Lock()
	mgr.teams["team-1"] = team
	mgr.mu.Unlock()

	go runTeammateLoop(ctx, tm, team, agent, mgr, nil, 30*time.Minute)

	// Create 3 tasks.
	taskMgr.Create("Task A", "First", "", nil)
	taskMgr.Create("Task B", "Second", "", nil)
	taskMgr.Create("Task C", "Third", "", nil)

	// Wait for all tasks to be processed (one per poll cycle).
	time.Sleep(500 * time.Millisecond)

	calls := agent.getCalls()
	if calls != 3 {
		t.Errorf("expected 3 agent calls, got %d", calls)
	}

	// All tasks should be completed.
	for _, tk := range taskMgr.List() {
		if tk.Status != task.StatusCompleted {
			t.Errorf("task %q status = %s, want completed", tk.Subject, tk.Status)
		}
	}
}

func TestIdleRunner_NoPollWhenWorking(t *testing.T) {
	// Create a slow agent that blocks during execution.
	slowAgent := &blockingAgent{unblock: make(chan struct{})}
	tm := &Teammate{
		ID:     "tm-1",
		Name:   "worker",
		Status: TeammateIdle,
		Inbox:  make(chan MailMessage, 16),
	}

	taskMgr := task.NewManager()
	team := &Team{
		ID:        "team-1",
		Name:      "test",
		LeaderID:  "leader",
		Teammates: map[string]*Teammate{"tm-1": tm},
		Tasks:     taskMgr,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tm.ctx = ctx

	mgr := newTestManager()
	mgr.mu.Lock()
	mgr.teams["team-1"] = team
	mgr.mu.Unlock()

	go runTeammateLoop(ctx, tm, team, slowAgent, mgr, nil, 30*time.Minute)

	// Send an inbox task to make the teammate working.
	tm.Inbox <- MailMessage{Content: "inbox task", Type: "task"}
	time.Sleep(50 * time.Millisecond)

	if tm.getStatus() != TeammateWorking {
		t.Fatal("expected teammate to be working")
	}

	// Create pending tasks while working — they should NOT be claimed yet.
	taskMgr.Create("Pending A", "", "", nil)
	taskMgr.Create("Pending B", "", "", nil)

	time.Sleep(200 * time.Millisecond)

	// Still working, tasks still pending.
	for _, tk := range taskMgr.List() {
		if tk.Status != task.StatusPending {
			t.Errorf("task %q should still be pending while teammate is working, got %s", tk.Subject, tk.Status)
		}
	}

	// Now unblock the agent.
	close(slowAgent.unblock)
	time.Sleep(300 * time.Millisecond)

	// After returning to idle, the poller should pick up the pending tasks.
	picked := false
	for _, tk := range taskMgr.List() {
		if tk.Status == task.StatusCompleted {
			picked = true
		}
	}
	if !picked {
		t.Error("expected at least one task to be picked up after teammate returned to idle")
	}
}

// blockingAgent blocks RunStream until unblock channel is closed.
type blockingAgent struct {
	unblock chan struct{}
}

func (a *blockingAgent) RunStream(ctx context.Context, _ string, onEvent func(provider.StreamEvent)) error {
	if onEvent != nil {
		onEvent(provider.StreamEvent{Type: provider.StreamEventText, Text: "working..."})
	}
	select {
	case <-a.unblock:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestIdleRunner_SkipsAssignedToOtherTeammate(t *testing.T) {
	agent := &countingAgent{}
	tm := &Teammate{
		ID:     "tm-1",
		Name:   "worker",
		Status: TeammateIdle,
		Inbox:  make(chan MailMessage, 16),
	}

	taskMgr := task.NewManager()
	team := &Team{
		ID:        "team-1",
		Name:      "test",
		LeaderID:  "leader",
		Teammates: map[string]*Teammate{"tm-1": tm},
		Tasks:     taskMgr,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tm.ctx = ctx

	mgr := newTestManager()
	mgr.mu.Lock()
	mgr.teams["team-1"] = team
	mgr.mu.Unlock()

	go runTeammateLoop(ctx, tm, team, agent, mgr, nil, 30*time.Minute)

	// Create a task assigned to a DIFFERENT teammate.
	taskMgr.Create("Assigned task", "For someone else", "", map[string]string{"assignee": "tm-2"})

	// Wait for a few poll cycles.
	time.Sleep(300 * time.Millisecond)

	// The agent should NOT have been called — task is assigned to tm-2.
	if agent.getCalls() != 0 {
		t.Errorf("expected 0 agent calls (task assigned to other teammate), got %d", agent.getCalls())
	}

	// Task should still be pending.
	tasks := taskMgr.List()
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].Status != task.StatusPending {
		t.Errorf("task should still be pending, got %s", tasks[0].Status)
	}
}

func TestIdleRunner_ClaimsUnassignedTask(t *testing.T) {
	agent := &countingAgent{}
	tm := &Teammate{
		ID:     "tm-1",
		Name:   "worker",
		Status: TeammateIdle,
		Inbox:  make(chan MailMessage, 16),
	}

	taskMgr := task.NewManager()
	team := &Team{
		ID:        "team-1",
		Name:      "test",
		LeaderID:  "leader",
		Teammates: map[string]*Teammate{"tm-1": tm},
		Tasks:     taskMgr,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tm.ctx = ctx

	mgr := newTestManager()
	mgr.mu.Lock()
	mgr.teams["team-1"] = team
	mgr.mu.Unlock()

	go runTeammateLoop(ctx, tm, team, agent, mgr, nil, 30*time.Minute)

	// Create a task with NO assignee (up for grabs).
	taskMgr.Create("Open task", "Anyone can do this", "", nil)

	time.Sleep(300 * time.Millisecond)

	// Should have been claimed and completed by tm-1.
	if agent.getCalls() != 1 {
		t.Errorf("expected 1 agent call, got %d", agent.getCalls())
	}
	tasks := taskMgr.List()
	if tasks[0].Status != task.StatusCompleted {
		t.Errorf("task should be completed, got %s", tasks[0].Status)
	}
}
