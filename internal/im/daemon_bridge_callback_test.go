package im

import (
	"testing"
	"time"

	toolpkg "github.com/topcheer/ggcode/internal/tool"
)

// ============================================================
// handleInteractiveCallback — message ID correlation (issue #326)
// ============================================================

func newCallbackTestBridge(msgIDs map[string]string, multi bool) (*DaemonBridge, chan toolpkg.AskUserResponse) {
	req := toolpkg.AskUserRequest{
		Title: "Choose",
		Questions: []toolpkg.AskUserQuestion{
			{
				ID:      "q1",
				Title:   "Pick one",
				Kind:    toolpkg.AskUserKindSingle,
				Choices: []toolpkg.AskUserChoice{{ID: "yes", Label: "Yes"}, {ID: "no", Label: "No"}},
			},
		},
	}
	ch := make(chan toolpkg.AskUserResponse, 1)
	b := &DaemonBridge{
		pendingAsk: &pendingAskUser{
			request:     req,
			response:    ch,
			multiSelect: multi,
		},
		interactiveMsgIDs: msgIDs,
	}
	return b, ch
}

// Pending question with interactive buttons + callback from that exact
// message → accepted and submitted as the answer.
func TestHandleInteractiveCallback_MessageIDMatchAccepted(t *testing.T) {
	b, ch := newCallbackTestBridge(map[string]string{"tg": "tg_msg_1"}, false)

	b.handleInteractiveCallback(InteractiveCallback{
		MessageID: "tg_msg_1",
		Values:    []string{"yes"},
		Adapter:   "tg",
	})

	select {
	case resp := <-ch:
		if len(resp.Answers) != 1 || !resp.Answers[0].Answered {
			t.Fatalf("expected answered response, got %+v", resp.Answers)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout: matched callback was not accepted")
	}

	b.mu.Lock()
	stillPending := b.pendingAsk != nil
	b.mu.Unlock()
	if stillPending {
		t.Error("pendingAsk should be cleared after accepted callback")
	}
}

// Pending question with interactive buttons + callback from a stale card
// (different message ID) → dropped: no answer submitted, no state pollution.
func TestHandleInteractiveCallback_StaleMessageIDDropped(t *testing.T) {
	b, ch := newCallbackTestBridge(map[string]string{"tg": "tg_msg_2"}, false)

	b.handleInteractiveCallback(InteractiveCallback{
		MessageID: "tg_msg_1", // old card from a previous question
		Values:    []string{"yes"},
		Adapter:   "tg",
	})

	select {
	case resp := <-ch:
		t.Fatalf("stale callback should be dropped, got response %+v", resp)
	case <-time.After(200 * time.Millisecond):
	}

	b.mu.Lock()
	pendingCleared := b.pendingAsk == nil
	msgIDsCleared := b.interactiveMsgIDs == nil
	b.mu.Unlock()
	if pendingCleared {
		t.Error("pendingAsk must remain set when a stale callback is dropped")
	}
	if msgIDsCleared {
		t.Error("interactiveMsgIDs must remain set when a stale callback is dropped")
	}
}

// Stale-card click on the multi-select toggle path must not pollute
// multiSelectChosen.
func TestHandleInteractiveCallback_StaleMultiSelectToggleDropped(t *testing.T) {
	b, _ := newCallbackTestBridge(map[string]string{"tg": "tg_msg_5"}, true)

	b.handleInteractiveCallback(InteractiveCallback{
		MessageID: "tg_msg_1", // stale
		Values:    []string{"opt1"},
		Adapter:   "tg",
	})

	b.mu.Lock()
	chosen := b.multiSelectChosen
	b.mu.Unlock()
	if chosen != nil && len(chosen) != 0 {
		t.Fatalf("stale multi-select toggle must not pollute multiSelectChosen, got %v", chosen)
	}
}

// Text-only pending (no interactive message IDs recorded) → callbacks are
// accepted without ID correlation ("no comparable ID → don't reject").
func TestHandleInteractiveCallback_TextOnlyPendingAccepted(t *testing.T) {
	b, ch := newCallbackTestBridge(nil, false)

	b.handleInteractiveCallback(InteractiveCallback{
		MessageID: "anything",
		Values:    []string{"yes"},
		Adapter:   "tg",
	})

	select {
	case resp := <-ch:
		if len(resp.Answers) != 1 || !resp.Answers[0].Answered {
			t.Fatalf("expected answered response, got %+v", resp.Answers)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout: text-only pending callback was not accepted")
	}
}

// No pending question → callback silently dropped (existing behavior).
func TestHandleInteractiveCallback_NoPendingDropped(t *testing.T) {
	b := &DaemonBridge{
		interactiveMsgIDs: map[string]string{"tg": "tg_msg_1"},
	}

	b.handleInteractiveCallback(InteractiveCallback{
		MessageID: "tg_msg_1",
		Values:    []string{"yes"},
		Adapter:   "tg",
	})
	// Reached without panic; nothing to assert beyond no panic and no pending set.
	b.mu.Lock()
	pending := b.pendingAsk
	b.mu.Unlock()
	if pending != nil {
		t.Error("pendingAsk should stay nil")
	}
}
