package tui

import (
	"strings"
	"testing"
)

func TestCompactResultMsgDrainsPendingSubmission(t *testing.T) {
	m := newTestModel()
	m.setLoading(true)
	m.queuePendingSubmission("queued during compact")
	if m.pendingSubmissionCount() != 1 {
		t.Fatalf("expected 1 pending, got %d", m.pendingSubmissionCount())
	}
	m2, cmd := m.handleCompactResultMsg(compactResultMsg{text: "compacted"})
	if m2.pendingSubmissionCount() != 0 {
		t.Fatalf("pending must be drained, got %d", m2.pendingSubmissionCount())
	}
	if cmd == nil {
		t.Fatal("drain must return the submit command")
	}

}

func TestCompactResultMsgNoPendingNoop(t *testing.T) {
	m := newTestModel()
	m2, cmd := m.handleCompactResultMsg(compactResultMsg{text: "compacted"})
	if cmd != nil {
		t.Fatal("no pending submissions: expected nil cmd")
	}
	if m2.pendingSubmissionCount() != 0 {
		t.Fatal("queue must stay empty")
	}
}

func TestRestorePendingInputRemovesQueuedBubbles(t *testing.T) {
	m := newTestModel()
	m.queuePendingSubmission("first queued")
	m.queuePendingSubmission("second queued")
	if len(m.queuedChatIDs) != 2 {
		t.Fatalf("expected 2 tracked bubble IDs, got %d", len(m.queuedChatIDs))
	}
	chatID := m.lastQueuedChatID
	lenBefore := m.chatList.Len()
	m.restorePendingInput()
	if m.pendingSubmissionCount() != 0 {
		t.Fatalf("visible queue must be consumed, got %d", m.pendingSubmissionCount())
	}
	if len(m.queuedChatIDs) != 0 {
		t.Fatalf("bubble ID list must be cleared, got %d", len(m.queuedChatIDs))
	}
	if m.chatList.FindByID(chatID) != nil {
		t.Fatal("queued bubble must be removed from chat list on restore")
	}
	if m.chatList.Len() != lenBefore-2 {
		t.Fatalf("two queued bubbles must be removed: len %d -> %d", lenBefore, m.chatList.Len())
	}

	got := m.input.Value()
	if !strings.Contains(got, "first queued") || !strings.Contains(got, "second queued") {
		t.Fatalf("both queued texts must be restored to input, got %+v", got)
	}
}

func TestRestorePendingInputKeepsHiddenQueued(t *testing.T) {
	m := newTestModel()
	m.queuePendingSubmission("visible queued")
	m.queuePendingSubmissionHidden("cron trigger")
	m.restorePendingInput()
	if m.pendingSubmissionCount() != 1 {
		t.Fatalf("hidden submission must remain queued, got %d", m.pendingSubmissionCount())
	}
	if strings.Contains(m.input.Value(), "cron trigger") {
		t.Fatal("hidden submission must not be restored to the composer")
	}
}

func TestDrainPendingInterruptPopsAndReturns(t *testing.T) {
	m := newTestModel()
	m.queuePendingSubmission("interrupt me")
	text := m.drainPendingInterrupt(1)
	if len(text) != 1 || text[0].Text != "interrupt me" {
		t.Fatalf("expected drained text, got %+v", text)
	}
	if m.pendingSubmissionCount() != 0 {
		t.Fatalf("queue must be empty after drain, got %d", m.pendingSubmissionCount())
	}

	if again := m.drainPendingInterrupt(1); len(again) != 0 {
		t.Fatalf("empty queue must drain to empty, got %+v", again)
	}
}

func TestDequeueLastVisibleSyncsBubbleIDs(t *testing.T) {
	m := newTestModel()
	m.queuePendingSubmission("a")
	m.queuePendingSubmission("b")
	text, _, ok := m.dequeueLastVisible()
	if !ok || text != "b" {
		t.Fatalf("expected last visible 'b', got %q ok=%t", text, ok)
	}
	if len(m.queuedChatIDs) != 1 {
		t.Fatalf("expected 1 remaining tracked bubble ID, got %d", len(m.queuedChatIDs))
	}
	if m.queuedChatIDs[0] == m.lastQueuedChatID && m.lastQueuedChatID != "" {

		t.Fatal("dequeued bubble ID must be removed from tracking")
	}
}
