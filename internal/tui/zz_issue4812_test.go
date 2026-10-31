package tui

import "testing"

// #4812: a lanchat/IM/tunnel (remote-origin) or cron (hidden) message that
// arrives while a long shell command is running gets queued in the same
// pending queue as local shell-mode input. When the shell finishes, the
// shell-done drain must distinguish the two: local input may be chained
// into the next shell command, but agent-bound entries must be drained
// through the agent path - feeding them to submitShellCommand executed a
// teammate's message as a shell command.

func TestPendingQueueHeadIsAgentOrigin(t *testing.T) {
	q := &pendingQueue{}

	if got := q.headIsAgentOrigin(); got {
		t.Fatalf("empty queue: headIsAgentOrigin() = true, want false")
	}

	// Local visible input (what the shell chain may consume).
	q.enqueueWithImages("echo local", nil)
	if got := q.headIsAgentOrigin(); got {
		t.Fatalf("local visible head: headIsAgentOrigin() = true, want false")
	}

	// Remote-origin entry (lanchat DM queued during a long shell run).
	q2 := &pendingQueue{}
	q2.enqueueWithImagesOrigin("[LAN Chat from alice]: please review", nil, true)
	if got := q2.headIsAgentOrigin(); !got {
		t.Fatalf("remote-origin head: headIsAgentOrigin() = false, want true")
	}

	// Hidden cron injection.
	q3 := &pendingQueue{}
	q3.enqueueHidden("patrol round", nil)
	if got := q3.headIsAgentOrigin(); !got {
		t.Fatalf("hidden head: headIsAgentOrigin() = false, want true")
	}

	// Mixed queue: local first, remote behind. The head decides: the shell
	// chain may consume the local entry, but once the remote entry reaches
	// the head the drain must switch to the agent path.
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
