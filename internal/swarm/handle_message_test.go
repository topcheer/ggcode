package swarm

// White-box pin tests for handleMessage's direct-delivery (ReplyTo) paths.
// These tests were added (r165) before refactoring handleMessage so that the
// decomposition is guarded against behavior drift: they pin the success
// reply, the error reply (#1497), the missing-agent guard, the nil-ReplyTo
// branches, and the non-blocking abandoned-reply fallback.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/task"
)

// errAgent always fails RunStream with a fixed error.
type errAgent struct{ err error }

func (a *errAgent) RunStream(_ context.Context, _ string, onEvent func(provider.StreamEvent)) error {
	if onEvent != nil {
		onEvent(provider.StreamEvent{Type: provider.StreamEventText, Text: "partial output"})
	}
	return a.err
}

// eventRecorder collects swarm events emitted during a test.
type eventRecorder struct {
	mu     sync.Mutex
	events []Event
}

func (r *eventRecorder) record(ev Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
}

func (r *eventRecorder) byType(typ string) []Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []Event
	for _, ev := range r.events {
		if ev.Type == typ {
			out = append(out, ev)
		}
	}
	return out
}

// newHandleMessageFixture builds a minimal teammate/team pair for white-box
// handleMessage tests.
func newHandleMessageFixture(ctx context.Context) (*Teammate, *Team) {
	tm := &Teammate{
		ID:     "tm-hm",
		Name:   "pinner",
		Status: TeammateIdle,
		Inbox:  make(chan MailMessage, 1),
		ctx:    ctx,
	}
	team := &Team{
		ID:        "team-hm",
		Name:      "hm",
		LeaderID:  "leader",
		Teammates: map[string]*Teammate{"tm-hm": tm},
		Tasks:     task.NewManager(),
	}
	return tm, team
}

// TestHandleMessage_ReplyToSuccess pins the direct-delivery success path:
// the TaskResult lands on the caller's channel with the agent output, the
// stale board task ID is cleared (#1688), and the teammate returns to idle
// with exactly one teammate_working and one teammate_idle event.
func TestHandleMessage_ReplyToSuccess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tm, team := newHandleMessageFixture(ctx)
	agent := &countingAgent{}
	mgr := newTestManager()
	tm.mu.Lock()
	tm.CurrentTaskID = "stale-board-task" // must be cleared for direct messages
	tm.mu.Unlock()

	replyCh := make(chan TaskResult, 1)
	var rec eventRecorder

	handleMessage(ctx, tm, team, agent, mgr, rec.record, 30*time.Minute, MailMessage{
		From:    "leader",
		Content: "ping",
		Type:    "task",
		ReplyTo: replyCh,
	})

	select {
	case got := <-replyCh:
		if got.Error != nil {
			t.Fatalf("expected nil error, got %v", got.Error)
		}
		if !strings.Contains(got.Output, "ping") {
			t.Errorf("unexpected reply output %q", got.Output)
		}
	default:
		t.Fatal("expected a reply on ReplyTo channel, got none")
	}
	if status := tm.getStatus(); status != TeammateIdle {
		t.Errorf("expected idle after task, got %q", status)
	}
	if working := rec.byType("teammate_working"); len(working) != 1 {
		t.Errorf("expected exactly one teammate_working event, got %d", len(working))
	}
	if idle := rec.byType("teammate_idle"); len(idle) != 1 {
		t.Errorf("expected exactly one teammate_idle event, got %d", len(idle))
	}
	tm.mu.Lock()
	cleared := tm.CurrentTaskID == ""
	tm.mu.Unlock()
	if !cleared {
		t.Error("expected CurrentTaskID to be cleared for direct messages (#1688)")
	}

	// The success path with a nil ReplyTo must not panic and must stay idle.
	handleMessage(ctx, tm, team, agent, mgr, rec.record, 30*time.Minute, MailMessage{
		From:    "leader",
		Content: "no-reply task",
		Type:    "task",
	})
	if status := tm.getStatus(); status != TeammateIdle {
		t.Errorf("expected idle after nil-ReplyTo task, got %q", status)
	}
}

