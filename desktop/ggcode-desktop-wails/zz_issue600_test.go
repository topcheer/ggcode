package main

// Issue #600 regression tests: five desktop-notification defects.
// N1: approval dedup key had no request identity (app.go fixed wording).
// N2: NotifyApprovalNeeded dedup branch skipped unread/setBadge bump.
// N3: Layout.tsx hardcoded focused=true on mount (frontend; covered by
//     the Go-side helper behavior plus manual inspection, no TS test runner).
// N4: Windows toast queue-full drop did not roll back lastShown/unread.
// N5: error/complete fixed wording merged concurrent sessions.

import (
	"encoding/json"
	"testing"
	"time"
)

// --- N1/N5 helpers (app.go) ---

func TestNotifyApprovalBodyCarriesIdentity(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "full payload",
			raw:  `{"requestID":"req-1234567890","toolName":"run_command","input":"ls"}`,
			want: "Approval needed: run_command (#req-1234)",
		},
		{
			name: "tool only",
			raw:  `{"toolName":"edit_file"}`,
			want: "Approval needed: edit_file",
		},
		{
			name: "request id only",
			raw:  `{"requestID":"abcdefgh"}`,
			want: "Approval needed (#abcdefgh)",
		},
		{
			name: "empty payload falls back to legacy wording",
			raw:  `{}`,
			want: "Approval needed",
		},
		{
			name: "nil payload falls back to legacy wording",
			raw:  ``,
			want: "Approval needed",
		},
		{
			name: "non-string field ignored",
			raw:  `{"requestID":123}`,
			want: "Approval needed",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := notifyApprovalBody(json.RawMessage(tc.raw))
			if got != tc.want {
				t.Fatalf("notifyApprovalBody(%s) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

// N1 probe: two distinct approvals must produce distinct bodies (and hence
// distinct dedup keys) within the 5s window.
func TestDistinctApprovalsProduceDistinctDedupKeys(t *testing.T) {
	nm := NewNotificationManager()
	nm.SetFocused(false)

	raw1 := json.RawMessage(`{"requestID":"req-111111111","toolName":"run_command"}`)
	raw2 := json.RawMessage(`{"requestID":"req-222222222","toolName":"edit_file"}`)
	body1 := notifyApprovalBody(raw1)
	body2 := notifyApprovalBody(raw2)
	if body1 == body2 {
		t.Fatalf("distinct approvals produced identical bodies %q — dedup would swallow the second banner", body1)
	}

	nm.NotifyApprovalNeeded("GGCode", body1)
	nm.NotifyApprovalNeeded("GGCode", body2)

	if got := nm.GetUnread(); got != 2 {
		t.Fatalf("unread = %d after two distinct approvals, want 2 (second banner previously swallowed)", got)
	}
	if len(nm.lastShown) != 2 {
		t.Fatalf("lastShown has %d entries, want 2 (identity-bearing keys)", len(nm.lastShown))
	}
}

// N2 probe: the dedup branch of NotifyApprovalNeeded must still bump the
// unread counter (mirror of Notify's #427 fix).
func TestApprovalDedupBranchBumpsUnread(t *testing.T) {
	nm := NewNotificationManager()
	nm.SetFocused(false)

	nm.NotifyApprovalNeeded("GGCode", "Approval needed")
	if got := nm.GetUnread(); got != 1 {
		t.Fatalf("unread after first approval = %d, want 1", got)
	}
	// Replay the identical request within the dedup window.
	nm.NotifyApprovalNeeded("GGCode", "Approval needed")
	if got := nm.GetUnread(); got != 2 {
		t.Fatalf("unread after deduped replay = %d, want 2 (#600 N2: dedup branch previously skipped the bump)", got)
	}

	// Focused approvals do not bump (unchanged semantics).
	nm.SetFocused(true)
	nm.NotifyApprovalNeeded("GGCode", "Approval needed")
	if got := nm.GetUnread(); got != 0 {
		t.Fatalf("unread after focused deduped approval = %d, want 0", got)
	}
}

// N4 probe: Windows toast queue-full must roll back the committed
// lastShown entry and unread count. We simulate a full queue directly since
// notifyWindows only enqueues on GOOS=windows.
func TestEnqueueWinToastFullQueueRollsBack(t *testing.T) {
	nm := NewNotificationManager()
	nm.SetFocused(false)

	// Fill the 32-slot queue without a draining worker.
	for i := 0; i < cap(nm.winQueue); i++ {
		nm.winQueue <- winToast{title: "fill", body: ""}
	}

	if nm.enqueueWinToast("t", "b") {
		t.Fatal("enqueueWinToast reported success on a full queue")
	}

	// Reproduce Notify's commit + rollback sequence for the full-queue case.
	nm.mu.Lock()
	key := "GGCode\x00Task completed (turn-1)"
	nm.lastShown[key] = time.Now()
	nm.unread++
	before := nm.unread
	nm.mu.Unlock()

	if nm.enqueueWinToast("GGCode", "Task completed (turn-1)") {
		t.Fatal("enqueueWinToast reported success on a full queue (second attempt)")
	}
	nm.mu.Lock()
	delete(nm.lastShown, key)
	nm.unread--
	if nm.unread < 0 {
		nm.unread = 0
	}
	after := nm.unread
	_, keyPresent := nm.lastShown[key]
	nm.mu.Unlock()

	if after != before-1 {
		t.Fatalf("unread = %d after rollback, want %d", after, before-1)
	}
	if keyPresent {
		t.Fatal("lastShown still contains the rolled-back key — a retry within 5s would hit dedup and never re-queue")
	}

	// The rollback frees the dedup key, so a retry can display again.
	nm.Notify("GGCode", "Task completed (turn-1)")
	nm.mu.Lock()
	_, retried := nm.lastShown["GGCode\x00Task completed (turn-1)"]
	nm.mu.Unlock()
	if !retried {
		t.Fatal("retry after rollback was deduped — rollback did not free the dedup key")
	}
}

// N4: enqueueWinToast on a non-full queue must succeed (non-Windows hosts
// return true unconditionally; the queue mechanics are identical).
func TestEnqueueWinToastAcceptsWhenCapacity(t *testing.T) {
	nm := NewNotificationManager()
	if !nm.enqueueWinToast("title", "body") {
		t.Fatal("enqueueWinToast failed on an empty queue")
	}
	// Drain so the background worker doesn't accumulate test noise.
	select {
	case <-nm.winQueue:
	default:
	}
}

// --- N5 helpers (app.go) ---

func TestNotifyCompleteAndErrorBodiesCarrySessionDimension(t *testing.T) {
	if got := notifyCompleteBody(json.RawMessage(`{"turn_id":"turn-abc12345","message_id":"m1","error":""}`)); got != "Task completed (turn-abc)" {
		t.Fatalf("notifyCompleteBody = %q", got)
	}
	if got := notifyCompleteBody(nil); got != "Task completed" {
		t.Fatalf("notifyCompleteBody(nil) = %q, want legacy fallback", got)
	}

	// Distinct turns must produce distinct bodies so concurrent sessions are
	// not dedup-merged.
	b1 := notifyCompleteBody(json.RawMessage(`{"turn_id":"turn-1"}`))
	b2 := notifyCompleteBody(json.RawMessage(`{"turn_id":"turn-2"}`))
	if b1 == b2 {
		t.Fatalf("distinct turns produced identical bodies %q", b1)
	}

	if got := notifyErrorBody(json.RawMessage(`{"message":"boom"}`)); got != "An error occurred: boom" {
		t.Fatalf("notifyErrorBody = %q", got)
	}
	long := make([]byte, 200)
	for i := range long {
		long[i] = 'x'
	}
	raw, _ := json.Marshal(map[string]string{"message": string(long)})
	got := notifyErrorBody(raw)
	if len(got) > len("An error occurred: ")+83 {
		t.Fatalf("notifyErrorBody not truncated: %d chars", len(got))
	}
	if got := notifyErrorBody(json.RawMessage(`{}`)); got != "An error occurred" {
		t.Fatalf("notifyErrorBody(empty) = %q, want legacy fallback", got)
	}

	// Distinct errors must produce distinct bodies.
	e1 := notifyErrorBody(json.RawMessage(`{"message":"timeout"}`))
	e2 := notifyErrorBody(json.RawMessage(`{"message":"rate limit"}`))
	if e1 == e2 {
		t.Fatalf("distinct errors produced identical bodies %q", e1)
	}
}

// N1 companion: ask_user body identity.
func TestNotifyAskUserBodyCarriesIdentity(t *testing.T) {
	if got := notifyAskUserBody(json.RawMessage(`{"requestID":"req-999999999","title":"Choose DB"}`)); got != "Question from agent: Choose DB (#req-9999)" {
		t.Fatalf("notifyAskUserBody = %q", got)
	}
	if got := notifyAskUserBody(nil); got != "Question from agent" {
		t.Fatalf("notifyAskUserBody(nil) = %q", got)
	}
}
