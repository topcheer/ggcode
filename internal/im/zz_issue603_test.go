package im

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

func TestIssue603_PruneApprovalsBelowThresholdDenies(t *testing.T) {
	mgr := NewManager()

	const staleCount = 3
	chans := make([]<-chan permission.Decision, 0, staleCount)
	for i := 0; i < staleCount; i++ {
		_, ch := mgr.RegisterApproval(ApprovalRequest{
			ToolName:    "run_command",
			Input:       `{"command":"ls"}`,
			RequestedAt: time.Now().Add(-2 * time.Hour),
		})
		chans = append(chans, ch)
	}

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

func TestIssue603_PruneApprovalsTimeDrivenDeny(t *testing.T) {
	orig := approvalPruneInterval
	approvalPruneInterval = 30 * time.Millisecond
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

	a.waitRunStopped()
}

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

	case <-time.After(5 * time.Second):
		t.Fatal("run loop still retrying a deterministic invalid_auth failure (#603)")
	}
}

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

	select {
	case <-done:
		t.Fatal("run loop exited on a transient error — retry semantics broken")
	case <-time.After(400 * time.Millisecond):
	}

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("run loop did not stop after context cancellation")
	}
}

func TestIssue603_EmitterCloseAfterEnqueue(t *testing.T) {
	s := newIMEmitterState()
	s.enqueue(NewManager(), OutboundEvent{Kind: OutboundEventText, Text: "hello"}, "")

	s.close()

	deadline := time.After(3 * time.Second)
	for {
		select {
		case _, ok := <-s.ch:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("emitter channel never closed — close() is dead-on-arrival after enqueue (#603)")
		}
	}
}

func TestIssue603_EmitterCloseIdempotentAndDropAfterClose(t *testing.T) {
	s := newIMEmitterState()
	s.close()
	s.close()

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
