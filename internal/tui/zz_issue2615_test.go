package tui

import (
	"strconv"
	"strings"
	"testing"
)

// #2615: suffix multiplication must not silently overflow into a wrong
// (possibly negative) value that gets persisted as context_window.
func TestIssue2615ParseIntPositiveOverflowRejected(t *testing.T) {
	// Exact boundary: MaxInt/1G fits, +1 overflows.
	maxG := int64(^uint64(0)>>1) / (1024 * 1024 * 1024)
	cases := []struct {
		in   string
		want int64
		err  string
	}{
		{"8589934593G", 0, "overflows"},
		{"999999999999G", 0, "overflows"},
		{"9999999999999999M", 0, "overflows"},
		{strconv.FormatInt(maxG, 10) + "G", maxG * 1024 * 1024 * 1024, ""}, // boundary OK
		{strconv.FormatInt(maxG+1, 10) + "G", 0, "overflows"},              // boundary +1 rejected
		{"1G", 1024 * 1024 * 1024, ""},                                     // normal values unaffected
		{"8M", 8 * 1024 * 1024, ""},
	}
	for _, c := range cases {
		got, err := parseIntPositive(c.in)
		if c.err == "" {
			if err != nil || int64(got) != c.want {
				t.Errorf("parseIntPositive(%q) = %d, %v; want %d, nil", c.in, got, err, c.want)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), c.err) {
			t.Errorf("parseIntPositive(%q) = %d, %v; want error containing %q", c.in, got, err, c.err)
		}
		if got != 0 {
			t.Errorf("parseIntPositive(%q) returned non-zero %d on error", c.in, got)
		}
	}
}

// #2617: queued-message image restore must MERGE into pendingImages, not
// overwrite - screenshots pasted during the run must survive the restore.
func TestIssue2617QueueImageRestoreMerges(t *testing.T) {
	// Drain path (drainPendingInterrupt): queue WITH an image (set
	// pendingImages before queueing so the entry carries it), then paste a
	// second image mid-run; the drain must leave both in pendingImages.
	// Note: the sibling restore in submitPendingSubmissionCmd uses the
	// identical merge fix but is not directly assertable - startAgent
	// synchronously captures and clears pendingImages (submit.go:85).
	m := newTestModel()
	m.pendingImages = []imageAttachedMsg{{placeholder: "[img1]"}}
	m.queuePendingSubmission("queued with img1")
	if len(m.pendingImages) != 0 {
		t.Fatalf("queue must capture images, pendingImages has %d", len(m.pendingImages))
	}
	m.pendingImages = []imageAttachedMsg{{placeholder: "[img3]"}}
	blocks := m.drainPendingInterrupt(1)
	if len(blocks) != 1 {
		t.Fatalf("drainPendingInterrupt returned %d blocks, want 1", len(blocks))
	}
	if len(m.pendingImages) != 2 {
		t.Fatalf("after drain restore: pendingImages has %d imgs, want 2 (merge, not overwrite)", len(m.pendingImages))
	}
}

// #2618: EVERY Up-arrow dequeue must remove its chat bubble, not just the
// first (old single lastQueuedChatID scalar left later bubbles stale).
func TestIssue2618EveryDequeueRemovesBubble(t *testing.T) {
	m := newTestModel()
	m.queuePendingSubmission("msg A")
	m.queuePendingSubmission("msg B")
	if len(m.queuedChatIDs) != 2 {
		t.Fatalf("expected 2 tracked bubble IDs, got %d", len(m.queuedChatIDs))
	}
	lenAfterQueue := m.chatList.Len()

	// First dequeue: removes B's bubble.
	m.dequeueLastVisible()
	if got := m.chatList.Len(); got != lenAfterQueue-1 {
		t.Fatalf("first dequeue: chatList.Len()=%d, want %d", got, lenAfterQueue-1)
	}
	if len(m.queuedChatIDs) != 1 {
		t.Fatalf("first dequeue: queuedChatIDs has %d left, want 1", len(m.queuedChatIDs))
	}

	// Second dequeue: must remove A's bubble too (the #2618 regression).
	m.dequeueLastVisible()
	if got := m.chatList.Len(); got != lenAfterQueue-2 {
		t.Fatalf("second dequeue: chatList.Len()=%d, want %d (stale bubble left behind)", got, lenAfterQueue-2)
	}
	if len(m.queuedChatIDs) != 0 {
		t.Fatalf("second dequeue: queuedChatIDs has %d left, want 0", len(m.queuedChatIDs))
	}
	if _, _, ok := m.dequeueLastVisible(); ok {
		t.Fatal("empty queue: dequeue must report ok=false")
	}
}
