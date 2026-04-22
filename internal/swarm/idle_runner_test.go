package swarm

import (
	"context"
	"sync"
	"testing"
	"time"

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

	go runTeammateLoop(ctx, tm, team, agent, onEvent, 30*time.Minute)

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

	go runTeammateLoop(ctx, tm, team, agent, nil, 30*time.Minute)

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

	done := make(chan struct{})
	go func() {
		runTeammateLoop(ctx, tm, team, agent, nil, 30*time.Minute)
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

	done := make(chan struct{})
	go func() {
		runTeammateLoop(ctx, tm, team, agent, nil, 30*time.Minute)
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

	done := make(chan struct{})
	go func() {
		runTeammateLoop(ctx, tm, team, nil, onEvent, 30*time.Minute)
		close(done)
	}()

	// Task message with nil agent should emit error event but keep loop alive
	tm.Inbox <- MailMessage{Content: "do something", Type: "task"}

	time.Sleep(50 * time.Millisecond)

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

	done := make(chan struct{})
	go func() {
		runTeammateLoop(ctx, tm, team, &countingAgent{}, nil, 30*time.Minute)
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
