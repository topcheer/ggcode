package tui

import "testing"

// The webui mirrors the TUI queue from append/dequeue events and the
// QueuedSubmissions snapshot; all three must carry the SAME text. The append
// event used to carry the TUI bubble text (image placeholder prefix), so a
// queued message with an image could never be matched by the dequeue event
// and the "queued" banner in the browser never went away.
func TestQueueNotify_AppendAndDequeueCarryCanonicalText(t *testing.T) {
	m := newTestModel()
	var events []string
	var texts []string
	m.queueNotifyFn = func(event, text string, count int) {
		events = append(events, event)
		texts = append(texts, text)
	}

	m.pendingImages = []imageAttachedMsg{{placeholder: "[image: shot.png, 10x10, 1KB]"}}
	m.queuePendingSubmission("fix the webui")
	if len(texts) != 1 || events[0] != "append" {
		t.Fatalf("expected one append event, got %v %v", events, texts)
	}
	if texts[0] != "fix the webui" {
		t.Fatalf("append must carry the canonical queued text, got %q", texts[0])
	}
	snap := m.QueuedSubmissions()
	if len(snap) != 1 || snap[0] != "fix the webui" {
		t.Fatalf("snapshot must match the append text, got %v", snap)
	}

	m.pendingImages = nil
	m.submitPendingSubmissionCmd()
	if len(texts) != 2 || events[1] != "dequeue" || texts[1] != "fix the webui" {
		t.Fatalf("dequeue must carry the same text as append, got %v %v", events, texts)
	}
	if got := m.QueuedSubmissions(); len(got) != 0 {
		t.Fatalf("queue must be empty after dequeue, got %v", got)
	}
}
