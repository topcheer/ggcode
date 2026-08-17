package im

// Regression tests for issue #603 (ver-92 review: 4 probes + 1 -race capture).
//
// I2  runtime.go          pruneApprovals len<32 gate blocked the #259 Deny
//                         fallback; abandoned approvals were never cleaned and
//                         waiters blocked on <-resp forever. Fixed: no size
//                         gate + a time-driven pruner goroutine.
// I4a whatsapp_adapter.go reconnect gave up permanently after ~2min outage.
//                         Fixed: unbounded retries with capped backoff; only
//                         errWhatsAppLoggedOut is terminal.
// I4b whatsapp_adapter.go Start wrote a.cancel / Stop read it without a.mu.
//                         Fixed: both under a.mu.
// I3  slack_adapter.go    deterministic auth failures retried forever.
//                         Fixed: terminal auth error codes exit the run loop.
// I1b emitter.go          close() shared the dispatcher-start sync.Once, so it
//                         was dead-on-arrival after the first enqueue.
//                         Fixed: dedicated mu/started/closed state.
// I5  binding_watcher.go  strict ModTime().After() swallowed same-mtime
//                         rewrites. Fixed: mtime+size change detection.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/permission"
)

// ---------------------------------------------------------------------------
// I2 — approval pruning must not be gated by map size, and must also run
// time-driven (no further RegisterApproval required).
// ---------------------------------------------------------------------------

