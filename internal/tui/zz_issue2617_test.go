package tui

import "testing"

// #2617: restoring a queued message's images must MERGE with (not overwrite)
// pendingImages - screenshots pasted while the message sat in the queue were
// silently discarded by the plain assignment in both submitPendingSubmissionCmd
// and drainPendingInterrupt. Correct semantics per restorePendingInput (#1411):
// append, keeping the mid-run pastes alive.
func TestSubmitPendingSubmissionMergesQueuedImages(t *testing.T) {
	m := newTestModel()
	// Queue an unknown slash command carrying img1 (queuePendingSubmission
	// captures and clears pendingImages, as in the busy-run paste flow).
	// An unknown command never reaches startAgentWithExpand (which would
	// synchronously capture-and-clear pendingImages into its run closure),
	// so the merged state stays observable after the call.
	m.pendingImages = []imageAttachedMsg{{placeholder: "[img1]"}}
	m.queuePendingSubmission("/definitely-unknown-cmd-2617")
	if n := m.pendingImageCount(); n != 0 {
		t.Fatalf("queue must capture images, pendingImages now %d", n)
	}
	// Mid-run paste: img2 lands in pendingImages while the message waits.
	m.pendingImages = []imageAttachedMsg{{placeholder: "[img2]"}}

	// Drain path 1: submitPendingSubmissionCmd consumes the queued entry and
	// restores its captured [img1] - which must be APPENDED after img2, not
	// replace it.
	_ = m.submitPendingSubmissionCmd()
	if n := m.pendingImageCount(); n != 2 {
		t.Fatalf("both images must survive submit restore, got %d", n)
	}
	if m.pendingImages[0].placeholder != "[img2]" || m.pendingImages[1].placeholder != "[img1]" {
		t.Fatalf("merge order wrong: existing paste must come first, got %v then %v",
			m.pendingImages[0].placeholder, m.pendingImages[1].placeholder)
	}
}

// Drain path 2: mid-run interruption injection (drainPendingInterrupt) must
// merge the same way.
func TestDrainPendingInterruptMergesQueuedImages(t *testing.T) {
	m := newTestModel()
	m.pendingImages = []imageAttachedMsg{{placeholder: "[img1]"}}
	m.queuePendingSubmission("interrupt with image")
	m.pendingImages = []imageAttachedMsg{{placeholder: "[img2]"}} // mid-run paste

	blocks := m.drainPendingInterrupt(1)
	if len(blocks) != 1 || blocks[0].Text != "interrupt with image" {
		t.Fatalf("expected drained text, got %+v", blocks)
	}
	if n := m.pendingImageCount(); n != 2 {
		t.Fatalf("both images must survive interrupt drain, got %d", n)
	}
	if m.pendingImages[0].placeholder != "[img2]" || m.pendingImages[1].placeholder != "[img1]" {
		t.Fatalf("merge order wrong: got %v then %v",
			m.pendingImages[0].placeholder, m.pendingImages[1].placeholder)
	}
}

// Empty queued images must not disturb existing pastes (len>0 guard kept).
func TestDrainPendingInterruptKeepsExistingWhenNoQueuedImages(t *testing.T) {
	m := newTestModel()
	m.queuePendingSubmission("text only") // no images captured
	m.pendingImages = []imageAttachedMsg{{placeholder: "[keep]"}}
	_ = m.drainPendingInterrupt(1)
	if n := m.pendingImageCount(); n != 1 || m.pendingImages[0].placeholder != "[keep]" {
		t.Fatalf("existing paste must be untouched, got %d: %+v", n, m.pendingImages)
	}
}
