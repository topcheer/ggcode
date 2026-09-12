package im

// #2134 regression: interactiveMsgIDs had no ownership - a stale button
// card (from an answered/expired/text-answered question) could silently
// submit as a successor question's answer whenever the successor was
// text-only (fail-open: expected=="" accepted anything). IDs now live on
// the pending question; the bridge-level map records the LAST EMITTED set
// so text-only successors reject stale cards instead of fail-opening.

import (
	"context"
	"testing"
	"time"

	toolpkg "github.com/topcheer/ggcode/internal/tool"
)

// Entry 3 (the everyday path): the user answers a button question by
// TYPING; the pending is consumed but the card stays on screen. A later
// text-only question is pending when the user taps the stale card - the
// tap must be dropped, not submitted as the new question's answer.
func TestStaleCardRejectedByTextOnlySuccessor(t *testing.T) {
	req := toolpkg.AskUserRequest{Title: "Q2 (text-only)"}
	ch2 := make(chan toolpkg.AskUserResponse, 1)
	b := &DaemonBridge{
		pendingAsk: &pendingAskUser{ // successor: text-only, NO msgIDs
			request:  req,
			response: ch2,
		},
		// Last emitted set belongs to the EARLIER button question.
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
		// dropped - correct
	}
	// The successor's registration must survive (no blind wipe).
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.pendingAsk == nil {
		t.Fatal("dropping a stale card must not clear the pending question")
	}
}

// Successor WITH buttons: the old card must mismatch the new expected ID.
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
		MessageID: "tg_msg_1", // stale card from Q1
		Values:    []string{"yes"},
	})
	select {
	case r := <-ch2:
		t.Fatalf("stale card submitted as successor answer: %+v", r)
	case <-time.After(100 * time.Millisecond):
	}
}

// The callback-consume path (L212 area) must NOT wipe the bridge-level
// last-emitted map: after answering Q1 via its card, a text-only Q2 still
// needs those IDs to reject Q1's remaining cards.
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

	// Now a text-only successor: the SAME card tapped again must drop.
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