// TestIssue603_PruneApprovalsBelowThresholdDenies verifies that abandoned
// approvals are denied and removed even when the approvals map never reaches
// the old 32-entry threshold. Before the fix, 31 stale approvals sat forever
// with zero cleanup and zero Deny, hanging every waiter.
func TestIssue603_PruneApprovalsBelowThresholdDenies(t *testing.T) {
	mgr := NewManager()

	const staleCount = 3 // far below the old 32 gate
	chans := make([]<-chan permission.Decision, 0, staleCount)
	for i := 0; i < staleCount; i++ {
		_, ch := mgr.RegisterApproval(ApprovalRequest{
			ToolName:    "run_command",
			Input:       `{"command":"ls"}`,
			RequestedAt: time.Now().Add(-2 * time.Hour),
		})
		chans = append(chans, ch)
	}

	// Every stale approval must have been denied (each registration's
	// synchronous prune evicts stale entries — no size gate anymore).
	for i, ch := range chans {
		select {
		case d, ok := <-ch:
			if !ok {
				t.Fatalf("stale approval %d: channel closed without a decision", i)
			}
			if d != permission.Deny {
				t.Fatalf("stale approval %d: expected Deny, got %v", i, d)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("stale approval %d: timed out waiting for Deny (prune gated by size?)", i)
		}
	}

	mgr.mu.RLock()
	remaining := len(mgr.approvals)
	mgr.mu.RUnlock()
	if remaining != 0 {
		t.Fatalf("expected 0 approvals left after pruning stale entries, got %d", remaining)
	}

	// A fresh approval must survive pruning (no over-eager eviction).
	_, freshCh := mgr.RegisterApproval(ApprovalRequest{
		ToolName:    "run_command",
		Input:       `{"command":"pwd"}`,
		RequestedAt: time.Now(),
	})
	select {
	case d := <-freshCh:
		t.Fatalf("fresh approval must not be pruned, but got decision %v", d)
	default:
	}
}

// TestIssue603_PruneApprovalsTimeDrivenDeny verifies the time-driven pruner:
// an approval that becomes stale AFTER registration (so no further
// RegisterApproval ever runs) is still denied by the background pruner.
func TestIssue603_PruneApprovalsTimeDrivenDeny(t *testing.T) {
	orig := approvalPruneInterval
	approvalPruneInterval = 30 * time.Millisecond
	t.Cleanup(func() { approvalPruneInterval = orig })

	mgr := NewManager()
	_, ch := mgr.RegisterApproval(ApprovalRequest{
		ToolName:    "run_command",
		Input:       `{"command":"ls"}`,
		RequestedAt: time.Now(), // fresh at registration: survives sync prune
	})

	// Age the approval behind the manager lock so only the background
	// pruner can evict it.
	mgr.mu.Lock()
	for _, ap := range mgr.approvals {
		ap.state.Request.RequestedAt = time.Now().Add(-2 * time.Hour)
	}
	mgr.mu.Unlock()

	select {
	case d, ok := <-ch:
		if !ok {
			t.Fatal("time-driven pruner closed the channel without a Deny")
		}
		if d != permission.Deny {
			t.Fatalf("expected Deny from time-driven pruner, got %v", d)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("time-driven pruner never denied the abandoned approval (#603: waiters hang forever)")
	}
}

// TestIssue603_ApprovalPrunerSelfTerminates verifies the pruner goroutine
// stops itself once the approvals map is empty (no permanent background
// ticker per Manager).
func TestIssue603_ApprovalPrunerSelfTerminates(t *testing.T) {
	orig := approvalPruneInterval
	approvalPruneInterval = 20 * time.Millisecond
	t.Cleanup(func() { approvalPruneInterval = orig })

	mgr := NewManager()
	_, ch := mgr.RegisterApproval(ApprovalRequest{
		ToolName:    "run_command",
		Input:       `{"command":"ls"}`,
		RequestedAt: time.Now(),
	})
	mgr.mu.Lock()
	for _, ap := range mgr.approvals {
		ap.state.Request.RequestedAt = time.Now().Add(-2 * time.Hour)
	}
	mgr.mu.Unlock()

	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("pruner did not deny the stale approval")
	}

	deadline := time.After(3 * time.Second)
	for {
		mgr.mu.Lock()
		active := mgr.approvalPrunerActive
		remaining := len(mgr.approvals)
		mgr.mu.Unlock()
		if !active && remaining == 0 {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("pruner did not self-terminate: active=%v remaining=%d", active, remaining)
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// ---------------------------------------------------------------------------
// I4a — WhatsApp reconnect policy: unbounded retries, capped backoff,
// only LoggedOut is terminal.
// ---------------------------------------------------------------------------

// TestIssue603_WhatsAppBackoffUnboundedCapped verifies the backoff schedule
// caps at the last waBackoffs entry instead of giving up after 5 attempts.
func TestIssue603_WhatsAppBackoffUnboundedCapped(t *testing.T) {
	maxBackoff := waBackoffs[len(waBackoffs)-1]
	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{0, waBackoffs[0]},
		{1, waBackoffs[1]},
		{len(waBackoffs) - 1, maxBackoff},
		{len(waBackoffs), maxBackoff},
		{100, maxBackoff},
		{1000000, maxBackoff},
	}
	for _, tc := range cases {
		if got := waNextBackoff(tc.attempt); got != tc.want {
			t.Fatalf("waNextBackoff(%d) = %v, want %v", tc.attempt, got, tc.want)
		}
	}
}

// TestIssue603_WhatsAppTerminalOnlyLoggedOut verifies that only
// errWhatsAppLoggedOut is permanent; network-ish errors (and nil) must stay
// retryable so a multi-minute outage no longer kills the adapter.
func TestIssue603_WhatsAppTerminalOnlyLoggedOut(t *testing.T) {
	if !waTerminal(errWhatsAppLoggedOut) {
		t.Fatal("errWhatsAppLoggedOut must be terminal")
	}
	if !waTerminal(fmt.Errorf("wrapped: %w", errWhatsAppLoggedOut)) {
		t.Fatal("wrapped errWhatsAppLoggedOut must be terminal")
	}
	netErr := errors.New("dial tcp: i/o timeout")
	if waTerminal(netErr) {
		t.Fatal("network error must NOT be terminal (a ~2min outage must not kill the adapter)")
	}
	if waTerminal(nil) {
		t.Fatal("nil error must not be terminal")
	}
	if waTerminal(errors.New("EOF")) {
		t.Fatal("EOF must not be terminal")
	}
}

// ---------------------------------------------------------------------------
// I4b — Start/Stop data race on a.cancel (both must hold a.mu now).
// ---------------------------------------------------------------------------

// TestIssue603_WhatsAppStartStopConcurrent hammers concurrent Start/Stop on a
// shared adapter. Under -race this reproduces the original DATA RACE
// (unsynchronized write a.cancel in Start vs read in Stop) and must stay
// clean after the fix. The per-Start context carries a short timeout so no
// run goroutine can outlive the test by much.
func TestIssue603_WhatsAppStartStopConcurrent(t *testing.T) {
	a := &whatsappAdapter{
		name:     "race-probe",
		storeDir: t.TempDir(),
	}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			a.Start(ctx)
			time.Sleep(time.Millisecond)
			cancel()
		}()
		go func() {
			defer wg.Done()
			a.Stop()
		}()
	}
	wg.Wait()
	a.Stop()
	// Wait for every run goroutine to finish writing session state before
	// t.TempDir cleanup removes the store directory (avoided cleanup race).
	a.waitRunStopped()
}

// ---------------------------------------------------------------------------
// I3 — Slack deterministic auth failures must stop the run loop permanently.
// ---------------------------------------------------------------------------

// TestIssue603_SlackTerminalAuthStopsRetrying verifies that invalid_auth from
// auth.test exits the run loop (previously: retried every cycle forever).
func TestIssue603_SlackTerminalAuthStopsRetrying(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"ok":false,"error":"invalid_auth"}`)
	}))
	defer srv.Close()

	a := &slackAdapter{
		name:       "slack-terminal",
		httpClient: &http.Client{Timeout: 5 * time.Second},
		botToken:   "xoxb-bad",
		appToken:   "xapp-bad",
		apiBase:    srv.URL,
		manager:    NewManager(),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { a.run(ctx); close(done) }()

	select {
	case <-done:
		// Terminal auth failure exited the run loop on its own — correct.
	case <-time.After(5 * time.Second):
		t.Fatal("run loop still retrying a deterministic invalid_auth failure (#603)")
	}
}

// TestIssue603_SlackTransientAuthKeepsRetrying verifies that non-terminal
// errors still take the retry path (the fix must not over-trigger).
func TestIssue603_SlackTransientAuthKeepsRetrying(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"ok":false,"error":"internal_error"}`)
	}))
	defer srv.Close()

	a := &slackAdapter{
		name:       "slack-transient",
		httpClient: &http.Client{Timeout: 5 * time.Second},
		botToken:   "xoxb-ok",
		appToken:   "xapp-ok",
		apiBase:    srv.URL,
		manager:    NewManager(),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { a.run(ctx); close(done) }()

	// A transient error must NOT exit the loop on its own within the first
	// backoff window.
	select {
	case <-done:
		t.Fatal("run loop exited on a transient error — retry semantics broken")
	case <-time.After(400 * time.Millisecond):
	}

	// Cancellation must still stop it promptly.
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("run loop did not stop after context cancellation")
	}
}

