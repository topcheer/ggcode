package tui

import "sync"

// approvalFeedbackBox carries the corrective text a user typed while
// rejecting an approval from the Bubble Tea key handler (UI goroutine) to the
// agent's rejection-feedback hook (agent goroutine).
//
// Ordering guarantee: the key handler stashes the text BEFORE the deny
// decision is sent through the approval channel, and the agent takes it only
// AFTER that decision is received — channel ordering makes a plain mutex
// sufficient. take() clears the buffer so stale feedback can never attach to
// a later rejection.
type approvalFeedbackBox struct {
	mu  sync.Mutex
	buf string
}

func (b *approvalFeedbackBox) stash(s string) {
	b.mu.Lock()
	b.buf = s
	b.mu.Unlock()
}

func (b *approvalFeedbackBox) take() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.buf
	b.buf = ""
	return s
}

// approvalFeedbackWireMsg delivers a freshly created feedback box to the LIVE
// Bubble Tea model. The REPL holds only a value snapshot of the model
// (repl.go), so the box cannot be attached to it directly — it is sent as a
// message and stored by the update dispatcher instead.
type approvalFeedbackWireMsg struct {
	box *approvalFeedbackBox
}
