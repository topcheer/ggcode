package tui

import "testing"

func TestIssue3466_RemoteOriginRidesQueueToDrain(t *testing.T) {
	q := &pendingQueue{}
	q.enqueueWithImages("local one", nil)
	n := q.enqueueWithImagesOrigin("remote DM", nil, true)
	if n != 2 {
		t.Fatalf("queue count = %d, want 2", n)
	}
	q.enqueueWithImages("local two", nil)

	text1, hidden1, _, _, remote1 := q.consumeDetailed()
	if hidden1 {
		t.Fatal("visible entries must drain as visible")
	}
	if remote1 {
		t.Fatalf("first batch (%q) is local-only after the #4812 origin split: remote=true, want false", text1)
	}
	text2, _, _, _, remote2 := q.consumeDetailed()
	if text2 != "remote DM" || !remote2 {
		t.Fatalf("second batch = (%q, remote=%v), want (%q, true) - the remote entry must flag its own batch", text2, remote2, "remote DM")
	}
	if _, _, _, _, remote3 := q.consumeDetailed(); remote3 {
		t.Fatal("third batch is local-only: remote=true, want false")
	}
}

func TestIssue3466_PureLocalDrainIsNotRemote(t *testing.T) {
	q := &pendingQueue{}
	q.enqueueWithImages("local one", nil)
	q.enqueueWithImages("local two", nil)
	_, _, _, _, remote := q.consumeDetailed()
	if remote {
		t.Fatal("pure-local drain must report remote=false - local runs keep recording refusals")
	}
}

func TestIssue3466_LocalQueueWrapperUnflagged(t *testing.T) {
	q := &pendingQueue{}
	q.enqueueWithImages("local", nil)
	if q.items[0].RemoteOrigin {
		t.Fatal("queuePendingSubmission (local wrapper) must not stamp RemoteOrigin")
	}
}
