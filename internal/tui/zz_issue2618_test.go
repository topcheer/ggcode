package tui

import "testing"

// #2618: the second Up-arrow dequeue must also remove its chat bubble. The
// old implementation deleted only via the single lastQueuedChatID value,
// which was cleared after the first dequeue - so a second dequeue (queue A,B;
// Up pops B; Ctrl+U clears input; Up pops A) left bubble A permanently
// rendered even though message A was never submitted.
func TestSecondDequeueRemovesItsBubble(t *testing.T) {
	m := newTestModel()
	m.queuePendingSubmission("a")
	m.queuePendingSubmission("b")
	if len(m.queuedChatIDs) != 2 {
		t.Fatalf("expected 2 tracked bubble IDs, got %d", len(m.queuedChatIDs))
	}
	bubbleA, bubbleB := m.queuedChatIDs[0], m.queuedChatIDs[1]
	if m.chatList.FindByID(bubbleA) == nil || m.chatList.FindByID(bubbleB) == nil {
		t.Fatal("both queued bubbles must be rendered before dequeue")
	}

	// First dequeue: pops "b", removes bubble B.
	if text, _, ok := m.dequeueLastVisible(); !ok || text != "b" {
		t.Fatalf("first dequeue: expected 'b', got %q ok=%t", text, ok)
	}
	if m.chatList.FindByID(bubbleB) != nil {
		t.Fatal("first dequeue must remove bubble B")
	}

	// Second dequeue (input cleared by Ctrl+U in the real flow): pops "a",
	// must remove bubble A too — the regression the old lastQueuedChatID=""
	// guard skipped entirely.
	if text, _, ok := m.dequeueLastVisible(); !ok || text != "a" {
		t.Fatalf("second dequeue: expected 'a', got %q ok=%t", text, ok)
	}
	if m.chatList.FindByID(bubbleA) != nil {
		t.Fatal("second dequeue must remove bubble A (was permanently stale before #2618 fix)")
	}
	if len(m.queuedChatIDs) != 0 {
		t.Fatalf("bubble ID list must be empty, got %d", len(m.queuedChatIDs))
	}
	if m.pendingSubmissionCount() != 0 {
		t.Fatalf("queue must be empty, got %d", m.pendingSubmissionCount())
	}
}

// Mixed hidden/visible: hidden submissions render no bubble, so the visible
// bubble list must still pop in LIFO order against visible entries only.
func TestDequeueVisibleBubbleAfterHiddenEnqueue(t *testing.T) {
	m := newTestModel()
	m.queuePendingSubmission("visible one")
	m.queuePendingSubmissionHidden("cron hidden")
	m.queuePendingSubmission("visible two")
	// Two visible bubbles tracked, hidden one has no bubble.
	if len(m.queuedChatIDs) != 2 {
		t.Fatalf("expected 2 tracked bubble IDs, got %d", len(m.queuedChatIDs))
	}
	if text, _, ok := m.dequeueLastVisible(); !ok || text != "visible two" {
		t.Fatalf("expected 'visible two', got %q ok=%t", text, ok)
	}
	if len(m.queuedChatIDs) != 1 {
		t.Fatalf("expected 1 tracked bubble ID, got %d", len(m.queuedChatIDs))
	}
	// Hidden submission must still be queued (untouched by visible dequeues).
	if m.pendingSubmissionCount() != 2 {
		t.Fatalf("expected 2 pending (1 hidden + 1 visible), got %d", m.pendingSubmissionCount())
	}
}
