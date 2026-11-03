package im

import (
	"context"
	"testing"
	"time"

	toolpkg "github.com/topcheer/ggcode/internal/tool"
)

func TestStaleCardRejectedByTextOnlySuccessor(t *testing.T) {
	req := toolpkg.AskUserRequest{Title: "Q2 (text-only)"}
	ch2 := make(chan toolpkg.AskUserResponse, 1)
	b := &DaemonBridge{
		pendingAsk: &pendingAskUser{
			request:  req,
			response: ch2,
		},

		interactiveMsgIDs: map[string]string{"tg": "tg_msg_1"},
	}

	b.handleInteractiveCallback(InteractiveCallback{
		Adapter:   "tg",
		MessageID: "tg_msg_1",
		Values:    []string{"yes"},
	})

	select {
	case r := <-ch2:
		t.Fatalf("stale card submitted as the text-only successor's answer: %+v", r)
	case <-time.After(100 * time.Millisecond):

	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if b.pendingAsk == nil {
		t.Fatal("dropping a stale card must not clear the pending question")
	}
}

func TestStaleCardRejectedBySuccessorButtons(t *testing.T) {
	req := toolpkg.AskUserRequest{Title: "Q2"}
	ch2 := make(chan toolpkg.AskUserResponse, 1)
	b := &DaemonBridge{
		pendingAsk: &pendingAskUser{
			request:  req,
			response: ch2,
			msgIDs:   map[string]string{"tg": "tg_msg_2"},
		},
		interactiveMsgIDs: map[string]string{"tg": "tg_msg_2"},
	}
	b.handleInteractiveCallback(InteractiveCallback{
		Adapter:   "tg",
		MessageID: "tg_msg_1",
		Values:    []string{"yes"},
	})
	select {
	case r := <-ch2:
		t.Fatalf("stale card submitted as successor answer: %+v", r)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestCallbackConsumeKeepsLastEmitted(t *testing.T) {
	b, ch := newCallbackTestBridge(map[string]string{"tg": "tg_msg_1"}, false)
	b.handleInteractiveCallback(InteractiveCallback{
		Adapter:   "tg",
		MessageID: "tg_msg_1",
		Values:    []string{"yes"},
	})
	select {
	case <-ch:
	default:
		t.Fatal("original card should have been accepted")
	}
	b.mu.Lock()
	ids := b.interactiveMsgIDs
	pendingGone := b.pendingAsk == nil
	b.mu.Unlock()
	if !pendingGone {
		t.Fatal("pending should be consumed")
	}
	if ids == nil {
		t.Fatal("consume must keep the last-emitted map (stale rejection depends on it)")
	}

	req2 := toolpkg.AskUserRequest{Title: "Q2"}
	ch2 := make(chan toolpkg.AskUserResponse, 1)
	b.mu.Lock()
	b.pendingAsk = &pendingAskUser{request: req2, response: ch2}
	b.mu.Unlock()
	b.handleInteractiveCallback(InteractiveCallback{
		Adapter:   "tg",
		MessageID: "tg_msg_1",
		Values:    []string{"yes"},
	})
	select {
	case r := <-ch2:
		t.Fatalf("re-tapped consumed card answered the successor: %+v", r)
	case <-time.After(100 * time.Millisecond):
	}
}

var _ = context.Background
