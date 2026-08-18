package im

import (
	"context"
	"testing"
	"time"

	toolpkg "github.com/topcheer/ggcode/internal/tool"
)

// newIssue655Bridge builds a DaemonBridge with only the fields the pendingAsk
// lifecycle touches. The IMEmitter is safe to use bare (all its methods are
// nil-manager safe), so no adapter stack is required.
func newIssue655Bridge() *DaemonBridge {
	return &DaemonBridge{
		emitter: &IMEmitter{},
	}
}

// TestIssue655SingleRegistrationChoice tests defect 1: for a choice question,
// pendingAsk must be registered exactly once, BEFORE the buttons are sent.
// Pre-fix, a second registration with a fresh channel unconditionally
// overwrote the first right after EmitAskUserInteractive returned — any
// answer landing in that window resolved the first (orphaned, buffered)
// channel and was silently dropped, so HandleAskUser hung until ctx timeout
// and the user had to click twice.
func TestIssue655SingleRegistrationChoice(t *testing.T) {
	b := newIssue655Bridge()

	// Pretend the interactive send recorded a message ID for adapter "test".
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

	// Wait for the registration, then race: answer immediately, the way an
	// early button click would land right after the buttons go out.
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

	// Callback for the exact interactive message — must resolve the CURRENT
	// registration (the only one) so HandleAskUser returns promptly.
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

// TestIssue655AnswerDoesNotClearNextRegistration tests defect 2: answering a
// question must clear pendingAsk via compare-then-clear, not a blind wipe.
// Deterministic production-path coverage: (a) an answered registration is
// cleared, and (b) a stale late duplicate callback for the OLD message is
// dropped by correlation and must not disturb the NEXT registration.
func TestIssue655AnswerDoesNotClearNextRegistration(t *testing.T) {
	b := newIssue655Bridge()

	// Q1 registered with interactive message m2.
	first := &pendingAskUser{
		request:  toolpkg.AskUserRequest{},
		response: make(chan toolpkg.AskUserResponse, 1),
	}
	b.mu.Lock()
	b.pendingAsk = first
	b.interactiveMsgIDs = map[string]string{"test": "m2"}
	b.mu.Unlock()

	// Answer Q1 via the production callback path — must deliver into first's
	// channel AND clear pendingAsk (it is still the current registration).
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

	// Q2 gets registered (with its own interactive message m3) — then a LATE
	// duplicate callback for Q1's message m2 arrives. The correlation guard
	// must drop it and Q2's registration must survive untouched.
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
