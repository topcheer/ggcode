package swarm

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/task"
	"github.com/topcheer/ggcode/internal/util"
)

type countingAgent struct {
	mu      sync.Mutex
	calls   int
	prompts []string
}

func (a *countingAgent) RunStream(_ context.Context, prompt string, onEvent func(provider.StreamEvent), origin ...string) error {
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

func newTestManager() *Manager {
	return NewManager(config.SwarmConfig{
		MaxTeammatesPerTeam: 10,
		InboxSize:           16,
		TeammateTimeout:     5 * time.Second,
		PollInterval:        50 * time.Millisecond,
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

	tm.Inbox <- MailMessage{
		From:    "leader",
		Content: "Research the API",
		Type:    "task",
	}

	time.Sleep(200 * time.Millisecond)

	if agent.getCalls() != 1 {
		t.Errorf("expected 1 agent call, got %d", agent.getCalls())
	}

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

	var evMu sync.Mutex
	events := make([]Event, 0, 2)
	onEvent := func(ev Event) {
		evMu.Lock()
		events = append(events, ev)
		evMu.Unlock()
	}

	mgr := newTestManager()
	done := make(chan struct{})
	go func() {
		runTeammateLoop(ctx, tm, team, nil, mgr, onEvent, 30*time.Minute)
		close(done)
	}()

	tm.Inbox <- MailMessage{Content: "do something", Type: "task"}

	time.Sleep(100 * time.Millisecond)

	select {
	case <-done:
		t.Error("idle loop should NOT exit when agent is nil - it should continue")
	default:

	}

	evMu.Lock()
	evCount := len(events)
	firstType := ""
	if evCount > 0 {
		firstType = events[0].Type
	}
	evMu.Unlock()
	if evCount == 0 || firstType != "teammate_error" {
		t.Fatalf("expected teammate_error event, got %v", events)
	}

	tm.Inbox <- MailMessage{Type: "shutdown"}

	select {
	case <-done:

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
		got := util.Truncate(tt.input, tt.maxLen)
		if got != tt.want {
			t.Errorf("util.Truncate(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.want)
		}
	}
}

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

	mgr.mu.Lock()
	mgr.teams["team-1"] = team
	mgr.mu.Unlock()

	go runTeammateLoop(ctx, tm, team, agent, mgr, nil, 30*time.Minute)

	taskMgr.Create("Write tests", "Add unit tests for the module", "", nil)

	time.Sleep(300 * time.Millisecond)

	calls := agent.getCalls()
	if calls != 1 {
		t.Errorf("expected agent to be called 1 time via task board polling, got %d", calls)
	}

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

	taskMgr.Create("Task A", "First", "", nil)
	taskMgr.Create("Task B", "Second", "", nil)
	taskMgr.Create("Task C", "Third", "", nil)

	time.Sleep(500 * time.Millisecond)

	calls := agent.getCalls()
	if calls != 3 {
		t.Errorf("expected 3 agent calls, got %d", calls)
	}

	for _, tk := range taskMgr.List() {
		if tk.Status != task.StatusCompleted {
			t.Errorf("task %q status = %s, want completed", tk.Subject, tk.Status)
		}
	}
}

func TestIdleRunner_NoPollWhenWorking(t *testing.T) {

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

	tm.Inbox <- MailMessage{Content: "inbox task", Type: "task"}
	time.Sleep(50 * time.Millisecond)

	if tm.getStatus() != TeammateWorking {
		t.Fatal("expected teammate to be working")
	}

	taskMgr.Create("Pending A", "", "", nil)
	taskMgr.Create("Pending B", "", "", nil)

	time.Sleep(200 * time.Millisecond)

	for _, tk := range taskMgr.List() {
		if tk.Status != task.StatusPending {
			t.Errorf("task %q should still be pending while teammate is working, got %s", tk.Subject, tk.Status)
		}
	}

	close(slowAgent.unblock)
	time.Sleep(300 * time.Millisecond)

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

type blockingAgent struct {
	unblock chan struct{}
}

func (a *blockingAgent) RunStream(ctx context.Context, _ string, onEvent func(provider.StreamEvent), origin ...string) error {
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

func TestIdleRunnerDoesNotStartQueuedWorkAfterCancel(t *testing.T) {
	agent := &countingAgent{}
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
	}
	mgr := newTestManager()

	ctx, cancel := context.WithCancel(context.Background())
	tm.ctx = ctx
	tm.cancel = cancel
	tm.Inbox <- MailMessage{Type: "task", Content: "should-not-run"}
	cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		runTeammateLoop(ctx, tm, team, agent, mgr, nil, 30*time.Minute)
	}()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected teammate loop to exit after cancel")
	}
	if agent.getCalls() != 0 {
		t.Fatalf("expected 0 agent calls after cancel, got %d", agent.getCalls())
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

	taskMgr.Create("Assigned task", "For someone else", "", map[string]string{"assignee": "tm-2"})

	time.Sleep(300 * time.Millisecond)

	if agent.getCalls() != 0 {
		t.Errorf("expected 0 agent calls (task assigned to other teammate), got %d", agent.getCalls())
	}

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

	taskMgr.Create("Open task", "Anyone can do this", "", nil)

	time.Sleep(300 * time.Millisecond)

	if agent.getCalls() != 1 {
		t.Errorf("expected 1 agent call, got %d", agent.getCalls())
	}
	tasks := taskMgr.List()
	if tasks[0].Status != task.StatusCompleted {
		t.Errorf("task should be completed, got %s", tasks[0].Status)
	}
}

func TestIdleRunner_SkipsTaskWithUnmetDependency(t *testing.T) {

	agent := &blockingAgent{}
	agent.unblock = make(chan struct{})
	defer close(agent.unblock)

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

	taskA := taskMgr.Create("Task A", "Do first", "", nil)
	taskB := taskMgr.Create("Task B", "Do after A", "", nil)
	taskMgr.Update(taskB.ID, task.UpdateOptions{AddBlockedBy: []string{taskA.ID}})

	time.Sleep(300 * time.Millisecond)

	taskAGot, _ := taskMgr.Get(taskA.ID)
	if taskAGot.Status != task.StatusInProgress {
		t.Errorf("task A should be in_progress, got %s", taskAGot.Status)
	}

	taskBGot, _ := taskMgr.Get(taskB.ID)
	if taskBGot.Status != task.StatusPending {
		t.Errorf("task B should be pending while A is in_progress (unmet dependency), got %s", taskBGot.Status)
	}
}

type panicAgent struct{}

func (a *panicAgent) RunStream(_ context.Context, _ string, onEvent func(provider.StreamEvent), origin ...string) error {
	onEvent(provider.StreamEvent{Type: provider.StreamEventText, Text: "boom"})
	panic("agent exploded mid-stream")
}

func TestRollbackClaimedTask(t *testing.T) {
	mgr := newTestManager()
	snap := mgr.CreateTeam("rollback-unit", "leader")
	defer func() { _ = mgr.DeleteTeam(snap.ID) }()
	mgr.mu.Lock()
	team := mgr.teams[snap.ID]
	mgr.mu.Unlock()

	board, err := mgr.EnsureTaskManager(team.ID)
	if err != nil {
		t.Fatalf("ensure board: %v", err)
	}
	created := board.Create("claimed", "d", "", nil)
	pending := task.StatusPending
	inProgress := task.StatusInProgress
	owner := "owner-1"
	if _, err := board.Update(created.ID, task.UpdateOptions{Status: &inProgress, Owner: &owner}); err != nil {
		t.Fatalf("claim: %v", err)
	}

	tm := &Teammate{ID: "owner-1"}
	tm.mu.Lock()
	tm.CurrentTaskID = created.ID
	tm.mu.Unlock()
	rollbackClaimedTask(mgr, team, tm)

	got, ok := board.Get(created.ID)
	if !ok || got.Status != pending {
		t.Fatalf("task must be back to pending, got %+v", got)
	}

	rollbackClaimedTask(mgr, team, &Teammate{ID: "x"})
}
