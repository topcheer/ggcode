package tui

// #3466 probe: remote-origin text queued while the agent is busy must arm
// the refusal-ledger inhibit when DRAINED, not silently write the ledger
// (the #3460 direct-path gap). The queue stamps RemoteOrigin on the entry;
// consumeDetailed reports it so submitPendingSubmissionCmd can arm.

import "testing"

func TestIssue3466_RemoteOriginRidesQueueToDrain(t *testing.T) {
	q := &pendingQueue{}
	q.enqueueWithImages("local one", nil) // local
	n := q.enqueueWithImagesOrigin("remote DM", nil, true)
	if n != 2 {
		t.Fatalf("queue count = %d, want 2", n)
	}
	q.enqueueWithImages("local two", nil) // local

	// First drain: consumes the visible prefix (all three, merged).
	_, hidden, _, _, remote := q.consumeDetailed()
	if hidden {
		t.Fatal("visible entries must drain as visible")
	}
	if !remote {
		t.Fatal("a remote-origin entry inside the drained prefix must report remote=true")
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
