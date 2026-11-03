package tui

import "testing"

func TestPendingQueueHeadIsAgentOrigin(t *testing.T) {
	q := &pendingQueue{}

	if got := q.headIsAgentOrigin(); got {
		t.Fatalf("empty queue: headIsAgentOrigin() = true, want false")
	}

	q.enqueueWithImages("echo local", nil)
	if got := q.headIsAgentOrigin(); got {
		t.Fatalf("local visible head: headIsAgentOrigin() = true, want false")
	}

	q2 := &pendingQueue{}
	q2.enqueueWithImagesOrigin("[LAN Chat from alice]: please review", nil, true)
	if got := q2.headIsAgentOrigin(); !got {
		t.Fatalf("remote-origin head: headIsAgentOrigin() = false, want true")
	}

	q3 := &pendingQueue{}
	q3.enqueueHidden("patrol round", nil)
	if got := q3.headIsAgentOrigin(); !got {
		t.Fatalf("hidden head: headIsAgentOrigin() = false, want true")
	}

	q4 := &pendingQueue{}
	q4.enqueueWithImages("echo local", nil)
	q4.enqueueWithImagesOrigin("[LAN Chat from alice]: later", nil, true)
	if got := q4.headIsAgentOrigin(); got {
		t.Fatalf("mixed queue, local head: headIsAgentOrigin() = true, want false")
	}
	if text := q4.consume(); text != "echo local" {
		t.Fatalf("consume() = %q, want %q", text, "echo local")
	}
	if got := q4.headIsAgentOrigin(); !got {
		t.Fatalf("mixed queue after consuming local head: headIsAgentOrigin() = false, want true (remote entry now at head)")
	}
}