// TestHandleMessage_ReplyToCarriesError pins #1497: a failing direct task
// must surface its error both on the ReplyTo channel and on the
// teammate_idle event so remote clients do not render failures green.
func TestHandleMessage_ReplyToCarriesError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tm, team := newHandleMessageFixture(ctx)
	agent := &errAgent{err: errors.New("boom")}
	mgr := newTestManager()

	replyCh := make(chan TaskResult, 1)
	var rec eventRecorder

	handleMessage(ctx, tm, team, agent, mgr, rec.record, 30*time.Minute, MailMessage{
		From:    "leader",
		Content: "failing task",
		Type:    "task",
		ReplyTo: replyCh,
	})

	select {
	case got := <-replyCh:
		if !errors.Is(got.Error, errors.New("boom")) && got.Error == nil {
			t.Fatalf("expected boom error on reply, got %v", got.Error)
		}
		if got.Error == nil || !strings.Contains(got.Error.Error(), "boom") {
			t.Fatalf("expected boom error on reply, got %v", got.Error)
		}
	default:
		t.Fatal("expected a reply on ReplyTo channel, got none")
	}
	if status := tm.getStatus(); status != TeammateIdle {
		t.Errorf("expected idle after failed task, got %q", status)
	}
	idle := rec.byType("teammate_idle")
	if len(idle) != 1 {
		t.Fatalf("expected one teammate_idle event, got %d", len(idle))
	}
	if idle[0].Error == nil || !strings.Contains(idle[0].Error.Error(), "boom") {
		t.Errorf("expected teammate_idle to carry the task error (#1497), got %v", idle[0].Error)
	}
}

// TestHandleMessage_NoAgentRepliesError pins the missing-agent guard: the
// caller must receive an error reply instead of blocking forever, a
// teammate_error event must be emitted, and the teammate must not flip to
// working.
func TestHandleMessage_NoAgentRepliesError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tm, team := newHandleMessageFixture(ctx)
	mgr := newTestManager()

	replyCh := make(chan TaskResult, 1)
	var rec eventRecorder

	handleMessage(ctx, tm, team, nil, mgr, rec.record, 30*time.Minute, MailMessage{
		From:    "leader",
		Content: "orphan task",
		Type:    "task",
		ReplyTo: replyCh,
	})

	select {
	case got := <-replyCh:
		if got.Error == nil || !strings.Contains(got.Error.Error(), "has no agent") {
			t.Fatalf("expected no-agent error reply, got %v", got.Error)
		}
	default:
		t.Fatal("expected an error reply on ReplyTo channel, got none")
	}
	if terr := rec.byType("teammate_error"); len(terr) != 1 {
		t.Errorf("expected one teammate_error event, got %d", len(terr))
	}
	if status := tm.getStatus(); status != TeammateIdle {
		t.Errorf("expected teammate to stay idle, got %q", status)
	}
	if len(rec.byType("teammate_working")) != 0 {
		t.Error("teammate_working must not be emitted without an agent")
	}
}

// TestHandleMessage_NoAgentNilReplyTo covers the nil-agent + nil-ReplyTo
// combination: no reply channel to answer, just the error event, no panic.
func TestHandleMessage_NoAgentNilReplyTo(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tm, team := newHandleMessageFixture(ctx)
	mgr := newTestManager()
	var rec eventRecorder

	handleMessage(ctx, tm, team, nil, mgr, rec.record, 30*time.Minute, MailMessage{
		From:    "leader",
		Content: "orphan task",
		Type:    "task",
	})

	if terr := rec.byType("teammate_error"); len(terr) != 1 {
		t.Errorf("expected one teammate_error event, got %d", len(terr))
	}
}

// TestHandleMessage_AbandonedReplyToDoesNotBlock pins the non-blocking reply
// fallback: when nobody reads an unbuffered ReplyTo channel and the context
// is cancelled, handleMessage must give up on the reply, mark the teammate
// shutting_down (cancel path), and never emit teammate_idle.
func TestHandleMessage_AbandonedReplyToDoesNotBlock(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	tm, team := newHandleMessageFixture(ctx)
	agent := &countingAgent{}
	mgr := newTestManager()
	var rec eventRecorder

	done := make(chan struct{})
	go func() {
		defer close(done)
		handleMessage(ctx, tm, team, agent, mgr, rec.record, 30*time.Minute, MailMessage{
			From:    "leader",
			Content: "nobody listens",
			Type:    "task",
			ReplyTo: make(chan TaskResult), // unbuffered, no reader
		})
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handleMessage blocked forever on an abandoned ReplyTo channel")
	}
	if status := tm.getStatus(); status != TeammateShuttingDown {
		t.Errorf("expected shutting_down after ctx cancel, got %q", status)
	}
	if len(rec.byType("teammate_idle")) != 0 {
		t.Error("teammate_idle must not be emitted when context was cancelled")
	}
}
