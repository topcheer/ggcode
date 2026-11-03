package im

import (
	"context"
	"testing"
	"time"

	toolpkg "github.com/topcheer/ggcode/internal/tool"
)

func newIssue655Bridge() *DaemonBridge {
	return &DaemonBridge{
		emitter: &IMEmitter{},
	}
}

func TestIssue655SingleRegistrationChoice(t *testing.T) {
	b := newIssue655Bridge()

	b.mu.Lock()
	b.interactiveMsgIDs = map[string]string{"test": "m1"}
	b.mu.Unlock()

	req := toolpkg.AskUserRequest{
		Title: "pick",
		Questions: []toolpkg.AskUserQuestion{{
			ID:      "q1",
			Title:   "Which?",
			Kind:    toolpkg.AskUserKindSingle,
			Choices: []toolpkg.AskUserChoice{{ID: "a", Label: "A"}, {ID: "b", Label: "B"}},
		}},
	}

	type res struct {
		resp toolpkg.AskUserResponse
		err  error
	}
	done := make(chan res, 1)
	go func() {
		r, e := b.HandleAskUser(context.Background(), req)
		done <- res{r, e}
	}()

	var pending *pendingAskUser
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		b.mu.Lock()
		pending = b.pendingAsk
		b.mu.Unlock()
		if pending != nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if pending == nil {
		t.Fatal("pendingAsk never registered")
	}

	b.mu.Lock()
	pending.msgIDs = map[string]string{"test": "m1"}
	b.mu.Unlock()

	b.handleInteractiveCallback(InteractiveCallback{
		Adapter:   "test",
		MessageID: "m1",
		Values:    []string{"a"},
	})

	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("HandleAskUser returned error: %v", r.err)
		}
		if r.resp.AnsweredCount != 1 || len(r.resp.Answers) != 1 || !r.resp.Answers[0].Answered {
			t.Fatalf("answer was dropped — HandleAskUser completed without the early reply (#655): %+v", r.resp)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("HandleAskUser did not return — early answer resolved the overwritten first channel and was dropped (#655)")
	}
}

func TestIssue655AnswerDoesNotClearNextRegistration(t *testing.T) {
	b := newIssue655Bridge()

	first := &pendingAskUser{
		request:  toolpkg.AskUserRequest{},
		response: make(chan toolpkg.AskUserResponse, 1),
		msgIDs:   map[string]string{"test": "m2"},
	}
	b.mu.Lock()
	b.pendingAsk = first
	b.interactiveMsgIDs = map[string]string{"test": "m2"}
	b.mu.Unlock()

	b.handleInteractiveCallback(InteractiveCallback{Adapter: "test", MessageID: "m2", Values: []string{"x"}})
	select {
	case <-first.response:
	default:
		t.Fatal("answer was not delivered to the first pending channel")
	}
	b.mu.Lock()
	cur := b.pendingAsk
	b.mu.Unlock()
	if cur != nil {
		t.Fatal("answered registration was not cleared")
	}

	next := &pendingAskUser{
		request:  toolpkg.AskUserRequest{},
		response: make(chan toolpkg.AskUserResponse, 1),
	}
	b.mu.Lock()
	b.pendingAsk = next
	b.interactiveMsgIDs = map[string]string{"test": "m3"}
	b.mu.Unlock()

	b.handleInteractiveCallback(InteractiveCallback{Adapter: "test", MessageID: "m2", Values: []string{"x"}})
	select {
	case <-next.response:
		t.Fatal("stale callback for the answered question leaked into the next registration")
	default:
	}
	b.mu.Lock()
	cur = b.pendingAsk
	b.mu.Unlock()
	if cur != next {
		t.Fatal("answering the previous question wiped the NEXT question's registration — answers will be misrouted/dropped (#655)")
	}
}