// ---------------------------------------------------------------------------
// I1b — emitter close() must work after enqueue.
// ---------------------------------------------------------------------------

// TestIssue603_EmitterCloseAfterEnqueue verifies close() actually closes the
// channel after enqueue() has run. Before the fix, the shared sync.Once was
// consumed by the first enqueue, making close() a permanent no-op.
func TestIssue603_EmitterCloseAfterEnqueue(t *testing.T) {
	s := newIMEmitterState()
	s.enqueue(NewManager(), OutboundEvent{Kind: OutboundEventText, Text: "hello"}, "")

	// Must not panic and must actually close the channel.
	s.close()

	// A closed channel eventually yields ok==false on receive (the dispatcher
	// drains buffered items first). If close() were a no-op, this receive
	// would block forever and hit the deadline.
	deadline := time.After(3 * time.Second)
	for {
		select {
		case _, ok := <-s.ch:
			if !ok {
				return // channel confirmed closed
			}
		case <-deadline:
			t.Fatal("emitter channel never closed — close() is dead-on-arrival after enqueue (#603)")
		}
	}
}

// TestIssue603_EmitterCloseIdempotentAndDropAfterClose verifies close() is
// safe to call twice and that enqueue() after close() is a no-op (no send on
// a closed channel panic, no dispatcher restart).
func TestIssue603_EmitterCloseIdempotentAndDropAfterClose(t *testing.T) {
	s := newIMEmitterState()
	s.close()
	s.close() // second close must be a no-op

	// Enqueue after close must drop the event silently.
	s.enqueue(NewManager(), OutboundEvent{Kind: OutboundEventText, Text: "dropped"}, "")

	s.mu.Lock()
	closed := s.closed
	started := s.started
	s.mu.Unlock()
	if !closed {
		t.Fatal("emitter state must be marked closed")
	}
	if started {
		t.Fatal("enqueue after close must not start the dispatcher")
	}
}

// ---------------------------------------------------------------------------
// I5 — binding watcher must detect same-mtime rewrites (size fallback).
// ---------------------------------------------------------------------------

// TestIssue603_BindingStatChangedMatrix covers the change-detection matrix:
// mtime advance, same-mtime+size-change (the swallowed takeover case),
// unchanged, and older.
func TestIssue603_BindingStatChangedMatrix(t *testing.T) {
	t0 := time.Now()
	t1 := t0.Add(2 * time.Second)

	cases := []struct {
		name           string
		mod, lastMod   time.Time
		size, lastSize int64
		want           bool
	}{
		{"first poll (zero lastMod)", t0, time.Time{}, 100, 0, true},
		{"mtime advanced", t1, t0, 100, 100, true},
		{"same mtime, size changed (takeover swallowed before fix)", t0, t0, 200, 100, true},
		{"same mtime, same size", t0, t0, 100, 100, false},
		{"older mtime", t0.Add(-time.Second), t0, 100, 100, false},
	}
	for _, tc := range cases {
		if got := bindingStatChanged(tc.mod, tc.size, tc.lastMod, tc.lastSize); got != tc.want {
			t.Fatalf("%s: bindingStatChanged = %v, want %v", tc.name, got, tc.want)
		}
	}
}
